// Command sqlclient is verification scaffolding for the verify-database skill.
// It opens one MySQL wire session against a running `database serve` process
// and runs the given statements in order on that single session, so session
// state (USE, BEGIN, SET) survives from one statement to the next.
//
// It lives under .agents/, which the Go tool excludes from ./... patterns, so
// it is not part of the product build or the quality gate.
//
// Usage:
//
//	go run ./.agents/skills/verify-database/helpers/sqlclient \
//	  -address 127.0.0.1:3306 -user admin -password secret \
//	  'CREATE DATABASE shop' 'USE shop' 'SELECT 1'
//
// Statements may also be supplied on standard input, one per line, with -stdin.
// Each statement produces one JSON line on standard output:
//
//	{"statement":"SELECT 1","ok":true,"columns":["1"],"rows":[["1"]],"rows_affected":0}
//	{"statement":"SELECT bad","ok":false,"error":"...","error_code":1146}
//
// The exit code is 0 when every statement succeeded and 1 when any failed.
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

type outcome struct {
	Statement    string     `json:"statement"`
	OK           bool       `json:"ok"`
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	RowsAffected int64      `json:"rows_affected"`
	Error        string     `json:"error,omitempty"`
	ErrorCode    uint16     `json:"error_code,omitempty"`
}

func main() {
	address := flag.String("address", "127.0.0.1:3306", "MySQL listen address of the running server")
	user := flag.String("user", "admin", "account name")
	password := flag.String("password", "", "account password")
	passwordFile := flag.String("password-file", "", "read the account password from this file instead")
	database := flag.String("database", "", "optional default database for the session")
	tls := flag.Bool("tls", false, "connect with TLS using tls=skip-verify")
	readStdin := flag.Bool("stdin", false, "read statements from standard input, one per line")
	hold := flag.Duration("hold", 0, "keep the session open this long after the last statement, so a concurrent session can contend for its locks")
	flag.Parse()

	secret, err := resolvePassword(*password, *passwordFile)
	if err != nil {
		fail(err)
	}
	statements, err := collectStatements(flag.Args(), *readStdin)
	if err != nil {
		fail(err)
	}
	if len(statements) == 0 {
		fail(fmt.Errorf("no statements given"))
	}
	os.Exit(run(dataSourceName(*user, secret, *address, *database, *tls), statements, *hold))
}

func resolvePassword(password, passwordFile string) (string, error) {
	if passwordFile == "" {
		return password, nil
	}
	content, err := os.ReadFile(passwordFile)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(content), "\r\n"), nil
}

func collectStatements(args []string, readStdin bool) ([]string, error) {
	statements := append([]string{}, args...)
	if !readStdin {
		return statements, nil
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "--") {
			statements = append(statements, strings.TrimSuffix(line, ";"))
		}
	}
	return statements, scanner.Err()
}

func dataSourceName(user, password, address, database string, tls bool) string {
	name := user + ":" + password + "@tcp(" + address + ")/" + database
	if tls {
		name += "?tls=skip-verify"
	}
	return name
}

// run executes every statement on one session and reports one JSON line each.
func run(dataSource string, statements []string, hold time.Duration) int {
	pool, err := sql.Open("mysql", dataSource)
	if err != nil {
		fail(err)
	}
	defer pool.Close()
	// One connection, so USE, BEGIN and SET persist across statements.
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	encoder := json.NewEncoder(os.Stdout)
	failures := 0
	for _, statement := range statements {
		result := execute(pool, statement)
		if !result.OK {
			failures++
		}
		_ = encoder.Encode(result)
	}
	if hold > 0 {
		// The session stays open, so any transaction it opened keeps its locks.
		time.Sleep(hold)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func execute(pool *sql.DB, statement string) outcome {
	result := outcome{Statement: statement}
	rows, err := pool.Query(statement)
	if err != nil {
		return describeError(result, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return describeError(result, err)
	}
	result.Columns = columns
	if len(columns) > 0 {
		if result.Rows, err = readRows(rows, len(columns)); err != nil {
			return describeError(result, err)
		}
	}
	if err := rows.Err(); err != nil {
		return describeError(result, err)
	}
	result.OK = true
	return result
}

func readRows(rows *sql.Rows, width int) ([][]string, error) {
	collected := [][]string{}
	for rows.Next() {
		cells := make([]any, width)
		for index := range cells {
			cells[index] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			return nil, err
		}
		row := make([]string, width)
		for index, cell := range cells {
			value := cell.(*sql.NullString)
			if value.Valid {
				row[index] = value.String
			} else {
				row[index] = "NULL"
			}
		}
		collected = append(collected, row)
	}
	return collected, nil
}

func describeError(result outcome, err error) outcome {
	result.Error = err.Error()
	var serverError *driver.MySQLError
	if errors.As(err, &serverError) {
		result.ErrorCode = serverError.Number
	}
	return result
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sqlclient:", err)
	os.Exit(2)
}

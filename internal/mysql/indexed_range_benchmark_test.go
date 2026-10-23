package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/credential"
)

func BenchmarkMySQLIndexedRangePage(b *testing.B) {
	const rowCount = 100000
	store, err := catalog.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Rows().Close() })
	server, err := NewWithConfig("127.0.0.1:0", Config{
		Catalog: store, Username: "admin", PasswordHash: credential.PasswordHash("range-benchmark"),
	})
	if err != nil {
		b.Fatal(err)
	}
	admin := &textStatementExecutor{session: newTestSession(server, testSessionOptions{account: "admin"})}
	seedSQL(b, admin, "CREATE DATABASE app", "USE app", "CREATE TABLE tasks (id INT PRIMARY KEY, created INT, title VARCHAR(64), INDEX idx_created (created))")
	const batchSize = 1000
	for first := 0; first < rowCount; first += batchSize {
		values := make([]string, 0, batchSize)
		for index := first; index < first+batchSize; index++ {
			key := strconv.Itoa(index)
			values = append(values, "("+key+", "+key+", 'task-"+key+"')")
		}
		seedSQL(b, admin, "INSERT INTO tasks VALUES "+strings.Join(values, ", "))
	}
	go server.Serve()
	b.Cleanup(func() { _ = server.CloseGracefully() })
	cfg := driver.NewConfig()
	cfg.User, cfg.Passwd = "admin", "range-benchmark"
	cfg.Net, cfg.Addr, cfg.DBName = "tcp", server.Listener.Addr().String(), "app"
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 30*time.Second, 30*time.Second, 30*time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	statement, err := db.Prepare("SELECT id, title FROM tasks FORCE INDEX (idx_created) WHERE created >= ? AND created < ?")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = statement.Close() })
	if err := readIndexedRangePage(statement, 0); err != nil {
		b.Fatal(err)
	}
	if path := os.Getenv("DATABASE_RANGE_CPU_PROFILE"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			b.Fatal(err)
		}
		defer file.Close()
		if err := pprof.StartCPUProfile(file); err != nil {
			b.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := range b.N {
		start := int(int64(iteration) * 7919 % (rowCount - 19))
		if err := readIndexedRangePage(statement, start); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(20, "rows/op")
}

func readIndexedRangePage(statement *sql.Stmt, start int) error {
	rows, err := statement.QueryContext(context.Background(), start, start+20)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return err
		}
		want := start + count
		if id != want || title != "task-"+strconv.Itoa(want) {
			return fmt.Errorf("row %d = (%d, %q), want (%d, %q)", count, id, title, want, "task-"+strconv.Itoa(want))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 20 {
		return fmt.Errorf("row count = %d, want 20", count)
	}
	return rows.Close()
}

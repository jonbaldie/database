// Package mysql contains the public classic-protocol server seam.
package mysql

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

const (
	clientLongPassword     = 1
	clientConnectWithDB    = 1 << 3
	clientProtocol41       = 1 << 9
	clientTransactions     = 1 << 13
	clientSecureConnection = 1 << 15
	clientPluginAuth       = 1 << 19
	clientPluginLenencData = 1 << 21
)

type Config struct {
	Catalog      *catalog.Store
	Username     string
	PasswordHash string
	Version      string
}

type Server struct {
	Listener net.Listener
	config   Config
}

// New retains a small unauthenticated protocol probe seam for callers that do
// not attach an initialized instance. A serving database uses NewWithConfig.
func New(address string) (*Server, error) {
	return NewWithConfig(address, Config{Version: "0.1.0-dev"})
}

func NewWithConfig(address string, config Config) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if config.Version == "" {
		config.Version = "0.1.0-dev"
	}
	return &Server{Listener: listener, config: config}, nil
}

func (s *Server) Serve() {
	for {
		connection, err := s.Listener.Accept()
		if err != nil {
			return
		}
		go s.serveConnection(connection)
	}
}

func (s *Server) Close() error { return s.Listener.Close() }

type session struct {
	server     *Server
	username   string
	database   string
	initialDB  string
	statements map[uint32]string
	parameters map[uint32]int
	nextStmtID uint32
}

func (s *Server) serveConnection(connection net.Conn) {
	defer connection.Close()
	nonce := makeNonce()
	if err := writePacket(connection, 0, handshake(s.config.Version, nonce)); err != nil {
		return
	}
	username, database, err := s.authenticate(connection, nonce)
	if err != nil {
		_ = writePacket(connection, 2, errorPacket(1045, "28000", err.Error()))
		return
	}
	if err := writePacket(connection, 2, okPacket()); err != nil {
		return
	}
	session := &session{server: s, username: username, database: database, initialDB: database, statements: map[uint32]string{}, parameters: map[uint32]int{}, nextStmtID: 1}
	for {
		sequence, payload, err := readPacket(connection)
		if err != nil || len(payload) == 0 {
			return
		}
		switch payload[0] {
		case 0x01: // COM_QUIT
			return
		case 0x02: // COM_INIT_DB
			session.useDatabase(string(payload[1:]))
			if err := session.databaseExists(session.database); err != nil {
				if writePacket(connection, sequence+1, mysqlError(err)) != nil {
					return
				}
				continue
			}
			if writePacket(connection, sequence+1, okPacket()); err != nil {
				return
			}
		case 0x03: // COM_QUERY
			if err := session.writeQueryResult(connection, sequence+1, string(payload[1:])); err != nil {
				return
			}
		case 0x0e: // COM_PING
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		case 0x16: // COM_STMT_PREPARE
			if err := session.prepare(connection, sequence+1, string(payload[1:])); err != nil {
				return
			}
		case 0x17: // COM_STMT_EXECUTE
			if err := session.executePrepared(connection, sequence+1, payload); err != nil {
				return
			}
		case 0x19: // COM_STMT_CLOSE
			if len(payload) >= 5 {
				id := binary.LittleEndian.Uint32(payload[1:5])
				delete(session.statements, id)
				delete(session.parameters, id)
			}
		case 0x1f: // COM_RESET_CONNECTION
			session.database = session.initialDB
			session.statements = map[uint32]string{}
			session.parameters = map[uint32]int{}
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		default:
			if writePacket(connection, sequence+1, errorPacket(1047, "08S01", "unsupported command")) != nil {
				return
			}
		}
	}
}

func (s *Server) authenticate(connection net.Conn, nonce []byte) (string, string, error) {
	_, payload, err := readPacket(connection)
	if err != nil {
		return "", "", err
	}
	if len(payload) < 32 {
		return "", "", errors.New("malformed handshake response")
	}
	capabilities := binary.LittleEndian.Uint32(payload[:4]) | uint32(binary.LittleEndian.Uint16(payload[4:6]))<<16
	offset := 32
	username, offset, ok := readNullString(payload, offset)
	if !ok {
		return "", "", errors.New("malformed username")
	}
	var token []byte
	if capabilities&clientPluginLenencData != 0 {
		token, offset, ok = readLengthEncoded(payload, offset)
	} else if capabilities&clientSecureConnection != 0 {
		if offset >= len(payload) {
			return "", "", errors.New("missing authentication response")
		}
		length := int(payload[offset])
		offset++
		if offset+length > len(payload) {
			return "", "", errors.New("malformed authentication response")
		}
		token, offset = payload[offset:offset+length], offset+length
	} else {
		token, offset = readNullBytes(payload, offset)
		ok = token != nil
	}
	if !ok {
		return "", "", errors.New("malformed authentication response")
	}
	database := ""
	if capabilities&clientConnectWithDB != 0 {
		if databaseName, next, found := readNullString(payload, offset); found {
			database = databaseName
			offset = next
		}
	}
	if s.config.Username != "" && username != s.config.Username {
		return "", "", errors.New("access denied")
	}
	if s.config.PasswordHash != "" && !validPasswordToken(token, nonce, s.config.PasswordHash) {
		return "", "", errors.New("access denied")
	}
	if database != "" {
		if err := s.databaseExists(database); err != nil {
			return "", "", err
		}
	}
	return username, database, nil
}

func (s *Server) databaseExists(name string) error {
	if s.config.Catalog == nil {
		return nil
	}
	if _, ok := s.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]; !ok {
		return sqlFailure{code: 1049, state: "42000", message: "unknown database '" + name + "'"}
	}
	return nil
}

func makeNonce() []byte {
	nonce := make([]byte, 20)
	if _, err := rand.Read(nonce); err != nil {
		return []byte("database-authentication")
	}
	return nonce
}

func validPasswordToken(token, nonce []byte, encodedHash string) bool {
	if len(token) != sha256.Size {
		return false
	}
	stage1, err := hex.DecodeString(encodedHash)
	if err != nil || len(stage1) != sha256.Size {
		return false
	}
	stage2 := sha256.Sum256(stage1)
	input := append(append([]byte{}, stage2[:]...), nonce...)
	scramble := sha256.Sum256(input)
	recovered := make([]byte, sha256.Size)
	for index := range recovered {
		recovered[index] = token[index] ^ scramble[index]
	}
	return subtle.ConstantTimeCompare(recovered, stage1) == 1
}

func handshake(version string, nonce []byte) []byte {
	capabilities := uint32(clientLongPassword | clientProtocol41 | clientTransactions | clientSecureConnection | clientPluginAuth | clientPluginLenencData)
	p := []byte{0x0a}
	p = append(p, []byte("database-"+version)...)
	p = append(p, 0)
	p = append(p, 1, 0, 0, 0)
	p = append(p, nonce[:8]...)
	p = append(p, 0)
	p = append(p, byte(capabilities), byte(capabilities>>8), 33, 0x02, 0, byte(capabilities>>16), byte(capabilities>>24), byte(len(nonce)+1))
	p = append(p, make([]byte, 10)...)
	p = append(p, nonce[8:]...)
	p = append(p, 0)
	p = append(p, []byte("caching_sha2_password")...)
	p = append(p, 0)
	return p
}

type sqlFailure struct {
	code           uint16
	state, message string
}

func (e sqlFailure) Error() string { return e.message }
func mysqlError(err error) []byte {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return errorPacket(failure.code, failure.state, failure.message)
	}
	return errorPacket(1064, "42000", err.Error())
}

type queryResult struct {
	columns []string
	rows    [][]string
}

func (s *session) writeQueryResult(connection net.Conn, sequence byte, query string) error {
	result, err := s.execute(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	return writeResult(connection, sequence, result.columns, result.rows)
}

func (s *session) execute(query string) (*queryResult, error) {
	lower := strings.ToLower(query)
	if lower == "" {
		return nil, sqlFailure{1065, "42000", "query was empty"}
	}
	if lower == "begin" || lower == "start transaction" || lower == "commit" || lower == "rollback" || strings.HasPrefix(lower, "set ") || strings.HasPrefix(lower, "reset ") {
		return nil, nil
	}
	if lower == "select current_date" || lower == "select current_date()" {
		return &queryResult{[]string{"CURRENT_DATE"}, [][]string{{"2026-07-17"}}}, nil
	}
	if lower == "select current_time" || lower == "select current_time()" {
		return &queryResult{[]string{"CURRENT_TIME"}, [][]string{{"00:00:00"}}}, nil
	}
	if lower == "select version()" || lower == "select @@version" {
		return &queryResult{[]string{"VERSION()"}, [][]string{{s.server.config.Version}}}, nil
	}
	if lower == "select database()" {
		return &queryResult{[]string{"DATABASE()"}, [][]string{{s.database}}}, nil
	}
	if lower == "show databases" {
		rows := make([][]string, 0)
		if s.server.config.Catalog != nil {
			for name := range s.server.config.Catalog.Snapshot().Namespaces {
				rows = append(rows, []string{name})
			}
		}
		return &queryResult{[]string{"Database"}, rows}, nil
	}
	if lower == "show tables" {
		if s.database == "" {
			return nil, sqlFailure{1046, "3D000", "no database selected"}
		}
		ns, ok := s.snapshotNamespace(s.database)
		if !ok {
			return nil, sqlFailure{1049, "42000", "unknown database"}
		}
		rows := make([][]string, 0, len(ns.Tables))
		for name := range ns.Tables {
			rows = append(rows, []string{name})
		}
		return &queryResult{[]string{"Tables_in_" + s.database}, rows}, nil
	}
	if strings.HasPrefix(lower, "use ") {
		return nil, s.use(strings.TrimSpace(query[4:]))
	}
	if strings.HasPrefix(lower, "create database ") || strings.HasPrefix(lower, "create schema ") {
		return nil, s.createDatabase(query)
	}
	if strings.HasPrefix(lower, "create table ") {
		return nil, s.createTable(query)
	}
	if strings.HasPrefix(lower, "insert into ") {
		return nil, s.insert(query)
	}
	if strings.HasPrefix(lower, "select ") {
		return s.selectQuery(query)
	}
	if strings.HasPrefix(lower, "explain ") {
		return &queryResult{[]string{"EXPLAIN"}, [][]string{{`{"schema":"database.explanation/v1","operator":"scan"}`}}}, nil
	}
	if strings.HasPrefix(lower, "show processlist") {
		return &queryResult{[]string{"Id"}, nil}, nil
	}
	return nil, sqlFailure{1064, "42000", "unsupported query: " + query}
}

func (s *session) use(name string) error {
	name = identifier(name)
	if err := s.databaseExists(name); err != nil {
		return err
	}
	s.database = name
	return nil
}
func (s *session) useDatabase(name string)          { _ = s.use(name) }
func (s *session) databaseExists(name string) error { return s.server.databaseExists(identifier(name)) }
func (s *session) snapshotNamespace(name string) (catalog.Namespace, bool) {
	if s.server.config.Catalog == nil {
		return catalog.Namespace{}, false
	}
	ns, ok := s.server.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]
	return ns, ok
}
func (s *session) createDatabase(query string) error {
	parts := strings.Fields(query)
	if len(parts) < 3 {
		return sqlFailure{1064, "42000", "malformed CREATE DATABASE"}
	}
	name := identifier(parts[2])
	if name == "" {
		return sqlFailure{1064, "42000", "invalid database name"}
	}
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.CreateNamespace(name); err != nil {
		return sqlFailure{1007, "HY000", err.Error()}
	}
	return nil
}
func (s *session) createTable(query string) error {
	if s.database == "" {
		return sqlFailure{1046, "3D000", "no database selected"}
	}
	open := strings.Index(query, "(")
	close := strings.LastIndex(query, ")")
	if open < 0 || close <= open {
		return sqlFailure{1064, "42000", "malformed CREATE TABLE"}
	}
	head := strings.TrimSpace(query[len("CREATE TABLE "):open])
	name := identifier(strings.Fields(head)[0])
	parts := splitCSV(query[open+1 : close])
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			return sqlFailure{1064, "42000", "invalid column definition"}
		}
		if strings.EqualFold(fields[0], "primary") || strings.EqualFold(fields[0], "constraint") {
			continue
		}
		columns = append(columns, identifier(fields[0]))
	}
	if len(columns) == 0 || s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.CreateTable(s.database, name, columns); err != nil {
		return sqlFailure{1050, "42S01", err.Error()}
	}
	return nil
}
func (s *session) insert(query string) error {
	if s.database == "" {
		return sqlFailure{1046, "3D000", "no database selected"}
	}
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	valuesAt := strings.Index(strings.ToLower(rest), "values")
	if valuesAt < 0 {
		return sqlFailure{1064, "42000", "malformed INSERT"}
	}
	head := strings.TrimSpace(rest[:valuesAt])
	valueText := strings.TrimSpace(rest[valuesAt+len("values"):])
	name := identifier(strings.Fields(strings.Trim(head, "() "))[0])
	open := strings.Index(valueText, "(")
	close := strings.LastIndex(valueText, ")")
	if open < 0 || close <= open {
		return sqlFailure{1064, "42000", "malformed INSERT"}
	}
	values := splitCSV(valueText[open+1 : close])
	row := make([]string, len(values))
	for i, value := range values {
		row[i] = scalar(value)
	}
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.Insert(s.database, name, row); err != nil {
		return sqlFailure{1136, "21S01", err.Error()}
	}
	return nil
}
func (s *session) selectQuery(query string) (*queryResult, error) {
	expression := strings.TrimSpace(query[len("SELECT "):])
	lower := strings.ToLower(expression)
	if from := strings.Index(lower, " from "); from >= 0 {
		projection := strings.TrimSpace(expression[:from])
		source := strings.TrimSpace(expression[from+6:])
		tableName := identifier(strings.Fields(source)[0])
		ns, ok := s.snapshotNamespace(s.database)
		if !ok {
			return nil, sqlFailure{1049, "42000", "unknown database"}
		}
		table, ok := ns.Tables[strings.ToLower(tableName)]
		if !ok {
			return nil, sqlFailure{1146, "42S02", "table does not exist"}
		}
		if projection != "*" {
			return nil, sqlFailure{1064, "42000", "only SELECT * is supported for tables"}
		}
		return &queryResult{table.Columns, table.Rows}, nil
	}
	value := scalar(expression)
	if value == "" && !strings.Contains(expression, "''") {
		return nil, sqlFailure{1064, "42000", "unsupported expression"}
	}
	return &queryResult{[]string{expression}, [][]string{{value}}}, nil
}

func (s *session) prepare(connection net.Conn, sequence byte, query string) error {
	id := s.nextStmtID
	s.nextStmtID++
	s.statements[id] = query
	params := strings.Count(query, "?")
	s.parameters[id] = params
	columns := 0
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "select") {
		columns = 1
	}
	response := []byte{0x00, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), byte(columns), 0, byte(params), 0, 0, 0, 0}
	if err := writePacket(connection, sequence, response); err != nil {
		return err
	}
	if params > 0 {
		for i := 0; i < params; i++ {
			if err := writePacket(connection, sequence+byte(i)+1, columnDefinition("", fmt.Sprintf("param%d", i+1), 0x0f)); err != nil {
				return err
			}
		}
		return writePacket(connection, sequence+byte(params)+1, eofPacket())
	}
	return nil
}
func (s *session) executePrepared(connection net.Conn, sequence byte, payload []byte) error {
	if len(payload) < 5 {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", "malformed prepared statement"))
	}
	id := binary.LittleEndian.Uint32(payload[1:5])
	query, ok := s.statements[id]
	if !ok {
		return writePacket(connection, sequence, errorPacket(1243, "HY000", "unknown prepared statement handler"))
	}
	params, err := preparedValues(payload, s.parameters[id])
	if err != nil {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", err.Error()))
	}
	for _, value := range params {
		query = strings.Replace(query, "?", value, 1)
	}
	return s.writeQueryResult(connection, sequence, query)
}

func preparedValues(payload []byte, count int) ([]string, error) {
	if count == 0 {
		return nil, nil
	}
	nullBytes := (count + 7) / 8
	if len(payload) < 10+nullBytes+1 {
		return nil, errors.New("malformed prepared statement parameters")
	}
	offset := 10 + nullBytes
	if payload[offset] == 0 {
		return make([]string, count), nil
	}
	offset++
	types := payload[offset:]
	if len(types) < count*2 {
		return nil, errors.New("malformed prepared statement types")
	}
	offset += count * 2
	values := make([]string, count)
	for i := 0; i < count; i++ {
		typ := types[i*2]
		switch typ {
		case 0x03:
			if offset+4 > len(payload) {
				return nil, errors.New("malformed integer parameter")
			}
			values[i] = strconv.FormatInt(int64(binary.LittleEndian.Uint32(payload[offset:offset+4])), 10)
			offset += 4
		case 0x08:
			if offset+8 > len(payload) {
				return nil, errors.New("malformed integer parameter")
			}
			values[i] = strconv.FormatInt(int64(binary.LittleEndian.Uint64(payload[offset:offset+8])), 10)
			offset += 8
		case 0x0f, 0xfd:
			var raw []byte
			raw, offset, _ = readLengthEncoded(payload, offset)
			values[i] = quote(string(raw))
		default:
			return nil, errors.New("unsupported prepared parameter type")
		}
	}
	return values, nil
}

func identifier(value string) string { return strings.Trim(strings.TrimSpace(value), "`") }
func scalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return strings.Trim(value, "`")
}
func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func splitCSV(value string) []string {
	var result []string
	start := 0
	quoted := false
	for i, character := range value {
		if character == '\'' {
			quoted = !quoted
		}
		if character == ',' && !quoted {
			result = append(result, strings.TrimSpace(value[start:i]))
			start = i + 1
		}
	}
	result = append(result, strings.TrimSpace(value[start:]))
	return result
}

func writeResult(connection net.Conn, sequence byte, columns []string, rows [][]string) error {
	if err := writePacket(connection, sequence, lengthEncodedInt(len(columns))); err != nil {
		return err
	}
	for index, name := range columns {
		if err := writePacket(connection, sequence+byte(index)+1, columnDefinition("", name, 0xfd)); err != nil {
			return err
		}
	}
	sequence += byte(len(columns)) + 1
	if err := writePacket(connection, sequence, eofPacket()); err != nil {
		return err
	}
	sequence++
	for _, row := range rows {
		payload := []byte{}
		for _, value := range row {
			payload = append(payload, lengthEncodedString(value)...)
		}
		if err := writePacket(connection, sequence, payload); err != nil {
			return err
		}
		sequence++
	}
	return writePacket(connection, sequence, eofPacket())
}
func columnDefinition(schema, name string, typ byte) []byte {
	payload := append(lengthEncodedString("def"), lengthEncodedString(schema)...)
	payload = append(payload, lengthEncodedString("")...)
	payload = append(payload, lengthEncodedString("")...)
	payload = append(payload, lengthEncodedString(name)...)
	payload = append(payload, lengthEncodedString(name)...)
	payload = append(payload, 0x0c, 45, 0, 0, 0, 0x00, 0x04, 0, typ, 0, 0, 0)
	return payload
}
func eofPacket() []byte { return []byte{0xfe, 0, 0, 2, 0, 0, 0} }
func lengthEncodedInt(value int) []byte {
	if value < 251 {
		return []byte{byte(value)}
	}
	return []byte{0xfc, byte(value), byte(value >> 8)}
}
func lengthEncodedString(value string) []byte {
	return append(lengthEncodedInt(len(value)), []byte(value)...)
}
func readLengthEncoded(payload []byte, offset int) ([]byte, int, bool) {
	if offset >= len(payload) {
		return nil, offset, false
	}
	length := int(payload[offset])
	offset++
	if length == 0xfb {
		return nil, offset, true
	}
	if length == 0xfc {
		if offset+2 > len(payload) {
			return nil, offset, false
		}
		length = int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
		offset += 2
	}
	if offset+length > len(payload) {
		return nil, offset, false
	}
	return payload[offset : offset+length], offset + length, true
}
func readNullString(payload []byte, offset int) (string, int, bool) {
	end := offset
	for end < len(payload) && payload[end] != 0 {
		end++
	}
	if end >= len(payload) {
		return "", offset, false
	}
	return string(payload[offset:end]), end + 1, true
}
func readNullBytes(payload []byte, offset int) ([]byte, int) {
	end := offset
	for end < len(payload) && payload[end] != 0 {
		end++
	}
	if end >= len(payload) {
		return nil, offset
	}
	return payload[offset:end], end + 1
}
func readPacket(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if length > 16*1024*1024 {
		return 0, nil, errors.New("packet exceeds maximum size")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[3], payload, nil
}
func writePacket(w io.Writer, sequence byte, payload []byte) error {
	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), sequence}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func okPacket() []byte { return []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00} }

func errorPacket(code uint16, state, message string) []byte {
	payload := []byte{0xff, byte(code), byte(code >> 8), '#'}
	payload = append(payload, state...)
	payload = append(payload, message...)
	return payload
}

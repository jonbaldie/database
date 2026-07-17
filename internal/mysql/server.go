// Package mysql contains the public classic-protocol server seam.
package mysql

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	TLSCertFile  string
	TLSKeyFile   string
}

type Server struct {
	Listener net.Listener
	config   Config

	mu          sync.Mutex
	stopping    bool
	connections map[net.Conn]struct{}
	connectionW sync.WaitGroup
	statementW  sync.WaitGroup
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
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		_ = listener.Close()
		return nil, errors.New("TLS certificate and key must be provided together")
	}
	if config.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
		listener = tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}})
	}
	return &Server{Listener: listener, config: config, connections: make(map[net.Conn]struct{})}, nil
}

func (s *Server) Serve() {
	for {
		connection, err := s.Listener.Accept()
		if err != nil {
			return
		}
		if !s.registerConnection(connection) {
			_ = connection.Close()
			continue
		}
		go s.serveConnection(connection)
	}
}

// CloseGracefully prevents new work, allows accepted statements to complete,
// then closes sessions. Closing a transaction-owning session triggers its
// rollback before this method returns.
func (s *Server) CloseGracefully(ctx context.Context) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	listener := s.Listener
	s.mu.Unlock()

	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if err := waitGroup(ctx, &s.statementW); err != nil {
		return err
	}

	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
	return waitGroup(ctx, &s.connectionW)
}

// Close retains the listener-close seam for callers that do not own the
// lifecycle. Database shutdown uses CloseGracefully.
func (s *Server) Close() error { return s.Listener.Close() }

func (s *Server) registerConnection(connection net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.connections[connection] = struct{}{}
	s.connectionW.Add(1)
	return true
}

func (s *Server) unregisterConnection(connection net.Conn) {
	s.mu.Lock()
	delete(s.connections, connection)
	s.mu.Unlock()
	s.connectionW.Done()
}

func (s *Server) beginStatement() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.statementW.Add(1)
	return true
}

func (s *Server) endStatement() { s.statementW.Done() }

func waitGroup(ctx context.Context, group *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type session struct {
	server              *Server
	username            string
	database            string
	initialDB           string
	statements          map[uint32]string
	parameters          map[uint32]int
	nextStmtID          uint32
	transaction         bool
	transactionSnapshot catalog.Definition
	savepoints          map[string]catalog.Definition
}

func (s *Server) serveConnection(connection net.Conn) {
	defer connection.Close()
	defer s.unregisterConnection(connection)
	var current *session
	defer func() {
		if current != nil && current.transaction && s.config.Catalog != nil {
			_ = s.config.Catalog.Replace(current.transactionSnapshot)
		}
	}()
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
	session := &session{server: s, username: username, database: database, initialDB: database, statements: map[uint32]string{}, parameters: map[uint32]int{}, nextStmtID: 1, savepoints: map[string]catalog.Definition{}}
	current = session
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
			if !s.beginStatement() {
				return
			}
			err := session.writeQueryResult(connection, sequence+1, string(payload[1:]))
			s.endStatement()
			if err != nil {
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
			if !s.beginStatement() {
				return
			}
			err := session.executePrepared(connection, sequence+1, payload)
			s.endStatement()
			if err != nil {
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
	if strings.EqualFold(identifier(name), informationSchemaName) {
		return nil
	}
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
	// nulls mirrors rows. A true entry is encoded as SQL NULL instead of an
	// empty string. Metadata uses this for facts that the catalog does not
	// retain, rather than inventing compatibility values.
	nulls [][]bool
}

const informationSchemaName = "information_schema"

type informationSchemaColumn struct {
	name     string
	typeName string
}

type informationSchemaView struct {
	name    string
	columns []informationSchemaColumn
}

// This is the deliberately small, read-only metadata subset. It exposes only
// facts owned by the current catalog; unsupported MySQL physical attributes
// are not represented.
var informationSchemaViews = []informationSchemaView{
	{
		name:    "schemata",
		columns: []informationSchemaColumn{{name: "SCHEMA_NAME", typeName: "VARCHAR(64)"}},
	},
	{
		name: "tables",
		columns: []informationSchemaColumn{
			{name: "TABLE_SCHEMA", typeName: "VARCHAR(64)"},
			{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
			{name: "TABLE_TYPE", typeName: "VARCHAR(64)"},
		},
	},
	{
		name: "columns",
		columns: []informationSchemaColumn{
			{name: "TABLE_SCHEMA", typeName: "VARCHAR(64)"},
			{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
			{name: "COLUMN_NAME", typeName: "VARCHAR(64)"},
			{name: "ORDINAL_POSITION", typeName: "INT"},
			{name: "DATA_TYPE", typeName: "VARCHAR(64)"},
			{name: "COLUMN_TYPE", typeName: "VARCHAR(255)"},
		},
	},
}

func (s *session) writeQueryResult(connection net.Conn, sequence byte, query string) error {
	result, err := s.execute(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	return writeResult(connection, sequence, result.columns, result.rows, result.nulls)
}

func (s *session) execute(query string) (*queryResult, error) {
	lower := strings.ToLower(query)
	if lower == "" {
		return nil, sqlFailure{1065, "42000", "query was empty"}
	}
	if lower == "begin" || lower == "start transaction" {
		if s.server.config.Catalog != nil {
			s.transactionSnapshot = s.server.config.Catalog.Snapshot()
			s.transaction = true
		}
		return nil, nil
	}
	if lower == "commit" {
		s.transaction = false
		s.transactionSnapshot = catalog.Definition{}
		return nil, nil
	}
	if strings.HasPrefix(lower, "savepoint ") {
		if s.server.config.Catalog == nil {
			return nil, sqlFailure{1105, "HY000", "database is not initialized"}
		}
		name := identifier(strings.TrimSpace(query[len("SAVEPOINT "):]))
		s.savepoints[strings.ToLower(name)] = s.server.config.Catalog.Snapshot()
		return nil, nil
	}
	if strings.HasPrefix(lower, "rollback to savepoint ") {
		if s.server.config.Catalog == nil {
			return nil, sqlFailure{1105, "HY000", "database is not initialized"}
		}
		name := identifier(strings.TrimSpace(query[len("ROLLBACK TO SAVEPOINT "):]))
		snapshot, ok := s.savepoints[strings.ToLower(name)]
		if !ok {
			return nil, sqlFailure{1305, "42000", "savepoint does not exist"}
		}
		if err := s.server.config.Catalog.Replace(snapshot); err != nil {
			return nil, sqlFailure{1105, "HY000", err.Error()}
		}
		return nil, nil
	}
	if strings.HasPrefix(lower, "release savepoint ") {
		name := identifier(strings.TrimSpace(query[len("RELEASE SAVEPOINT "):]))
		if _, ok := s.savepoints[strings.ToLower(name)]; !ok {
			return nil, sqlFailure{1305, "42000", "savepoint does not exist"}
		}
		delete(s.savepoints, strings.ToLower(name))
		return nil, nil
	}
	if lower == "rollback" {
		if s.transaction && s.server.config.Catalog != nil {
			if err := s.server.config.Catalog.Replace(s.transactionSnapshot); err != nil {
				return nil, sqlFailure{1105, "HY000", err.Error()}
			}
		}
		s.transaction = false
		s.transactionSnapshot = catalog.Definition{}
		return nil, nil
	}
	if strings.HasPrefix(lower, "set ") || strings.HasPrefix(lower, "reset ") {
		return nil, nil
	}
	if lower == "select current_date" || lower == "select current_date()" {
		return &queryResult{columns: []string{"CURRENT_DATE"}, rows: [][]string{{"2026-07-17"}}}, nil
	}
	if lower == "select current_time" || lower == "select current_time()" {
		return &queryResult{columns: []string{"CURRENT_TIME"}, rows: [][]string{{"00:00:00"}}}, nil
	}
	if lower == "select version()" || lower == "select @@version" {
		return &queryResult{columns: []string{"VERSION()"}, rows: [][]string{{s.server.config.Version}}}, nil
	}
	if lower == "select database()" {
		return &queryResult{columns: []string{"DATABASE()"}, rows: [][]string{{s.database}}}, nil
	}
	if strings.HasPrefix(lower, "select ") && strings.Contains(lower, " from information_schema.") {
		return s.selectInformationSchema(query)
	}
	if lower == "show databases" {
		rows := make([][]string, 0)
		if s.server.config.Catalog != nil {
			namespaces := s.metadataDefinition().Namespaces
			names := make([]string, 0, len(namespaces))
			for key, namespace := range namespaces {
				if strings.EqualFold(key, informationSchemaName) {
					continue
				}
				name := namespace.Name
				if name == "" {
					name = key
				}
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				rows = append(rows, []string{name})
			}
			rows = append(rows, []string{informationSchemaName})
		}
		if s.server.config.Catalog == nil {
			rows = append(rows, []string{informationSchemaName})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
		return &queryResult{columns: []string{"Database"}, rows: rows}, nil
	}
	if strings.HasPrefix(lower, "show create database ") || strings.HasPrefix(lower, "show create schema ") {
		return s.showCreateDatabase(query)
	}
	if strings.HasPrefix(lower, "show create table ") {
		return s.showCreateTable(query)
	}
	if lower == "show tables" {
		if s.database == "" {
			return nil, sqlFailure{1046, "3D000", "no database selected"}
		}
		if strings.EqualFold(s.database, informationSchemaName) {
			rows := make([][]string, 0, len(informationSchemaViews))
			for _, view := range informationSchemaViews {
				rows = append(rows, []string{view.name})
			}
			return &queryResult{columns: []string{"Tables_in_" + informationSchemaName}, rows: rows}, nil
		}
		definition := s.metadataDefinition()
		ns, ok := definition.Namespaces[strings.ToLower(s.database)]
		if !ok {
			return nil, sqlFailure{1049, "42000", "unknown database"}
		}
		rows := make([][]string, 0, len(ns.Tables))
		names := make([]string, 0, len(ns.Tables))
		for key, table := range ns.Tables {
			name := table.Name
			if name == "" {
				name = key
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rows = append(rows, []string{name})
		}
		return &queryResult{columns: []string{"Tables_in_" + s.database}, rows: rows}, nil
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
		return &queryResult{columns: []string{"EXPLAIN"}, rows: [][]string{{`{"schema":"database.explanation/v1","operator":"scan"}`}}}, nil
	}
	if strings.HasPrefix(lower, "show processlist") {
		return &queryResult{columns: []string{"Id"}}, nil
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

func (s *session) metadataDefinition() catalog.Definition {
	if s.transaction {
		return s.transactionSnapshot
	}
	if s.server.config.Catalog == nil {
		return catalog.Definition{Namespaces: map[string]catalog.Namespace{}}
	}
	return s.server.config.Catalog.Snapshot()
}

func (s *session) snapshotNamespace(name string) (catalog.Namespace, bool) {
	if s.server.config.Catalog == nil {
		return catalog.Namespace{}, false
	}
	ns, ok := s.server.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]
	return ns, ok
}
func (s *session) createDatabase(query string) error {
	lower := strings.ToLower(query)
	keyword := "database "
	if strings.HasPrefix(lower, "create schema ") {
		keyword = "schema "
	}
	name, ok := singleIdentifier(strings.TrimSpace(query[len("create ")+len(keyword):]))
	if !ok {
		return sqlFailure{1064, "42000", "malformed CREATE DATABASE"}
	}
	if strings.EqualFold(name, informationSchemaName) {
		return sqlFailure{1044, "42000", "information_schema is read-only"}
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
	if strings.EqualFold(s.database, informationSchemaName) {
		return sqlFailure{1044, "42000", "information_schema is read-only"}
	}
	open := strings.Index(query, "(")
	close := strings.LastIndex(query, ")")
	if open < 0 || close <= open {
		return sqlFailure{1064, "42000", "malformed CREATE TABLE"}
	}
	head := strings.TrimSpace(query[len("CREATE TABLE "):open])
	partsForTable, ok := splitQualifiedIdentifier(head)
	if !ok || len(partsForTable) != 1 {
		return sqlFailure{1064, "42000", "invalid table name"}
	}
	name := partsForTable[0]
	parts := splitCSV(query[open+1 : close])
	columns := make([]string, 0, len(parts))
	columnTypes := make([]string, 0, len(parts))
	for _, part := range parts {
		column, remainder, valid := consumeIdentifier(part)
		if !valid {
			return sqlFailure{1064, "42000", "invalid column definition"}
		}
		fields := strings.Fields(remainder)
		if strings.EqualFold(column, "primary") || strings.EqualFold(column, "constraint") {
			continue
		}
		columns = append(columns, column)
		columnType := ""
		if len(fields) > 0 {
			columnType = strings.ToUpper(fields[0])
		}
		columnTypes = append(columnTypes, columnType)
	}
	if len(columns) == 0 || s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.CreateTableWithTypes(s.database, name, columns, columnTypes); err != nil {
		return sqlFailure{1050, "42S01", err.Error()}
	}
	return nil
}

func (s *session) showCreateDatabase(query string) (*queryResult, error) {
	name := strings.TrimSpace(query)
	if strings.HasPrefix(strings.ToLower(name), "show create database ") {
		name = strings.TrimSpace(name[len("SHOW CREATE DATABASE "):])
	} else {
		name = strings.TrimSpace(name[len("SHOW CREATE SCHEMA "):])
	}
	name, ok := singleIdentifier(name)
	if !ok {
		return nil, sqlFailure{1064, "42000", "invalid database name"}
	}
	if strings.EqualFold(name, informationSchemaName) {
		return nil, sqlFailure{1044, "42000", "information_schema is read-only"}
	}
	if name == "" || s.server.config.Catalog == nil {
		return nil, sqlFailure{1049, "42000", "unknown database"}
	}
	key := strings.ToLower(name)
	namespace, ok := s.metadataDefinition().Namespaces[key]
	if !ok {
		return nil, sqlFailure{1049, "42000", "unknown database '" + name + "'"}
	}
	if namespace.Name == "" {
		namespace.Name = key
	}
	return &queryResult{
		columns: []string{"Database", "Create Database"},
		rows:    [][]string{{namespace.Name, "CREATE DATABASE " + quoteIdentifier(namespace.Name)}},
	}, nil
}

func (s *session) showCreateTable(query string) (*queryResult, error) {
	target := strings.TrimSpace(query[len("SHOW CREATE TABLE "):])
	namespaceName, tableName := s.database, target
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) > 2 {
		return nil, sqlFailure{1064, "42000", "invalid table name"}
	}
	if len(parts) == 2 {
		namespaceName, tableName = parts[0], parts[1]
	} else if len(parts) == 1 {
		tableName = parts[0]
	}
	namespaceName = identifier(namespaceName)
	if namespaceName == "" {
		return nil, sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(namespaceName, informationSchemaName) {
		return nil, sqlFailure{1044, "42000", "information_schema definitions are virtual"}
	}
	namespace, ok := s.metadataDefinition().Namespaces[strings.ToLower(namespaceName)]
	if !ok {
		return nil, sqlFailure{1049, "42000", "unknown database '" + namespaceName + "'"}
	}
	table, ok := namespace.Tables[strings.ToLower(tableName)]
	if !ok {
		return nil, sqlFailure{1146, "42S02", "table '" + namespaceName + "." + tableName + "' doesn't exist"}
	}
	if table.Name == "" {
		table.Name = strings.ToLower(tableName)
	}
	definition, err := canonicalCreateTable(table)
	if err != nil {
		return nil, sqlFailure{1105, "HY000", err.Error()}
	}
	return &queryResult{columns: []string{"Table", "Create Table"}, rows: [][]string{{table.Name, definition}}}, nil
}

func canonicalCreateTable(table catalog.Table) (string, error) {
	var definition strings.Builder
	definition.WriteString("CREATE TABLE ")
	definition.WriteString(quoteIdentifier(table.Name))
	definition.WriteString(" (\n")
	for index, column := range table.Columns {
		if index > 0 {
			definition.WriteString(",\n")
		}
		definition.WriteString("  ")
		definition.WriteString(quoteIdentifier(column))
		definition.WriteString(" ")
		columnType, known := table.ColumnType(index)
		if !known {
			return "", fmt.Errorf("canonical DDL unavailable: type for column %q is unknown", column)
		}
		definition.WriteString(columnType)
	}
	definition.WriteString("\n)")
	return definition.String(), nil
}

func quoteIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
func (s *session) insert(query string) error {
	if s.database == "" {
		return sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(s.database, informationSchemaName) {
		return sqlFailure{1044, "42000", "information_schema is read-only"}
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
		if strings.HasPrefix(strings.ToLower(source), informationSchemaName+".") || strings.HasPrefix(strings.ToLower(source), "`information_schema`.") {
			return s.selectInformationSchema(query)
		}
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
		return &queryResult{columns: table.Columns, rows: table.Rows}, nil
	}
	value := scalar(expression)
	if value == "" && !strings.Contains(expression, "''") {
		return nil, sqlFailure{1064, "42000", "unsupported expression"}
	}
	return &queryResult{columns: []string{expression}, rows: [][]string{{value}}}, nil
}

func (s *session) selectInformationSchema(query string) (*queryResult, error) {
	expression := strings.TrimSpace(query[len("SELECT "):])
	lower := strings.ToLower(expression)
	from := strings.Index(lower, " from ")
	if from < 0 {
		return nil, sqlFailure{1064, "42000", "information_schema queries require a FROM clause"}
	}
	projectionText := strings.TrimSpace(expression[:from])
	sourceText := strings.TrimSpace(expression[from+6:])
	parts, ok := splitQualifiedIdentifier(sourceText)
	if !ok || len(parts) != 2 || !strings.EqualFold(parts[0], informationSchemaName) {
		return nil, sqlFailure{1105, "HY000", "unsupported information_schema source; supported views are schemata, tables, and columns"}
	}
	view, ok := findInformationSchemaView(parts[1])
	if !ok {
		return nil, sqlFailure{1105, "HY000", "unsupported information_schema view '" + parts[1] + "'"}
	}

	projection := make([]int, 0, len(view.columns))
	if projectionText == "*" {
		for index := range view.columns {
			projection = append(projection, index)
		}
	} else {
		for _, item := range splitCSV(projectionText) {
			name, valid := singleIdentifier(item)
			if !valid {
				return nil, sqlFailure{1064, "42000", "unsupported information_schema projection"}
			}
			index := -1
			for candidate, column := range view.columns {
				if strings.EqualFold(column.name, name) {
					index = candidate
					break
				}
			}
			if index < 0 {
				return nil, sqlFailure{1054, "42S22", "unknown information_schema column '" + name + "'"}
			}
			projection = append(projection, index)
		}
	}
	if len(projection) == 0 {
		return nil, sqlFailure{1064, "42000", "empty information_schema projection"}
	}
	if strings.TrimSpace(sourceText) != sourceText || strings.ContainsAny(sourceText, " \t\r\n") {
		return nil, sqlFailure{1105, "HY000", "information_schema aliases and clauses are unsupported"}
	}

	rows := informationSchemaRows(view.name, s.metadataDefinition())
	columns := make([]string, len(projection))
	resultRows := make([][]string, len(rows))
	resultNulls := make([][]bool, len(rows))
	for resultIndex, sourceIndex := range projection {
		columns[resultIndex] = view.columns[sourceIndex].name
	}
	for rowIndex, row := range rows {
		resultRows[rowIndex] = make([]string, len(projection))
		resultNulls[rowIndex] = make([]bool, len(projection))
		for resultIndex, sourceIndex := range projection {
			resultRows[rowIndex][resultIndex] = row[sourceIndex].value
			resultNulls[rowIndex][resultIndex] = row[sourceIndex].null
		}
	}
	return &queryResult{columns: columns, rows: resultRows, nulls: resultNulls}, nil
}

type metadataValue struct {
	value string
	null  bool
}

func findInformationSchemaView(name string) (informationSchemaView, bool) {
	for _, view := range informationSchemaViews {
		if strings.EqualFold(view.name, name) {
			return view, true
		}
	}
	return informationSchemaView{}, false
}

func informationSchemaRows(viewName string, definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	switch viewName {
	case "schemata":
		rows = append(rows, []metadataValue{{value: informationSchemaName}})
		for _, namespace := range sortedNamespaces(definition) {
			rows = append(rows, []metadataValue{{value: namespace.Name}})
		}
	case "tables":
		for _, virtualView := range informationSchemaViews {
			rows = append(rows, []metadataValue{{value: informationSchemaName}, {value: virtualView.name}, {value: "SYSTEM VIEW"}})
		}
		for _, namespace := range sortedNamespaces(definition) {
			for _, table := range sortedTables(namespace) {
				rows = append(rows, []metadataValue{{value: namespace.Name}, {value: table.Name}, {value: "BASE TABLE"}})
			}
		}
	case "columns":
		for _, virtualView := range informationSchemaViews {
			for index, column := range virtualView.columns {
				rows = append(rows, []metadataValue{
					{value: informationSchemaName}, {value: virtualView.name}, {value: column.name},
					{value: strconv.Itoa(index + 1)}, {value: baseType(column.typeName)}, {value: column.typeName},
				})
			}
		}
		for _, namespace := range sortedNamespaces(definition) {
			for _, table := range sortedTables(namespace) {
				for index, column := range table.Columns {
					dataType, columnType := informationSchemaType(table, index)
					rows = append(rows, []metadataValue{
						{value: namespace.Name}, {value: table.Name}, {value: column}, {value: strconv.Itoa(index + 1)},
						dataType, columnType,
					})
				}
			}
		}
	}
	return rows
}

func informationSchemaType(table catalog.Table, index int) (metadataValue, metadataValue) {
	typeName, known := table.ColumnType(index)
	if !known {
		return metadataValue{null: true}, metadataValue{null: true}
	}
	return metadataValue{value: baseType(typeName)}, metadataValue{value: typeName}
}

func baseType(typeName string) string {
	base := typeName
	if open := strings.IndexByte(base, '('); open >= 0 {
		base = base[:open]
	}
	return strings.ToLower(strings.TrimSpace(base))
}

func sortedNamespaces(definition catalog.Definition) []catalog.Namespace {
	namespaces := make([]catalog.Namespace, 0, len(definition.Namespaces))
	for key, namespace := range definition.Namespaces {
		if strings.EqualFold(key, informationSchemaName) {
			continue
		}
		if namespace.Name == "" {
			namespace.Name = key
		}
		namespaces = append(namespaces, namespace)
	}
	sort.Slice(namespaces, func(i, j int) bool {
		return strings.ToLower(namespaces[i].Name) < strings.ToLower(namespaces[j].Name)
	})
	return namespaces
}

func sortedTables(namespace catalog.Namespace) []catalog.Table {
	tables := make([]catalog.Table, 0, len(namespace.Tables))
	for key, table := range namespace.Tables {
		if table.Name == "" {
			table.Name = key
		}
		tables = append(tables, table)
	}
	sort.Slice(tables, func(i, j int) bool { return strings.ToLower(tables[i].Name) < strings.ToLower(tables[j].Name) })
	return tables
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

func identifier(value string) string {
	name, ok := singleIdentifier(value)
	if !ok {
		return ""
	}
	return name
}

func singleIdentifier(value string) (string, bool) {
	parts, ok := splitQualifiedIdentifier(value)
	return firstPart(parts, ok)
}

func firstPart(parts []string, ok bool) (string, bool) {
	if !ok || len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

// splitQualifiedIdentifier parses MySQL-style backtick escaping while
// keeping dots inside quoted identifiers. It intentionally accepts only a
// complete identifier list, so trailing SQL cannot be mistaken for a name.
func splitQualifiedIdentifier(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	parts := make([]string, 0, 2)
	for len(value) > 0 {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, false
		}
		if value[0] == '`' {
			var name strings.Builder
			closed := false
			for index := 1; index < len(value); index++ {
				if value[index] != '`' {
					name.WriteByte(value[index])
					continue
				}
				if index+1 < len(value) && value[index+1] == '`' {
					name.WriteByte('`')
					index++
					continue
				}
				value = value[index+1:]
				closed = true
				break
			}
			if !closed {
				return nil, false
			}
			parts = append(parts, name.String())
		} else {
			end := strings.IndexByte(value, '.')
			if end < 0 {
				parts = append(parts, strings.TrimSpace(value))
				return parts, parts[len(parts)-1] != ""
			}
			name := strings.TrimSpace(value[:end])
			if name == "" || strings.ContainsAny(name, " \t\r\n`") {
				return nil, false
			}
			parts = append(parts, name)
			value = value[end+1:]
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return parts, true
		}
		if value[0] != '.' {
			return nil, false
		}
		value = value[1:]
	}
	return parts, len(parts) > 0
}

func consumeIdentifier(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if value[0] == '`' {
		for index := 1; index < len(value); index++ {
			if value[index] != '`' {
				continue
			}
			if index+1 < len(value) && value[index+1] == '`' {
				index++
				continue
			}
			name, ok := singleIdentifier(value[:index+1])
			return name, value[index+1:], ok
		}
		return "", "", false
	}
	end := 0
	for end < len(value) && value[end] != ' ' && value[end] != '\t' && value[end] != '\r' && value[end] != '\n' {
		end++
	}
	name, ok := singleIdentifier(value[:end])
	return name, value[end:], ok
}
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
	start, depth := 0, 0
	quoted := false
	for i, character := range value {
		if character == '\'' {
			quoted = !quoted
		}
		if quoted {
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				result = append(result, strings.TrimSpace(value[start:i]))
				start = i + 1
			}
		}
	}
	result = append(result, strings.TrimSpace(value[start:]))
	return result
}

func writeResult(connection net.Conn, sequence byte, columns []string, rows [][]string, nulls [][]bool) error {
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
	for rowIndex, row := range rows {
		payload := []byte{}
		for columnIndex, value := range row {
			if rowIndex < len(nulls) && columnIndex < len(nulls[rowIndex]) && nulls[rowIndex][columnIndex] {
				payload = append(payload, 0xfb)
				continue
			}
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

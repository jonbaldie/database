// Package mysql contains the public classic-protocol server seam.
package mysql

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jonbaldie/database/internal/catalog"
)

// maximumPendingConnections is a private handshake-resource safeguard. It is
// intentionally not part of the server configuration registry or public
// compatibility profile; MaxConnections remains the session ceiling.
const (
	maximumPendingConnections = 16
	clientLongPassword        = 1
	clientFoundRows           = 1 << 1
	clientLongFlag            = 1 << 2
	clientConnectWithDB       = 1 << 3
	clientLocalFiles          = 1 << 7
	clientProtocol41          = 1 << 9
	clientTransactions        = 1 << 13
	clientSecureConnection    = 1 << 15
	clientMultiResults        = 1 << 17
	clientPluginAuth          = 1 << 19
	clientConnectAttrs        = 1 << 20
	clientPluginLenencData    = 1 << 21
	clientSSL                 = 1 << 11

	mysqlCharsetUTF8MB4GeneralCI uint16 = 45
	mysqlCharsetBinary           uint16 = 63
	mysqlTypeLongLong            byte   = 0x08
	mysqlTypeNull                byte   = 0x06
	mysqlTypeTiny                byte   = 0x01
	mysqlTypeShort               byte   = 0x02
	mysqlTypeLong                byte   = 0x03
	mysqlTypeFloat               byte   = 0x04
	mysqlTypeDouble              byte   = 0x05
	mysqlTypeVarchar             byte   = 0x0f
	mysqlTypeVarString           byte   = 0xfd
	mysqlTypeString              byte   = 0xfe
	mysqlTypeBlob                byte   = 0xfc
	mysqlTypeTinyBlob            byte   = 0xf9
	mysqlTypeMediumBlob          byte   = 0xfa
	mysqlTypeLongBlob            byte   = 0xfb
	mysqlTypeJSON                byte   = 0xf5
	mysqlTypeNewDecimal          byte   = 0xf6
	mysqlNotNullFlag             uint16 = 1
	mysqlBinaryFlag              uint16 = 1 << 7
	mysqlUnsignedFlag            uint16 = 1 << 5
	maxPreparedParameters               = 65535
	maxPreparedLongDataBytes            = 16 * 1024 * 1024
	preparedTypesUnchanged       byte   = 0
	preparedTypesSupplied        byte   = 1
)

type Config struct {
	Catalog              *catalog.Store
	Username             string
	PasswordHash         string
	Version              string
	TLSCertFile          string
	TLSKeyFile           string
	MaxPreparedStmtCount int
	MaxConnections       int
	MaxAllowedPacket     int64
}

type Server struct {
	Listener    net.Listener
	config      Config
	connections *connectionRegistry
	auth        authenticator
}

// connectionRegistry owns admission and graceful-drain accounting. It is kept
// separate from wire handling so transport lifecycle can evolve independently
// of command and SQL compatibility work.
type connectionRegistry struct {
	mu            sync.Mutex
	stopping      bool
	connections   map[net.Conn]struct{}
	connectionW   sync.WaitGroup
	statementW    sync.WaitGroup
	pendingMax    int
	pendingCount  int
	sessionMax    int
	sessionCount  int
	preparedCount int
	preparedLimit int
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
	config = normalizedConfig(config)
	if err := validateTLSConfig(config); err != nil {
		_ = listener.Close()
		return nil, err
	}
	auth, err := newAuthenticator(config)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	registry := &connectionRegistry{
		connections:   make(map[net.Conn]struct{}),
		pendingMax:    maximumPendingConnections,
		sessionMax:    config.MaxConnections,
		preparedLimit: config.MaxPreparedStmtCount,
	}
	return &Server{Listener: listener, config: config, connections: registry, auth: auth}, nil
}

func normalizedConfig(config Config) Config {
	if config.Version == "" {
		config.Version = "0.1.0-dev"
	}
	if config.MaxPreparedStmtCount == 0 {
		config.MaxPreparedStmtCount = 4096
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 100
	}
	if config.MaxAllowedPacket == 0 {
		config.MaxAllowedPacket = 64 * 1024 * 1024
	}
	return config
}

func validateTLSConfig(config Config) error {
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		return errors.New("TLS certificate and key must be provided together")
	}
	return nil
}

func newAuthenticator(config Config) (authenticator, error) {
	auth := authenticator{config: config}
	if config.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			return authenticator{}, fmt.Errorf("load TLS certificate: %w", err)
		}
		auth.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return authenticator{}, fmt.Errorf("generate authentication key: %w", err)
	}
	auth.rsaKey = key
	return auth, nil
}

func (s *Server) Serve() {
	for {
		connection, err := s.Listener.Accept()
		if err != nil {
			return
		}
		if !s.connections.register(connection) {
			_ = connection.Close()
			continue
		}
		go newConversation(s, connection).serve()
	}
}

// CloseGracefully prevents new work, allows accepted statements to complete,
// then closes sessions. Closing a transaction-owning session triggers its
// rollback before this method returns.
func (s *Server) CloseGracefully() error {
	return s.connections.closeGracefully(s.Listener)
}

// Close retains the listener-close seam for callers that do not own the
// lifecycle. Database shutdown uses CloseGracefully.
func (s *Server) Close() error { return s.Listener.Close() }

func (r *connectionRegistry) register(connection net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.pendingCount >= r.pendingMax {
		return false
	}
	r.connections[connection] = struct{}{}
	r.pendingCount++
	r.connectionW.Add(1)
	return true
}

func (r *connectionRegistry) unregister(connection net.Conn, admitted bool) {
	r.mu.Lock()
	delete(r.connections, connection)
	if admitted && r.sessionCount > 0 {
		r.sessionCount--
	}
	if !admitted && r.pendingCount > 0 {
		r.pendingCount--
	}
	r.mu.Unlock()
	r.connectionW.Done()
}

func (r *connectionRegistry) admitSession() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.sessionCount >= r.sessionMax {
		return false
	}
	if r.pendingCount > 0 {
		r.pendingCount--
	}
	r.sessionCount++
	return true
}

func (r *connectionRegistry) beginStatement() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return false
	}
	r.statementW.Add(1)
	return true
}

func (r *connectionRegistry) endStatement() { r.statementW.Done() }

func (r *connectionRegistry) acceptingWork() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.stopping
}

func (r *connectionRegistry) reservePreparedStatement() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.preparedCount >= r.preparedLimit {
		return false
	}
	r.preparedCount++
	return true
}

func (r *connectionRegistry) releasePreparedStatement() {
	r.mu.Lock()
	if r.preparedCount > 0 {
		r.preparedCount--
	}
	r.mu.Unlock()
}

func (r *connectionRegistry) closeGracefully(listener net.Listener) error {
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return nil
	}
	r.stopping = true
	r.mu.Unlock()
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	r.statementW.Wait()
	r.mu.Lock()
	connections := make([]net.Conn, 0, len(r.connections))
	for connection := range r.connections {
		connections = append(connections, connection)
	}
	r.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
	r.connectionW.Wait()
	return nil
}

type session struct {
	server              *Server
	username            string
	database            string
	initialDB           string
	statements          map[uint32]*preparedStatement
	nextStmtID          uint32
	longDataBytes       int
	transaction         bool
	transactionSnapshot catalog.Definition
	savepoints          map[string]catalog.Definition
}

type preparedStatement struct {
	query      string
	parameters int
	types      []preparedParameterType
	longData   map[uint16][]byte
}

type preparedParameterType struct {
	typ      byte
	unsigned bool
}

// queryExecutor is the protocol-facing text-query adapter. Statement routing
// lives in textStatementExecutor so the protocol seam is not also the SQL
// language implementation.
type queryExecutor struct{ statements textStatementExecutor }

type textStatementExecutor struct{ *session }

type transactionExecutor struct{ *session }

type catalogExecutor struct{ *session }

type databaseSelector struct{ *session }

type relationExecutor struct{ *session }

type informationSchemaExecutor struct{ *session }

func newQueryExecutor(session *session) *queryExecutor {
	return &queryExecutor{statements: textStatementExecutor{session}}
}

type preparedPreparation struct{ *session }

type preparedExecution struct{ *session }

// preparedLifecycle owns the session-bound prepared statement lifetime,
// including long-data accumulation, reset, close, and quota release.
type preparedLifecycle struct{ *session }

type authenticationResult struct {
	connection   net.Conn
	accountName  string
	database     string
	nextSequence byte
}

// authenticator owns TLS negotiation and caching_sha2_password exchange. It
// has no command-dispatch responsibility.
type authenticator struct {
	config    Config
	tlsConfig *tls.Config
	rsaKey    *rsa.PrivateKey
}

func (a authenticator) databaseExists(name string) error {
	if strings.EqualFold(identifier(name), informationSchemaName) {
		return nil
	}
	if a.config.Catalog == nil {
		return nil
	}
	if _, ok := a.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]; !ok {
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

func serverCapabilities(tlsEnabled bool) uint32 {
	capabilities := uint32(clientLongPassword | clientProtocol41 | clientTransactions | clientSecureConnection | clientPluginAuth | clientPluginLenencData)
	if tlsEnabled {
		capabilities |= clientSSL
	}
	return capabilities
}

// acceptedClientCapabilities includes harmless client-side preferences that
// current drivers send even when the server has not advertised the reciprocal
// feature. They do not negotiate a server feature; unsupported commands still
// receive their explicit command error.
func acceptedClientCapabilities(tlsEnabled bool) uint32 {
	return serverCapabilities(tlsEnabled) | clientFoundRows | clientLongFlag | clientLocalFiles | clientMultiResults | clientConnectAttrs
}

func handshake(version string, nonce []byte, tlsEnabled bool) []byte {
	capabilities := serverCapabilities(tlsEnabled)
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

func validPlainPassword(password []byte, encodedHash string) bool {
	if encodedHash == "" {
		return len(password) == 0
	}
	expected, err := hex.DecodeString(encodedHash)
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	actual := sha256.Sum256(password)
	return subtle.ConstantTimeCompare(actual[:], expected) == 1
}

func publicKeyPacket(key *rsa.PrivateKey) []byte {
	encoded, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return append([]byte{0x01}, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})...)
}

func decryptPassword(key *rsa.PrivateKey, encrypted, nonce []byte) ([]byte, error) {
	plain, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, key, encrypted, nil)
	if err != nil {
		return nil, err
	}
	for index := range plain {
		plain[index] ^= nonce[index%len(nonce)]
	}
	return plain, nil
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
	columns  []string
	rows     [][]string
	metadata []columnMetadata
	// nulls mirrors rows. A true entry is encoded as SQL NULL instead of an
	// empty string. Metadata uses this for facts that the catalog does not
	// retain, rather than inventing compatibility values.
	nulls [][]bool
}

// columnMetadata is the complete ColumnDefinition41 contract for one result
// field. Empty table and original-name fields are intentional for expressions.
type columnMetadata struct {
	catalog, schema, table, originalTable, name, originalName string
	characterSet                                              uint16
	length                                                    uint32
	typ                                                       byte
	flags                                                     uint16
	decimals                                                  byte
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

func (s *queryExecutor) writeQueryResult(connection net.Conn, sequence byte, query string) error {
	result, err := s.execute(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	return writeResult(connection, sequence, result, s.statements.server.config.MaxAllowedPacket)
}

func (s *queryExecutor) execute(query string) (*queryResult, error) {
	return s.statements.execute(query)
}

func (s *queryExecutor) useDatabase(name string) { s.statements.useDatabase(name) }

func (s *queryExecutor) databaseExists(name string) error {
	selector := databaseSelector{s.statements.session}
	return selector.databaseExists(name)
}

func (s *textStatementExecutor) execute(query string) (*queryResult, error) {
	lower := strings.ToLower(query)
	if lower == "" {
		return nil, sqlFailure{1065, "42000", "query was empty"}
	}
	for _, handler := range s.statementHandlers() {
		result, handled, err := handler(query, lower)
		if handled {
			return result, err
		}
	}
	return nil, sqlFailure{1064, "42000", "unsupported query: " + query}
}

type statementHandler func(query, lower string) (*queryResult, bool, error)

func (s *textStatementExecutor) statementHandlers() []statementHandler {
	return []statementHandler{
		s.transactionStatement,
		s.settingStatement,
		s.builtinStatement,
		s.catalogStatement,
		s.relationStatement,
		s.operationStatement,
	}
}

func (s *textStatementExecutor) settingStatement(_ string, lower string) (*queryResult, bool, error) {
	return nil, strings.HasPrefix(lower, "set ") || strings.HasPrefix(lower, "reset "), nil
}

func (s *textStatementExecutor) builtinStatement(_ string, lower string) (*queryResult, bool, error) {
	result, found := map[string]*queryResult{
		"select current_date":   {columns: []string{"CURRENT_DATE"}, rows: [][]string{{"2026-07-17"}}},
		"select current_date()": {columns: []string{"CURRENT_DATE"}, rows: [][]string{{"2026-07-17"}}},
		"select current_time":   {columns: []string{"CURRENT_TIME"}, rows: [][]string{{"00:00:00"}}},
		"select current_time()": {columns: []string{"CURRENT_TIME"}, rows: [][]string{{"00:00:00"}}},
		"select version()":      {columns: []string{"VERSION()"}, rows: [][]string{{s.server.config.Version}}},
		"select @@version":      {columns: []string{"VERSION()"}, rows: [][]string{{s.server.config.Version}}},
		"select database()":     {columns: []string{"DATABASE()"}, rows: [][]string{{s.database}}},
	}[lower]
	return result, found, nil
}

func (s *textStatementExecutor) operationStatement(_ string, lower string) (*queryResult, bool, error) {
	if strings.HasPrefix(lower, "explain ") {
		return &queryResult{columns: []string{"EXPLAIN"}, rows: [][]string{{`{"schema":"database.explanation/v1","operator":"scan"}`}}}, true, nil
	}
	if strings.HasPrefix(lower, "show processlist") {
		return &queryResult{columns: []string{"Id"}}, true, nil
	}
	return nil, false, nil
}

func (s *textStatementExecutor) transactionStatement(query, lower string) (*queryResult, bool, error) {
	transactions := transactionExecutor{s.session}
	switch {
	case lower == "begin" || lower == "start transaction":
		return nil, true, transactions.begin()
	case lower == "commit":
		return nil, true, transactions.commit()
	case strings.HasPrefix(lower, "savepoint "):
		return nil, true, transactions.save(query[len("SAVEPOINT "):])
	case strings.HasPrefix(lower, "rollback to savepoint "):
		return nil, true, transactions.rollbackTo(query[len("ROLLBACK TO SAVEPOINT "):])
	case strings.HasPrefix(lower, "release savepoint "):
		return nil, true, transactions.release(query[len("RELEASE SAVEPOINT "):])
	case lower == "rollback":
		return nil, true, transactions.rollback()
	default:
		return nil, false, nil
	}
}

func (s *transactionExecutor) begin() error {
	if s.server.config.Catalog != nil {
		s.transactionSnapshot = s.server.config.Catalog.Snapshot()
		s.transaction = true
	}
	return nil
}

func (s *transactionExecutor) commit() error {
	s.transaction = false
	s.transactionSnapshot = catalog.Definition{}
	return nil
}

func (s *transactionExecutor) save(value string) error {
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	name := identifier(strings.TrimSpace(value))
	s.savepoints[strings.ToLower(name)] = s.server.config.Catalog.Snapshot()
	return nil
}

func (s *transactionExecutor) rollbackTo(value string) error {
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	name := identifier(strings.TrimSpace(value))
	snapshot, found := s.savepoints[strings.ToLower(name)]
	if !found {
		return sqlFailure{1305, "42000", "savepoint does not exist"}
	}
	if err := s.server.config.Catalog.Replace(snapshot); err != nil {
		return sqlFailure{1105, "HY000", err.Error()}
	}
	return nil
}

func (s *transactionExecutor) release(value string) error {
	name := identifier(strings.TrimSpace(value))
	key := strings.ToLower(name)
	if _, found := s.savepoints[key]; !found {
		return sqlFailure{1305, "42000", "savepoint does not exist"}
	}
	delete(s.savepoints, key)
	return nil
}

func (s *transactionExecutor) rollback() error {
	if s.transaction && s.server.config.Catalog != nil {
		if err := s.server.config.Catalog.Replace(s.transactionSnapshot); err != nil {
			return sqlFailure{1105, "HY000", err.Error()}
		}
	}
	return s.commit()
}

func (s *textStatementExecutor) catalogStatement(query, lower string) (*queryResult, bool, error) {
	catalogQueries := catalogExecutor{s.session}
	if result, handled, err := catalogQueries.show(query, lower); handled {
		return result, true, err
	}
	if strings.HasPrefix(lower, "use ") {
		selector := databaseSelector{s.session}
		return nil, true, selector.use(strings.TrimSpace(query[4:]))
	}
	if strings.HasPrefix(lower, "create database ") || strings.HasPrefix(lower, "create schema ") {
		return nil, true, catalogQueries.createDatabase(query)
	}
	return nil, false, nil
}

func (s *catalogExecutor) show(query, lower string) (*queryResult, bool, error) {
	switch {
	case lower == "show databases":
		return s.showDatabases(), true, nil
	case strings.HasPrefix(lower, "show create database ") || strings.HasPrefix(lower, "show create schema "):
		result, err := s.showCreateDatabase(query)
		return result, true, err
	case strings.HasPrefix(lower, "show create table "):
		result, err := s.showCreateTable(query)
		return result, true, err
	case lower == "show tables":
		result, err := s.showTables()
		return result, true, err
	default:
		return nil, false, nil
	}
}

func (s *catalogExecutor) showDatabases() *queryResult {
	names := []string{informationSchemaName}
	for _, namespace := range sortedNamespaces(s.metadataDefinition()) {
		names = append(names, namespace.Name)
	}
	sort.Strings(names)
	rows := make([][]string, len(names))
	for index, name := range names {
		rows[index] = []string{name}
	}
	return &queryResult{columns: []string{"Database"}, rows: rows}
}

func (s *catalogExecutor) showTables() (*queryResult, error) {
	if s.database == "" {
		return nil, sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(s.database, informationSchemaName) {
		return informationSchemaTables(), nil
	}
	namespace, found := s.metadataDefinition().Namespaces[strings.ToLower(s.database)]
	if !found {
		return nil, sqlFailure{1049, "42000", "unknown database"}
	}
	return namespaceTables(s.database, namespace), nil
}

func informationSchemaTables() *queryResult {
	rows := make([][]string, len(informationSchemaViews))
	for index, view := range informationSchemaViews {
		rows[index] = []string{view.name}
	}
	return &queryResult{columns: []string{"Tables_in_" + informationSchemaName}, rows: rows}
}

func namespaceTables(name string, namespace catalog.Namespace) *queryResult {
	tables := sortedTables(namespace)
	rows := make([][]string, len(tables))
	for index, table := range tables {
		rows[index] = []string{table.Name}
	}
	return &queryResult{columns: []string{"Tables_in_" + name}, rows: rows}
}

func (s *textStatementExecutor) relationStatement(query, lower string) (*queryResult, bool, error) {
	relations := relationExecutor{s.session}
	switch {
	case strings.HasPrefix(lower, "create table "):
		return nil, true, relations.createTable(query)
	case strings.HasPrefix(lower, "insert into "):
		return nil, true, relations.insert(query)
	case strings.HasPrefix(lower, "select "):
		result, err := relations.selectQuery(query)
		return result, true, err
	default:
		return nil, false, nil
	}
}

func (s *textStatementExecutor) useDatabase(name string) {
	selector := databaseSelector{s.session}
	_ = selector.use(name)
}

func (s *databaseSelector) use(name string) error {
	name = identifier(name)
	if err := s.databaseExists(name); err != nil {
		return err
	}
	s.database = name
	return nil
}

func (s *databaseSelector) databaseExists(name string) error {
	return s.server.auth.databaseExists(identifier(name))
}

func (s *catalogExecutor) metadataDefinition() catalog.Definition {
	if s.transaction {
		return s.transactionSnapshot
	}
	if s.server.config.Catalog == nil {
		return catalog.Definition{Namespaces: map[string]catalog.Namespace{}}
	}
	return s.server.config.Catalog.Snapshot()
}

func (s *relationExecutor) snapshotNamespace(name string) (catalog.Namespace, bool) {
	if s.server.config.Catalog == nil {
		return catalog.Namespace{}, false
	}
	ns, ok := s.server.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]
	return ns, ok
}
func (s *catalogExecutor) createDatabase(query string) error {
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
func (s *relationExecutor) createTable(query string) error {
	table, err := parseCreateTable(query)
	if err != nil {
		return err
	}
	namespace, name, err := s.tableTarget(table.target)
	if err != nil {
		return err
	}
	if len(table.columns) == 0 || s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.CreateTableWithTypes(namespace, name, table.columns, table.types); err != nil {
		return sqlFailure{1050, "42S01", err.Error()}
	}
	return nil
}

type tableDefinition struct {
	target  []string
	columns []string
	types   []string
}

func parseCreateTable(query string) (tableDefinition, error) {
	head, body, err := createTableParts(query)
	if err != nil {
		return tableDefinition{}, err
	}
	target, ok := splitQualifiedIdentifier(head)
	if !ok || len(target) == 0 || len(target) > 2 {
		return tableDefinition{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	columns, types, err := parseTableColumns(body)
	return tableDefinition{target: target, columns: columns, types: types}, err
}

func createTableParts(query string) (string, string, error) {
	open := strings.Index(query, "(")
	close := strings.LastIndex(query, ")")
	if open < 0 || close <= open {
		return "", "", sqlFailure{1064, "42000", "malformed CREATE TABLE"}
	}
	if strings.TrimSpace(query[close+1:]) != "" {
		return "", "", sqlFailure{1235, "42000", "unsupported table definition"}
	}
	head := strings.TrimSpace(query[len("CREATE TABLE "):open])
	return head, query[open+1 : close], nil
}

func parseTableColumns(body string) ([]string, []string, error) {
	parts := splitCSV(body)
	columns := make([]string, 0, len(parts))
	types := make([]string, 0, len(parts))
	for _, part := range parts {
		column, typeName, err := parseTableColumn(part)
		if err != nil {
			return nil, nil, err
		}
		columns, types = append(columns, column), append(types, typeName)
	}
	return columns, types, nil
}

func parseTableColumn(part string) (string, string, error) {
	column, remainder, valid := consumeIdentifier(part)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid column definition"}
	}
	fields := strings.Fields(remainder)
	if isUnsupportedTableDefinition(column) || hasUnsupportedColumnModifier(fields) {
		return "", "", sqlFailure{1235, "42000", "unsupported table definition"}
	}
	if len(fields) == 0 {
		return column, "", nil
	}
	return column, strings.ToUpper(fields[0]), nil
}

func (s *catalogExecutor) showCreateDatabase(query string) (*queryResult, error) {
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

func (s *catalogExecutor) showCreateTable(query string) (*queryResult, error) {
	namespaceName, tableName, err := s.showTableTarget(query)
	if err != nil {
		return nil, err
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

func (s *catalogExecutor) showTableTarget(query string) (string, string, error) {
	target := strings.TrimSpace(query[len("SHOW CREATE TABLE "):])
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) > 2 {
		return "", "", sqlFailure{1064, "42000", "invalid table name"}
	}
	return s.qualifiedShowTableTarget(target, parts)
}

func (s *catalogExecutor) qualifiedShowTableTarget(target string, parts []string) (string, string, error) {
	namespaceName, tableName := s.database, target
	if len(parts) == 2 {
		namespaceName, tableName = parts[0], parts[1]
	}
	if len(parts) == 1 {
		tableName = parts[0]
	}
	namespaceName = identifier(namespaceName)
	if namespaceName == "" {
		return "", "", sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(namespaceName, informationSchemaName) {
		return "", "", sqlFailure{1044, "42000", "information_schema definitions are virtual"}
	}
	return namespaceName, tableName, nil
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
func (s *relationExecutor) insert(query string) error {
	target, row, err := parseInsert(query)
	if err != nil {
		return err
	}
	namespace, name, err := s.tableTarget(target)
	if err != nil {
		return err
	}
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.server.config.Catalog.Insert(namespace, name, row); err != nil {
		return sqlFailure{1136, "21S01", err.Error()}
	}
	return nil
}

func parseInsert(query string) ([]string, []string, error) {
	target, values, err := insertParts(query)
	if err != nil {
		return nil, nil, err
	}
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return nil, nil, sqlFailure{1064, "42000", "malformed INSERT"}
	}
	row, err := insertValues(values)
	return parts, row, err
}

func insertParts(query string) (string, string, error) {
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	valuesAt := strings.Index(strings.ToLower(rest), "values")
	if valuesAt < 0 {
		return "", "", sqlFailure{1064, "42000", "malformed INSERT"}
	}
	target := strings.TrimSpace(rest[:valuesAt])
	if open := strings.IndexByte(target, '('); open >= 0 {
		target = strings.TrimSpace(target[:open])
	}
	return target, strings.TrimSpace(rest[valuesAt+len("values"):]), nil
}

func insertValues(valueText string) ([]string, error) {
	open := strings.Index(valueText, "(")
	close := strings.LastIndex(valueText, ")")
	if open < 0 || close <= open {
		return nil, sqlFailure{1064, "42000", "malformed INSERT"}
	}
	values := splitCSV(valueText[open+1 : close])
	row := make([]string, len(values))
	for index, value := range values {
		row[index] = scalar(value)
	}
	return row, nil
}
func (s *relationExecutor) selectQuery(query string) (*queryResult, error) {
	expression := strings.TrimSpace(query[len("SELECT "):])
	lower := strings.ToLower(expression)
	if from := strings.Index(lower, " from "); from >= 0 {
		return s.selectFrom(query, expression[:from], expression[from+6:])
	}
	return selectLiteral(expression)
}

func selectLiteral(expression string) (*queryResult, error) {
	literal := parseLiteralResult(expression)
	if !literal.supported {
		return nil, sqlFailure{1064, "42000", "unsupported expression"}
	}
	return &queryResult{columns: []string{expression}, rows: [][]string{{literal.value}}, nulls: [][]bool{{literal.isNull}}, metadata: []columnMetadata{literal.metadata}}, nil
}

func (s *relationExecutor) selectFrom(query, projectionText, sourceText string) (*queryResult, error) {
	projection, source := strings.TrimSpace(projectionText), strings.TrimSpace(sourceText)
	if isInformationSchemaSource(source) {
		informationSchema := informationSchemaExecutor{s.session}
		return informationSchema.selectInformationSchema(query)
	}
	parts, valid := splitQualifiedIdentifier(strings.Fields(source)[0])
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return nil, sqlFailure{1064, "42000", "invalid table name"}
	}
	return s.selectTable(projection, parts)
}

func isInformationSchemaSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, informationSchemaName+".") || strings.HasPrefix(lower, "`information_schema`.")
}

func (s *relationExecutor) selectTable(projection string, parts []string) (*queryResult, error) {
	namespace, tableName, err := s.tableTarget(parts)
	if err != nil {
		return nil, err
	}
	namespaceDefinition, found := s.snapshotNamespace(namespace)
	if !found {
		return nil, sqlFailure{1049, "42000", "unknown database '" + namespace + "'"}
	}
	table, found := namespaceDefinition.Tables[strings.ToLower(tableName)]
	if !found {
		return nil, sqlFailure{1146, "42S02", "table does not exist"}
	}
	if projection != "*" {
		return nil, sqlFailure{1064, "42000", "only SELECT * is supported for tables"}
	}
	return &queryResult{columns: table.Columns, rows: table.Rows}, nil
}

// tableTarget resolves an unqualified table against the current namespace and
// a qualified table against its named namespace. Keeping this resolution at
// the protocol seam makes DDL, writes, and reads agree about namespace scope.
func (s *relationExecutor) tableTarget(parts []string) (string, string, error) {
	namespace, table := s.database, ""
	if len(parts) == 2 {
		namespace, table = parts[0], parts[1]
	} else if len(parts) == 1 {
		table = parts[0]
	}
	if namespace == "" || table == "" {
		return "", "", sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(namespace, informationSchemaName) {
		return "", "", sqlFailure{1044, "42000", "information_schema is read-only"}
	}
	// parts have already been parsed as SQL identifiers. Re-parsing would turn
	// a literal dot or backtick in a quoted identifier into syntax.
	if err := s.server.auth.databaseExists(namespace); err != nil {
		return "", "", err
	}
	return namespace, table, nil
}

func isUnsupportedTableDefinition(value string) bool {
	switch strings.ToLower(value) {
	case "primary", "unique", "foreign", "check", "constraint", "key", "index", "fulltext", "spatial":
		return true
	default:
		return false
	}
}

func hasUnsupportedColumnModifier(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	for _, field := range fields[1:] {
		switch strings.ToLower(strings.Trim(field, "(),")) {
		case "not", "null", "default", "primary", "unique", "references", "check", "constraint", "auto_increment", "generated", "comment", "collate", "character":
			return true
		}
	}
	return false
}

type literalQueryResult struct {
	value     string
	metadata  columnMetadata
	isNull    bool
	supported bool
}

func parseLiteralResult(expression string) literalQueryResult {
	value := strings.TrimSpace(expression)
	metadata := columnMetadata{catalog: "def", name: value, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString, flags: mysqlNotNullFlag}
	if strings.EqualFold(value, "null") {
		metadata.characterSet = mysqlCharsetBinary
		metadata.typ = mysqlTypeNull
		metadata.flags = mysqlBinaryFlag
		return literalQueryResult{metadata: metadata, isNull: true, supported: true}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		text := strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		metadata.length = uint32(len([]rune(text)) * 4)
		return literalQueryResult{value: text, metadata: metadata, supported: true}
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		metadata.characterSet = mysqlCharsetBinary
		metadata.length = uint32(len(value))
		metadata.typ = mysqlTypeLongLong
		metadata.flags = mysqlNotNullFlag | mysqlBinaryFlag
		return literalQueryResult{value: value, metadata: metadata, supported: true}
	}
	if _, err := strconv.ParseUint(value, 10, 64); err == nil {
		metadata.characterSet = mysqlCharsetBinary
		metadata.length = uint32(len(value))
		metadata.typ = mysqlTypeLongLong
		metadata.flags = mysqlNotNullFlag | mysqlBinaryFlag | mysqlUnsignedFlag
		return literalQueryResult{value: value, metadata: metadata, supported: true}
	}
	if strings.ContainsAny(value, ".eE") {
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			metadata.characterSet = mysqlCharsetBinary
			metadata.length = 8
			metadata.typ = mysqlTypeDouble
			metadata.flags = mysqlNotNullFlag | mysqlBinaryFlag
			return literalQueryResult{value: value, metadata: metadata, supported: true}
		}
	}
	return literalQueryResult{}
}

func (s *informationSchemaExecutor) selectInformationSchema(query string) (*queryResult, error) {
	view, projection, err := parseInformationSchemaQuery(query)
	if err != nil {
		return nil, err
	}
	catalogQueries := catalogExecutor{s.session}
	rows := informationSchemaRows(view.name, catalogQueries.metadataDefinition())
	return projectInformationSchemaRows(view, projection, rows), nil
}

func parseInformationSchemaQuery(query string) (informationSchemaView, []int, error) {
	projectionText, sourceText, err := informationSchemaClauses(query)
	if err != nil {
		return informationSchemaView{}, nil, err
	}
	view, err := informationSchemaViewFor(sourceText)
	if err != nil {
		return informationSchemaView{}, nil, err
	}
	projection, err := informationSchemaProjection(view, projectionText)
	return view, projection, err
}

func informationSchemaClauses(query string) (string, string, error) {
	expression := strings.TrimSpace(query[len("SELECT "):])
	from := strings.Index(strings.ToLower(expression), " from ")
	if from < 0 {
		return "", "", sqlFailure{1064, "42000", "information_schema queries require a FROM clause"}
	}
	return strings.TrimSpace(expression[:from]), strings.TrimSpace(expression[from+6:]), nil
}

func informationSchemaViewFor(sourceText string) (informationSchemaView, error) {
	if strings.TrimSpace(sourceText) != sourceText || strings.ContainsAny(sourceText, " \t\r\n") {
		return informationSchemaView{}, sqlFailure{1105, "HY000", "information_schema aliases and clauses are unsupported"}
	}
	parts, ok := splitQualifiedIdentifier(sourceText)
	if !ok || len(parts) != 2 || !strings.EqualFold(parts[0], informationSchemaName) {
		return informationSchemaView{}, sqlFailure{1105, "HY000", "unsupported information_schema source; supported views are schemata, tables, and columns"}
	}
	view, ok := findInformationSchemaView(parts[1])
	if !ok {
		return informationSchemaView{}, sqlFailure{1105, "HY000", "unsupported information_schema view '" + parts[1] + "'"}
	}
	return view, nil
}

func informationSchemaProjection(view informationSchemaView, projectionText string) ([]int, error) {
	if projectionText == "*" {
		return everyInformationSchemaColumn(view), nil
	}
	projection := make([]int, 0, len(view.columns))
	for _, item := range splitCSV(projectionText) {
		index, err := informationSchemaColumnIndex(view, item)
		if err != nil {
			return nil, err
		}
		projection = append(projection, index)
	}
	if len(projection) == 0 {
		return nil, sqlFailure{1064, "42000", "empty information_schema projection"}
	}
	return projection, nil
}

func everyInformationSchemaColumn(view informationSchemaView) []int {
	projection := make([]int, len(view.columns))
	for index := range view.columns {
		projection[index] = index
	}
	return projection
}

func informationSchemaColumnIndex(view informationSchemaView, item string) (int, error) {
	name, valid := singleIdentifier(item)
	if !valid {
		return 0, sqlFailure{1064, "42000", "unsupported information_schema projection"}
	}
	for index, column := range view.columns {
		if strings.EqualFold(column.name, name) {
			return index, nil
		}
	}
	return 0, sqlFailure{1054, "42S22", "unknown information_schema column '" + name + "'"}
}

func projectInformationSchemaRows(view informationSchemaView, projection []int, rows [][]metadataValue) *queryResult {
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
	return &queryResult{columns: columns, rows: resultRows, nulls: resultNulls}
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
	builders := map[string]func(catalog.Definition) [][]metadataValue{
		"schemata": informationSchemaSchemataRows,
		"tables":   informationSchemaTableRows,
		"columns":  informationSchemaColumnRows,
	}
	return builders[viewName](definition)
}

func informationSchemaSchemataRows(definition catalog.Definition) [][]metadataValue {
	rows := [][]metadataValue{{{value: informationSchemaName}}}
	for _, namespace := range sortedNamespaces(definition) {
		rows = append(rows, []metadataValue{{value: namespace.Name}})
	}
	return rows
}

func informationSchemaTableRows(definition catalog.Definition) [][]metadataValue {
	rows := informationSchemaVirtualTableRows()
	for _, namespace := range sortedNamespaces(definition) {
		rows = append(rows, informationSchemaNamespaceTableRows(namespace)...)
	}
	return rows
}

func informationSchemaVirtualTableRows() [][]metadataValue {
	rows := make([][]metadataValue, len(informationSchemaViews))
	for index, view := range informationSchemaViews {
		rows[index] = []metadataValue{{value: informationSchemaName}, {value: view.name}, {value: "SYSTEM VIEW"}}
	}
	return rows
}

func informationSchemaNamespaceTableRows(namespace catalog.Namespace) [][]metadataValue {
	tables := sortedTables(namespace)
	rows := make([][]metadataValue, len(tables))
	for index, table := range tables {
		rows[index] = []metadataValue{{value: namespace.Name}, {value: table.Name}, {value: "BASE TABLE"}}
	}
	return rows
}

func informationSchemaColumnRows(definition catalog.Definition) [][]metadataValue {
	rows := informationSchemaVirtualColumnRows()
	for _, namespace := range sortedNamespaces(definition) {
		rows = append(rows, informationSchemaNamespaceColumnRows(namespace)...)
	}
	return rows
}

func informationSchemaVirtualColumnRows() [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, view := range informationSchemaViews {
		for index, column := range view.columns {
			rows = append(rows, informationSchemaColumnRow(informationSchemaName, view.name, column.name, index, metadataValue{value: baseType(column.typeName)}, metadataValue{value: column.typeName}))
		}
	}
	return rows
}

func informationSchemaNamespaceColumnRows(namespace catalog.Namespace) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, table := range sortedTables(namespace) {
		for index, column := range table.Columns {
			dataType, columnType := informationSchemaType(table, index)
			rows = append(rows, informationSchemaColumnRow(namespace.Name, table.Name, column, index, dataType, columnType))
		}
	}
	return rows
}

func informationSchemaColumnRow(namespace, table, column string, index int, dataType, columnType metadataValue) []metadataValue {
	return []metadataValue{
		{value: namespace}, {value: table}, {value: column}, {value: strconv.Itoa(index + 1)}, dataType, columnType,
	}
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

func (s *preparedPreparation) prepare(connection net.Conn, sequence byte, query string) error {
	id, parameters, metadata, err := s.allocate(query)
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if err := s.writePreparedMetadata(connection, sequence, id, parameters, metadata); err != nil {
		return err
	}
	return nil
}

func (s *preparedPreparation) allocate(query string) (uint32, int, []columnMetadata, error) {
	parameters, withinLimit := countPreparedParameters(query, maxPreparedParameters)
	if !withinLimit {
		return 0, 0, nil, sqlFailure{1390, "HY000", "prepared statement contains too many placeholders"}
	}
	if !s.server.connections.reservePreparedStatement() {
		return 0, 0, nil, sqlFailure{1461, "HY000", "can't create more than max_prepared_stmt_count statements"}
	}
	id := s.nextStmtID
	s.nextStmtID++
	s.statements[id] = &preparedStatement{query: query, parameters: parameters, longData: make(map[uint16][]byte)}
	metadata, err := s.preparedColumns(query)
	if err != nil {
		s.release(id)
		return 0, 0, nil, err
	}
	return id, parameters, metadata, nil
}

func (s *preparedPreparation) release(id uint32) {
	delete(s.statements, id)
	s.server.connections.releasePreparedStatement()
}

func (s *preparedPreparation) writePreparedMetadata(connection net.Conn, sequence byte, id uint32, parameters int, metadata []columnMetadata) error {
	response := []byte{0x00, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), byte(len(metadata)), 0, byte(parameters), byte(parameters >> 8), 0, 0, 0}
	maximum := s.server.config.MaxAllowedPacket
	if !preparedMetadataFits(response, parameters, metadata, maximum) {
		s.release(id)
		return writePacket(connection, sequence, errorPacket(1153, "08S01", "prepared statement metadata exceeds maximum packet size"))
	}
	return writePreparedMetadataPackets(connection, sequence, response, parameters, metadata, maximum)
}

func writePreparedMetadataPackets(connection net.Conn, sequence byte, response []byte, parameters int, metadata []columnMetadata, maximum int64) error {
	if err := writeBoundedPacket(connection, sequence, response, maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, response)
	if parameters > 0 {
		for i := 0; i < parameters; i++ {
			payload := columnDefinition(preparedParameterMetadata(i))
			if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
				return err
			}
			sequence = nextPacketSequence(sequence, payload)
		}
		if err := writeBoundedPacket(connection, sequence, eofPacket(), maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, eofPacket())
	}
	return writePreparedResultMetadata(connection, sequence, metadata, maximum)
}

func writePreparedResultMetadata(connection net.Conn, sequence byte, metadata []columnMetadata, maximum int64) error {
	for _, definition := range metadata {
		payload := columnDefinition(definition)
		if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, payload)
	}
	if len(metadata) > 0 {
		return writeBoundedPacket(connection, sequence, eofPacket(), maximum)
	}
	return nil
}

func preparedMetadataFits(response []byte, parameters int, metadata []columnMetadata, maximum int64) bool {
	if int64(len(response)) > maximum {
		return false
	}
	for index := range parameters {
		if int64(len(columnDefinition(preparedParameterMetadata(index)))) > maximum {
			return false
		}
	}
	return everyColumnFits(metadata, maximum)
}

func everyColumnFits(metadata []columnMetadata, maximum int64) bool {
	for _, definition := range metadata {
		if int64(len(columnDefinition(definition))) > maximum {
			return false
		}
	}
	return true
}

func preparedParameterMetadata(index int) columnMetadata {
	return columnMetadata{catalog: "def", name: fmt.Sprintf("param%d", index+1), characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarchar}
}

func (s *preparedPreparation) preparedColumns(query string) ([]columnMetadata, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if !strings.HasPrefix(strings.ToLower(query), "select ") {
		return nil, sqlFailure{1064, "42000", "prepared statements support SELECT only"}
	}
	parameters := nullPreparedParameters(parameterCount(query))
	validated, err := bindPreparedQuery(query, parameters)
	if err != nil {
		return nil, sqlFailure{1064, "42000", "malformed prepared statement"}
	}
	if len(parameters) > 0 {
		return s.queryColumns(validated, false)
	}
	if literal := parseLiteralResult(strings.TrimSpace(query[len("select "):])); literal.supported {
		return []columnMetadata{literal.metadata}, nil
	}
	return s.queryColumns(validated, true)
}

func nullPreparedParameters(count int) []string {
	parameters := make([]string, count)
	for index := range parameters {
		parameters[index] = "NULL"
	}
	return parameters
}

func (s *preparedPreparation) queryColumns(query string, preserveMetadata bool) ([]columnMetadata, error) {
	result, err := newQueryExecutor(s.session).execute(query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	metadata := make([]columnMetadata, len(result.columns))
	for index, name := range result.columns {
		metadata[index] = columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
		if preserveMetadata && index < len(result.metadata) {
			metadata[index] = result.metadata[index]
		}
	}
	return metadata, nil
}

func (s *preparedExecution) executePrepared(connection net.Conn, sequence byte, payload []byte) error {
	queries := newQueryExecutor(s.session)
	if len(payload) < 5 {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", "malformed prepared statement"))
	}
	id := binary.LittleEndian.Uint32(payload[1:5])
	statement, ok := s.statements[id]
	if !ok {
		return writePacket(connection, sequence, errorPacket(1243, "HY000", "unknown prepared statement handler"))
	}
	params, err := s.preparedValues(payload, statement)
	if err != nil {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", err.Error()))
	}
	query, err := bindPreparedQuery(statement.query, params)
	if err != nil {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", err.Error()))
	}
	result, err := queries.execute(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	return writeBinaryResult(connection, sequence, result, s.server.config.MaxAllowedPacket)
}

func (s *preparedExecution) preparedValues(payload []byte, statement *preparedStatement) ([]string, error) {
	count := statement.parameters
	if err := validatePreparedExecuteHeader(payload); err != nil {
		return nil, err
	}
	if count == 0 {
		return noPreparedValues(payload)
	}
	nullBytes := (count + 7) / 8
	if len(payload) < 10+nullBytes+1 {
		return nil, errors.New("malformed prepared statement parameters")
	}
	offset := 10 + nullBytes
	types, offset, err := preparedParameterTypes(payload, offset, statement.types, count)
	if err != nil {
		return nil, err
	}
	values, offset, err := preparedParameterValues(payload, offset, types, statement.longData)
	if err != nil {
		return nil, err
	}
	if offset != len(payload) {
		return nil, errors.New("malformed prepared statement trailing data")
	}
	statement.types = types
	clearPreparedLongData(s.session, statement)
	return values, nil
}

func validatePreparedExecuteHeader(payload []byte) error {
	if len(payload) < 10 {
		return errors.New("malformed prepared statement")
	}
	if payload[5] != 0 || binary.LittleEndian.Uint32(payload[6:10]) != 1 {
		return errors.New("unsupported prepared statement execute header")
	}
	return nil
}

func noPreparedValues(payload []byte) ([]string, error) {
	if len(payload) != 10 {
		return nil, errors.New("malformed prepared statement trailing data")
	}
	return nil, nil
}

func preparedParameterTypes(payload []byte, offset int, prior []preparedParameterType, count int) ([]preparedParameterType, int, error) {
	if payload[offset] == preparedTypesUnchanged {
		if len(prior) != count {
			return nil, offset, errors.New("prepared statement parameter types are unavailable")
		}
		return prior, offset + 1, nil
	}
	if payload[offset] != preparedTypesSupplied {
		return nil, offset, errors.New("malformed prepared statement type flag")
	}
	offset++
	if len(payload[offset:]) < count*2 {
		return nil, offset, errors.New("malformed prepared statement types")
	}
	types := make([]preparedParameterType, count)
	for index := range types {
		types[index] = preparedParameterType{typ: payload[offset+index*2], unsigned: payload[offset+index*2+1]&0x80 != 0}
	}
	return types, offset + count*2, nil
}

func preparedParameterValues(payload []byte, offset int, types []preparedParameterType, longData map[uint16][]byte) ([]string, int, error) {
	values := make([]string, len(types))
	for index := range types {
		if preparedParameterIsNull(payload, index) {
			values[index] = "NULL"
			continue
		}
		if long, ok := longData[uint16(index)]; ok {
			values[index] = quote(string(long))
			continue
		}
		value, next, err := readPreparedValue(payload, offset, types[index])
		if err != nil {
			return nil, offset, err
		}
		values[index], offset = value, next
	}
	return values, offset, nil
}

func preparedParameterIsNull(payload []byte, index int) bool {
	return payload[10+index/8]&(1<<uint(index%8)) != 0
}

func readPreparedValue(payload []byte, offset int, typ preparedParameterType) (string, int, error) {
	if typ.typ == mysqlTypeNull {
		return "NULL", offset, nil
	}
	reader, ok := preparedValueReaders[typ.typ]
	if !ok {
		return "", offset, errors.New("unsupported prepared parameter type")
	}
	return reader(payload, offset, typ)
}

type preparedValueReader func([]byte, int, preparedParameterType) (string, int, error)

var preparedValueReaders = map[byte]preparedValueReader{
	mysqlTypeTiny: preparedIntegerReader(1), mysqlTypeShort: preparedIntegerReader(2), mysqlTypeLong: preparedIntegerReader(4), mysqlTypeLongLong: preparedIntegerReader(8),
	mysqlTypeFloat: readPreparedFloat, mysqlTypeDouble: readPreparedDouble,
	mysqlTypeVarchar: readPreparedString, mysqlTypeVarString: readPreparedString, mysqlTypeString: readPreparedString, mysqlTypeBlob: readPreparedString, mysqlTypeLongBlob: readPreparedString, mysqlTypeMediumBlob: readPreparedString, mysqlTypeTinyBlob: readPreparedString, mysqlTypeJSON: readPreparedString, mysqlTypeNewDecimal: readPreparedString,
}

func preparedIntegerReader(size int) preparedValueReader {
	return func(payload []byte, offset int, typ preparedParameterType) (string, int, error) {
		return readPreparedInteger(payload, offset, typ.unsigned, size)
	}
}
func readPreparedInteger(payload []byte, offset int, unsigned bool, size int) (string, int, error) {
	if offset+size > len(payload) {
		return "", offset, errors.New("malformed prepared parameter")
	}
	var value uint64
	for index := range size {
		value |= uint64(payload[offset+index]) << (8 * index)
	}
	if unsigned {
		return strconv.FormatUint(value, 10), offset + size, nil
	}
	return strconv.FormatInt(preparedSignedInteger(value, size), 10), offset + size, nil
}
func preparedSignedInteger(value uint64, size int) int64 {
	switch size {
	case 1:
		return int64(int8(value))
	case 2:
		return int64(int16(value))
	case 4:
		return int64(int32(value))
	default:
		return int64(value)
	}
}
func readPreparedFloat(payload []byte, offset int, _ preparedParameterType) (string, int, error) {
	if offset+4 > len(payload) {
		return "", offset, errors.New("malformed prepared parameter")
	}
	return strconv.FormatFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(payload[offset:offset+4]))), 'g', -1, 32), offset + 4, nil
}
func readPreparedDouble(payload []byte, offset int, _ preparedParameterType) (string, int, error) {
	if offset+8 > len(payload) {
		return "", offset, errors.New("malformed prepared parameter")
	}
	return strconv.FormatFloat(math.Float64frombits(binary.LittleEndian.Uint64(payload[offset:offset+8])), 'g', -1, 64), offset + 8, nil
}
func readPreparedString(payload []byte, offset int, _ preparedParameterType) (string, int, error) {
	raw, next, ok := readLengthEncoded(payload, offset)
	if !ok || raw == nil {
		return "", offset, errors.New("malformed string prepared parameter")
	}
	return quote(string(raw)), next, nil
}

func (s *preparedLifecycle) sendLongData(payload []byte) error {
	if len(payload) < 7 {
		return sqlFailure{1210, "HY000", "malformed prepared statement long data"}
	}
	id := binary.LittleEndian.Uint32(payload[1:5])
	statement, ok := s.statements[id]
	if !ok {
		return sqlFailure{1243, "HY000", "unknown prepared statement handler"}
	}
	parameter := binary.LittleEndian.Uint16(payload[5:7])
	if int(parameter) >= statement.parameters {
		return sqlFailure{1210, "HY000", "prepared statement parameter index out of range"}
	}
	if s.longDataBytes+len(payload[7:]) > maxPreparedLongDataBytes {
		return sqlFailure{1153, "08S01", "prepared statement long data exceeds maximum size"}
	}
	statement.longData[parameter] = append(statement.longData[parameter], payload[7:]...)
	s.longDataBytes += len(payload[7:])
	return nil
}

func (s *preparedLifecycle) resetPrepared(payload []byte) error {
	if len(payload) != 5 {
		return sqlFailure{1210, "HY000", "malformed prepared statement reset"}
	}
	statement, ok := s.statements[binary.LittleEndian.Uint32(payload[1:5])]
	if !ok {
		return sqlFailure{1243, "HY000", "unknown prepared statement handler"}
	}
	clearPreparedLongData(s.session, statement)
	return nil
}

func (s *preparedLifecycle) resetConnection() error {
	if err := rollbackTransaction(s.session); err != nil {
		return err
	}
	s.database = s.initialDB
	s.closeAllPrepared()
	s.longDataBytes = 0
	s.savepoints = make(map[string]catalog.Definition)
	return nil
}

func (s *preparedLifecycle) closePrepared(id uint32) {
	if statement, ok := s.statements[id]; ok {
		clearPreparedLongData(s.session, statement)
		delete(s.statements, id)
		s.server.connections.releasePreparedStatement()
	}
}

func (s *preparedLifecycle) closeAllPrepared() {
	for id := range s.statements {
		s.closePrepared(id)
	}
}

func clearPreparedLongData(session *session, statement *preparedStatement) {
	for _, value := range statement.longData {
		session.longDataBytes -= len(value)
	}
	statement.longData = make(map[uint16][]byte)
}

func rollbackTransaction(s *session) error {
	if s.transaction && s.server.config.Catalog != nil {
		if err := s.server.config.Catalog.Replace(s.transactionSnapshot); err != nil {
			return sqlFailure{1105, "HY000", err.Error()}
		}
	}
	s.transaction = false
	s.transactionSnapshot = catalog.Definition{}
	s.savepoints = make(map[string]catalog.Definition)
	return nil
}

func bindPreparedQuery(query string, values []string) (string, error) {
	positions := preparedPlaceholders(query)
	if len(positions) != len(values) {
		return "", errors.New("prepared statement parameter count does not match")
	}
	var result strings.Builder
	result.Grow(len(query) + len(values)*8)
	start := 0
	for index, position := range positions {
		result.WriteString(query[start:position])
		result.WriteString(values[index])
		start = position + 1
	}
	result.WriteString(query[start:])
	return result.String(), nil
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

const maximumPacketFrame = (1 << 24) - 1

func readPacket(r io.Reader, maximum int64) (byte, []byte, error) {
	if maximum <= 0 {
		return 0, nil, errors.New("packet maximum must be positive")
	}
	var payload []byte
	var sequence, expected byte
	for frame := 0; ; frame++ {
		header := make([]byte, 4)
		if _, err := io.ReadFull(r, header); err != nil {
			return 0, nil, err
		}
		if frame == 0 {
			sequence, expected = header[3], header[3]+1
		} else if header[3] != expected {
			return 0, nil, errors.New("packet continuation sequence mismatch")
		}
		sequence = header[3]
		expected++
		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		if int64(len(payload))+int64(length) > maximum {
			return 0, nil, errors.New("packet exceeds configured maximum size")
		}
		start := len(payload)
		payload = append(payload, make([]byte, length)...)
		if _, err := io.ReadFull(r, payload[start:]); err != nil {
			return 0, nil, err
		}
		if length < maximumPacketFrame {
			return sequence, payload, nil
		}
	}
}

func writeBoundedPacket(w io.Writer, sequence byte, payload []byte, maximum int64) error {
	if int64(len(payload)) > maximum {
		return errors.New("packet exceeds configured maximum size")
	}
	return writePacket(w, sequence, payload)
}

func nextPacketSequence(sequence byte, payload []byte) byte {
	return sequence + byte(len(payload)/maximumPacketFrame+1)
}

func writePacket(w io.Writer, sequence byte, payload []byte) error {
	for {
		length := len(payload)
		if length > maximumPacketFrame {
			length = maximumPacketFrame
		}
		header := []byte{byte(length), byte(length >> 8), byte(length >> 16), sequence}
		if _, err := w.Write(header); err != nil {
			return err
		}
		if _, err := w.Write(payload[:length]); err != nil {
			return err
		}
		payload = payload[length:]
		if length < maximumPacketFrame {
			return nil
		}
		sequence++
	}
}

func okPacket() []byte { return []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00} }

func errorPacket(code uint16, state, message string) []byte {
	if len(message) > 255 {
		message = message[:252] + "..."
	}
	payload := []byte{0xff, byte(code), byte(code >> 8), '#'}
	payload = append(payload, state...)
	payload = append(payload, message...)
	return payload
}

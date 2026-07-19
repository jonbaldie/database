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
	"time"

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

	mysqlCharsetBinary       uint16 = 63
	mysqlTypeLongLong        byte   = 0x08
	mysqlTypeNull            byte   = 0x06
	mysqlTypeTiny            byte   = 0x01
	mysqlTypeShort           byte   = 0x02
	mysqlTypeLong            byte   = 0x03
	mysqlTypeFloat           byte   = 0x04
	mysqlTypeDouble          byte   = 0x05
	mysqlTypeInt24           byte   = 0x09
	mysqlTypeBit             byte   = 0x10
	mysqlTypeVarchar         byte   = 0x0f
	mysqlTypeVarString       byte   = 0xfd
	mysqlTypeString          byte   = 0xfe
	mysqlTypeBlob            byte   = 0xfc
	mysqlTypeTinyBlob        byte   = 0xf9
	mysqlTypeMediumBlob      byte   = 0xfa
	mysqlTypeLongBlob        byte   = 0xfb
	mysqlTypeJSON            byte   = 0xf5
	mysqlTypeNewDecimal      byte   = 0xf6
	mysqlTypeTimestamp       byte   = 0x07
	mysqlTypeDate            byte   = 0x0a
	mysqlTypeTime            byte   = 0x0b
	mysqlTypeDatetime        byte   = 0x0c
	mysqlTypeYear            byte   = 0x0d
	mysqlNotNullFlag         uint16 = 1
	mysqlBinaryFlag          uint16 = 1 << 7
	mysqlUnsignedFlag        uint16 = 1 << 5
	maxPreparedParameters           = 65535
	maxPreparedLongDataBytes        = 16 * 1024 * 1024
	preparedTypesUnchanged   byte   = 0
	preparedTypesSupplied    byte   = 1
	storedSQLNullValue              = "\x00database-sql-null"
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
	// TimeZone is the fixed-offset session time zone that TIMESTAMP instants and
	// current-time functions render through. It defaults to UTC and accepts UTC
	// or a ±HH:MM offset within ±14:00.
	TimeZone string
	// Clock supplies the current instant for current-time functions. It defaults
	// to time.Now and is injectable so rendering is reproducible under test.
	Clock func() time.Time
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
	if config.TimeZone == "" {
		config.TimeZone = "UTC"
	}
	if config.Clock == nil {
		config.Clock = time.Now
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
	server          *Server
	username        string
	database        string
	initialDB       string
	timeZone        string
	initialTimeZone string
	statements      map[uint32]*preparedStatement
	nextStmtID      uint32
	longDataBytes   int
	transactionState
}

type transactionState struct {
	transactionSettings
	transactionWork
	savepointState
	statementState
}

type transactionSettings struct {
	autocommitOff bool
	isolation     isolationLevel
	readOnly      bool
	nextIsolation isolationLevel
	nextReadOnly  bool
}

type transactionWork struct {
	transaction          bool
	transactionSnapshot  catalog.Definition
	transactionRevision  uint64
	transactionStateSet  bool
	transactionReadSet   bool
	transactionDirty     bool
	transactionIsolation isolationLevel
	transactionReadOnly  bool
	transactionMutations []func(*catalog.Definition) error
}

type savepointState struct {
	savepoints         map[string]catalog.Definition
	savepointDirty     map[string]bool
	savepointMutations map[string]int
	savepointRead      map[string]bool
}

type statementState struct {
	statementDefinition    catalog.Definition
	statementDefinitionSet bool
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

type textStatementExecutor struct {
	*session
	streamRows bool
}

type transactionExecutor struct{ *session }

type catalogExecutor struct{ *session }

type databaseSelector struct{ *session }

type relationExecutor struct {
	*session
	streamRows bool
}

type informationSchemaExecutor struct{ *session }

func newQueryExecutor(session *session) *queryExecutor {
	return &queryExecutor{statements: textStatementExecutor{session: session}}
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
	if _, ok := a.config.Catalog.Snapshot().Namespaces[catalog.Key(name)]; !ok {
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
	affected uint64
	stream   queryRowStream
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
	result, err := s.executeProtocol(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	if len(result.columns) == 0 {
		return writePacket(connection, sequence, okPacket(result.affected))
	}
	return writeResult(connection, sequence, result, s.statements.server.config.MaxAllowedPacket)
}

func (s *queryExecutor) execute(query string) (*queryResult, error) {
	return s.statements.execute(query)
}

func (s *queryExecutor) executeProtocol(query string) (*queryResult, error) {
	executor := s.statements
	executor.streamRows = true
	return executor.execute(query)
}

func (s *queryExecutor) useDatabase(name string) { s.statements.useDatabase(name) }

func (s *queryExecutor) databaseExists(name string) error {
	selector := databaseSelector{s.statements.session}
	return selector.databaseExists(name)
}

func (s *textStatementExecutor) execute(query string) (*queryResult, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	lower := strings.ToLower(query)
	if lower == "" {
		return nil, sqlFailure{1065, "42000", "query was empty"}
	}
	return s.executeWithTransaction(query, lower)
}

type statementHandler func(query, lower string) (*queryResult, bool, error)

func (s *textStatementExecutor) statementHandlers() []statementHandler {
	return []statementHandler{
		s.transactionStatement,
		s.settingStatement,
		s.builtinStatement,
		s.ddlStatement,
		s.catalogStatement,
		s.relationStatement,
		s.operationStatement,
	}
}

func (s *textStatementExecutor) setTimeZone(query string) (bool, error) {
	assignment := strings.TrimSpace(query[len("SET "):])
	lhs, value, found := strings.Cut(assignment, "=")
	if !found {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(lhs)) {
	case "time_zone", "session.time_zone", "session time_zone", "@@time_zone", "@@session.time_zone", "@@session time_zone":
	default:
		return false, nil
	}
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "default") {
		s.timeZone = s.initialTimeZone
		return true, nil
	}
	offset, err := parseFixedOffset(scalar(value))
	if err != nil {
		return true, err
	}
	s.timeZone = formatFixedOffset(offset)
	return true, nil
}

func (s *textStatementExecutor) builtinStatement(_ string, lower string) (*queryResult, bool, error) {
	if column, kind, precision, ok := currentTimeQuery(lower); ok {
		value, err := s.renderCurrentTime(kind, precision)
		if err != nil {
			return nil, true, err
		}
		return &queryResult{
			columns:  []string{column},
			rows:     [][]string{{value}},
			metadata: []columnMetadata{temporalResultMetadata(column, kind, precision)},
		}, true, nil
	}
	if lower == "select @@time_zone" || lower == "select @@session.time_zone" || lower == "select @@session time_zone" {
		offset, err := sessionTimeZoneOffset(s.session)
		if err != nil {
			return nil, true, err
		}
		return &queryResult{
			columns: []string{"@@time_zone"},
			rows:    [][]string{{formatFixedOffset(offset)}},
			metadata: []columnMetadata{{
				catalog: "def", name: "@@time_zone", characterSet: mysqlCharsetUTF8MB40900AICI,
				typ: mysqlTypeVarString, length: 6,
			}},
		}, true, nil
	}
	result, found := map[string]*queryResult{
		"select version()":  {columns: []string{"VERSION()"}, rows: [][]string{{s.server.config.Version}}},
		"select @@version":  {columns: []string{"VERSION()"}, rows: [][]string{{s.server.config.Version}}},
		"select database()": {columns: []string{"DATABASE()"}, rows: [][]string{{s.database}}},
	}[lower]
	return result, found, nil
}

// currentTimeQuery maps a recognized current-time SELECT to its result column
// label, the temporal kind that shapes its value, and its fractional precision.
func currentTimeQuery(lower string) (string, temporalKind, int, bool) {
	lower = strings.TrimSpace(lower)
	for _, candidate := range []struct {
		functions []string
		label     string
		kind      temporalKind
		fraction  bool
	}{
		{functions: []string{"current_date", "curdate"}, label: "CURRENT_DATE", kind: temporalDate},
		{functions: []string{"current_time", "curtime"}, label: "CURRENT_TIME", kind: temporalTime, fraction: true},
		{functions: []string{"current_timestamp", "now"}, label: "CURRENT_TIMESTAMP", kind: temporalDatetime, fraction: true},
	} {
		for _, function := range candidate.functions {
			if precision, ok := currentTimeCallPrecision(lower, function, candidate.fraction); ok {
				return candidate.label, candidate.kind, precision, true
			}
		}
	}
	return "", temporalNone, 0, false
}

func currentTimeCallPrecision(query, function string, allowFraction bool) (int, bool) {
	prefix := "select " + function
	if query == prefix || query == prefix+"()" {
		return 0, true
	}
	if !allowFraction || !strings.HasPrefix(query, prefix+"(") || !strings.HasSuffix(query, ")") {
		return 0, false
	}
	argument := strings.TrimSpace(query[len(prefix)+1 : len(query)-1])
	precision, err := strconv.Atoi(argument)
	if err != nil || precision < 0 || precision > 6 {
		return 0, false
	}
	return precision, true
}

// renderCurrentTime evaluates a current-time function against the configured
// clock, rendered through the fixed-offset session time zone. A TIMESTAMP is the
// current instant rendered through the offset; a DATE or TIME is the session-
// local wall clock. Both read one captured instant, so references within a
// statement observe the same value.
func (s *textStatementExecutor) renderCurrentTime(kind temporalKind, precision int) (string, error) {
	offset, err := sessionTimeZoneOffset(s.session)
	if err != nil {
		return "", err
	}
	instant := s.server.config.Clock().UTC()
	local := instant.Add(time.Duration(offset) * time.Minute)
	return currentTemporal(local, kind, precision), nil
}

func (s *textStatementExecutor) operationStatement(query, lower string) (*queryResult, bool, error) {
	if strings.HasPrefix(lower, "explain ") {
		result, err := s.explainStatement(query)
		return result, true, err
	}
	if strings.HasPrefix(lower, "show processlist") {
		return &queryResult{columns: []string{"Id"}}, true, nil
	}
	return nil, false, nil
}

func (s *textStatementExecutor) transactionStatement(query, lower string) (*queryResult, bool, error) {
	handler, handled := findTransactionHandler(lower)
	if !handled {
		return nil, false, nil
	}
	return nil, true, handler(s.session, query)
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
	namespace, found := s.metadataDefinition().Namespaces[catalog.Key(s.database)]
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
	relations := relationExecutor{session: s.session, streamRows: s.streamRows}
	switch {
	case strings.HasPrefix(lower, "create table "):
		return nil, true, createTable(&relations, query)
	case strings.HasPrefix(lower, "insert into "):
		affected, err := insertRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case strings.HasPrefix(lower, "update "):
		affected, err := updateRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case strings.HasPrefix(lower, "delete from "):
		affected, err := deleteRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case strings.HasPrefix(lower, "select "):
		result, err := selectQuery(&relations, query)
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
	return s.session.databaseExists(identifier(name))
}

func (s *catalogExecutor) metadataDefinition() catalog.Definition {
	if s.server.config.Catalog == nil {
		return emptyDefinition()
	}
	// Catalog metadata is statement-scoped even when ordinary data reads use
	// a repeatable-read transaction snapshot.
	return s.server.config.Catalog.Snapshot()
}

func snapshotNamespace(s *relationExecutor, name string) (catalog.Namespace, bool) {
	ns, ok := s.session.currentDefinition().Namespaces[catalog.Key(name)]
	return ns, ok
}
func (s *catalogExecutor) createDatabase(query string) error {
	lower := strings.ToLower(query)
	keyword := "database "
	if strings.HasPrefix(lower, "create schema ") {
		keyword = "schema "
	}
	value := strings.TrimSpace(query[len("create ")+len(keyword):])
	ifNotExists := false
	if strings.HasPrefix(strings.ToLower(value), "if not exists ") {
		ifNotExists = true
		value = strings.TrimSpace(value[len("IF NOT EXISTS "):])
	}
	name, ok := singleIdentifier(value)
	if !ok {
		return sqlFailure{1064, "42000", "malformed CREATE DATABASE"}
	}
	if err := validateIdentifierLength(name); err != nil {
		return err
	}
	if strings.EqualFold(name, informationSchemaName) {
		return sqlFailure{1044, "42000", "information_schema is read-only"}
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		key := catalog.Key(name)
		if _, exists := definition.Namespaces[key]; exists {
			if ifNotExists {
				return nil
			}
			return errors.New("namespace already exists")
		}
		definition.Namespaces[key] = catalog.Namespace{Name: name, Tables: map[string]catalog.Table{}}
		return nil
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1007, "HY000", err.Error()})
	}
	return nil
}
func createTable(s *relationExecutor, query string) error {
	table, err := parseCreateTable(query)
	if err != nil {
		return err
	}
	namespace, name, err := tableTarget(s, table.target)
	if err != nil {
		return err
	}
	if len(table.columns) == 0 || s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		return createTableInDefinition(definition, namespace, name, table)
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1050, "42S01", err.Error()})
	}
	return nil
}

func createTableInDefinition(definition *catalog.Definition, namespace, name string, table tableDefinition) error {
	namespaceDefinition, exists := definition.Namespaces[catalog.Key(namespace)]
	if !exists {
		return errors.New("namespace does not exist")
	}
	key := catalog.Key(name)
	if _, exists := namespaceDefinition.Tables[key]; exists {
		if table.ifNotExists {
			return nil
		}
		return errors.New("table already exists")
	}
	tableDefinition := catalog.Table{Name: name, Columns: append([]string(nil), table.columns...)}
	if len(table.types) > 0 {
		tableDefinition.ColumnTypes = append([]string(nil), table.types...)
	}
	namespaceDefinition.Tables[key] = tableDefinition
	definition.Namespaces[catalog.Key(namespace)] = namespaceDefinition
	return nil
}

type tableDefinition struct {
	target      []string
	columns     []string
	types       []string
	ifNotExists bool
}

func parseCreateTable(query string) (tableDefinition, error) {
	head, body, err := createTableParts(query)
	if err != nil {
		return tableDefinition{}, err
	}
	ifNotExists := false
	if strings.HasPrefix(strings.ToLower(head), "if not exists ") {
		ifNotExists = true
		head = strings.TrimSpace(head[len("IF NOT EXISTS "):])
	}
	target, ok := splitQualifiedIdentifier(head)
	if !ok || len(target) == 0 || len(target) > 2 {
		return tableDefinition{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	for _, part := range target {
		if err := validateIdentifierLength(part); err != nil {
			return tableDefinition{}, err
		}
	}
	columns, types, err := parseTableColumns(body)
	if err == nil {
		err = validateTableColumns(columns)
	}
	return tableDefinition{target: target, columns: columns, types: types, ifNotExists: ifNotExists}, err
}

func validateTableColumns(columns []string) error {
	if len(columns) > maxTableColumns {
		return sqlFailure{1117, "HY000", "too many columns"}
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		key := catalog.Key(column)
		if _, duplicate := seen[key]; duplicate {
			return sqlFailure{1060, "42S21", "duplicate column name '" + column + "'"}
		}
		seen[key] = struct{}{}
	}
	return nil
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
	if err := validateIdentifierLength(column); err != nil {
		return "", "", err
	}
	fields := strings.Fields(remainder)
	if isUnsupportedTableDefinition(column) || hasUnsupportedColumnModifier(fields) {
		return "", "", sqlFailure{1235, "42000", "unsupported table definition"}
	}
	if len(fields) == 0 {
		return column, "", nil
	}
	typeName, err := columnTypeName(fields)
	if err != nil {
		return "", "", err
	}
	return column, typeName, nil
}

// columnTypeName folds a trailing UNSIGNED modifier into the declared type and
// rejects any numeric or bit declaration that violates a public ceiling.
func columnTypeName(fields []string) (string, error) {
	typeName := strings.ToUpper(fields[0])
	rest := fields[1:]
	if len(rest) >= 1 && strings.EqualFold(rest[0], "unsigned") {
		typeName += " UNSIGNED"
		rest = rest[1:]
	}
	if _, err := parseNumericType(typeName); err != nil {
		return "", err
	}
	if _, err := parseTemporalType(typeName); err != nil {
		return "", err
	}
	typeName, err := characterModifierTypeName(typeName, rest)
	if err != nil {
		return "", err
	}
	if _, err := parseCharacterType(typeName); err != nil {
		return "", err
	}
	return typeName, nil
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
	key := catalog.Key(name)
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
	namespace, ok := s.metadataDefinition().Namespaces[catalog.Key(namespaceName)]
	if !ok {
		return nil, sqlFailure{1049, "42000", "unknown database '" + namespaceName + "'"}
	}
	table, ok := namespace.Tables[catalog.Key(tableName)]
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

func mutateTableRows(definition *catalog.Definition, namespaceName, tableName string, transform func(catalog.Table) ([][]string, error)) error {
	namespace, found := definition.Namespaces[catalog.Key(namespaceName)]
	if !found {
		return errors.New("namespace does not exist")
	}
	table, found := namespace.Tables[catalog.Key(tableName)]
	if !found {
		return errors.New("table does not exist")
	}
	rows, err := transform(table)
	if err != nil {
		return err
	}
	table.Rows = cloneRows(rows)
	namespace.Tables[catalog.Key(tableName)] = table
	definition.Namespaces[catalog.Key(namespaceName)] = namespace
	return nil
}

func insertRows(s *relationExecutor, query string) (uint64, error) {
	plan, err := makeInsertPlan(s, query)
	if err != nil {
		return 0, err
	}
	_, affected, err := applyInsertPlan(plan)
	if err != nil {
		return 0, err
	}
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.namespace, plan.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.table = table
			rows, _, err := applyInsertPlan(currentPlan)
			return rows, err
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

type insertPlan struct {
	namespace, name string
	table           catalog.Table
	columns         []int
	groups          [][]string
	offsetMinutes   int
}

func makeInsertPlan(s *relationExecutor, query string) (insertPlan, error) {
	parts, columns, groups, err := parseInsertInput(query)
	if err != nil {
		return insertPlan{}, err
	}
	namespace, name, err := tableTarget(s, parts)
	if err != nil {
		return insertPlan{}, err
	}
	table, err := relationTable(s, namespace, name)
	if err != nil {
		return insertPlan{}, err
	}
	indexes, err := insertColumnIndexes(table, columns)
	if err != nil {
		return insertPlan{}, err
	}
	offsetMinutes, err := sessionTimeZoneOffset(s.session)
	if err != nil {
		return insertPlan{}, err
	}
	return insertPlan{namespace: namespace, name: name, table: table, columns: indexes, groups: groups, offsetMinutes: offsetMinutes}, nil
}

func parseInsertInput(query string) ([]string, []string, [][]string, error) {
	head, valueText, ok := splitInsert(query)
	if !ok {
		return nil, nil, nil, sqlFailure{1064, "42000", "malformed INSERT"}
	}
	parts, columns, ok := insertTarget(head)
	if !ok || len(parts) == 0 || len(parts) > 2 {
		return nil, nil, nil, sqlFailure{1064, "42000", "malformed INSERT"}
	}
	groups, ok := valueGroups(valueText)
	if !ok || len(groups) == 0 {
		return nil, nil, nil, sqlFailure{1064, "42000", "malformed INSERT"}
	}
	return parts, columns, groups, nil
}

func insertColumnIndexes(table catalog.Table, columns []string) ([]int, error) {
	if len(columns) == 0 {
		columns = table.Columns
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return nil, err
	}
	result := make([]int, len(columns))
	seen := make(map[int]bool, len(columns))
	for index, column := range columns {
		columnIndex, found := indexes[catalog.Key(column)]
		if !found || seen[columnIndex] {
			return nil, sqlFailure{1054, "42S22", "unknown or duplicate column '" + column + "'"}
		}
		seen[columnIndex], result[index] = true, columnIndex
	}
	return result, nil
}

func applyInsertPlan(plan insertPlan) ([][]string, uint64, error) {
	rows := cloneRows(plan.table.Rows)
	rowNumber := 1
	for _, group := range plan.groups {
		if len(group) != len(plan.columns) {
			return nil, 0, sqlFailure{1136, "21S01", "column count does not match value count"}
		}
		row := make([]string, len(plan.table.Columns))
		for valueIndex, value := range group {
			columnIndex := plan.columns[valueIndex]
			canonical, err := canonicalColumnValueAtOffset(plan.table, columnIndex, value, rowNumber, plan.offsetMinutes)
			if err != nil {
				return nil, 0, err
			}
			row[columnIndex] = canonical
		}
		rows = append(rows, row)
		rowNumber++
	}
	return rows, uint64(len(plan.groups)), nil
}

// canonicalColumnValue enforces the strict value contract for a written column.
// A column without a recorded numeric, bit, character, or temporal type keeps
// its literal scalar so a typeless column is unaffected by this seam.
func canonicalColumnValue(table catalog.Table, columnIndex int, raw string, row int) (string, error) {
	return canonicalColumnValueAtOffset(table, columnIndex, raw, row, 0)
}

func canonicalColumnValueAtOffset(table catalog.Table, columnIndex int, raw string, row, offsetMinutes int) (string, error) {
	if isSQLNullLiteral(raw) {
		return storedSQLNullValue, nil
	}
	value := scalar(raw)
	preparedWire, preparedValue, prepared := decodePreparedTemporalLiteral(value)
	if prepared {
		value = preparedValue
	}
	typeName, known := table.ColumnType(columnIndex)
	if !known {
		return value, nil
	}
	if err := rejectQuotedTemporalNull(typeName, value, table.Columns[columnIndex], row); err != nil {
		return "", err
	}
	value, err := normalizePreparedDate(typeName, value, preparedWire, prepared, table.Columns[columnIndex], row)
	if err != nil {
		return "", err
	}
	return canonicalTypedValueAtOffset(typeName, value, table.Columns[columnIndex], row, offsetMinutes)
}

func rejectQuotedTemporalNull(typeName, value, column string, row int) error {
	if !isSQLNullSpelling(value) {
		return nil
	}
	typ, err := parseTemporalType(typeName)
	if err != nil {
		return err
	}
	if typ.kind == temporalNone {
		return nil
	}
	return incorrectTemporal(temporalLabel(typ.kind), column, value, row)
}

func normalizePreparedDate(typeName, value string, wire byte, prepared bool, column string, row int) (string, error) {
	if !prepared || wire != mysqlTypeDate {
		return value, nil
	}
	typ, err := parseTemporalType(typeName)
	if err != nil || typ.kind != temporalDatetime {
		return value, nil
	}
	date, err := canonicalDateValue(value, column, row)
	if err != nil {
		return "", err
	}
	return date + " 00:00:00", nil
}

// scalarCanonicalizer reports whether a declared type belongs to its family and,
// if so, the canonical value or the rejection error for that family.
type scalarCanonicalizer func(typeName, value, column string, row, offsetMinutes int) (bool, string, error)

// scalarCanonicalizers is the ordered set of strict scalar value contracts a
// written column is routed through: numeric or bit, character or binary, then
// temporal. The first family that claims the declared type owns the value.
var scalarCanonicalizers = []scalarCanonicalizer{
	numericCanonicalizer,
	characterCanonicalizer,
	temporalCanonicalizer,
}

// canonicalTypedValue routes a value to the strict contract of its declared
// scalar family, returning the value unchanged for a typeless column.
func canonicalTypedValue(typeName, value, column string, row int) (string, error) {
	return canonicalTypedValueAtOffset(typeName, value, column, row, 0)
}

func canonicalTypedValueAtOffset(typeName, value, column string, row, offsetMinutes int) (string, error) {
	for _, canonicalize := range scalarCanonicalizers {
		if matched, canonical, err := canonicalize(typeName, value, column, row, offsetMinutes); matched {
			return canonical, err
		}
	}
	return value, nil
}

func numericCanonicalizer(typeName, value, column string, row, _ int) (bool, string, error) {
	typ, err := parseNumericType(typeName)
	if err != nil {
		return true, value, err
	}
	if typ.kind == numericNone {
		return false, value, nil
	}
	canonical, cerr := canonicalNumericValue(typ, value, column, row)
	return true, canonical, cerr
}

func characterCanonicalizer(typeName, value, column string, row, _ int) (bool, string, error) {
	typ, err := parseCharacterType(typeName)
	if err != nil {
		return true, value, err
	}
	if typ.kind == characterNone {
		return false, value, nil
	}
	canonical, cerr := canonicalCharacterValue(typ, value, column, row)
	return true, canonical, cerr
}

func temporalCanonicalizer(typeName, value, column string, row, offsetMinutes int) (bool, string, error) {
	typ, err := parseTemporalType(typeName)
	if err != nil {
		return true, value, err
	}
	if typ.kind == temporalNone {
		return false, value, nil
	}
	canonical, cerr := canonicalTemporalValueAtOffset(typ, value, column, row, offsetMinutes)
	return true, canonical, cerr
}

func updateRows(s *relationExecutor, query string) (uint64, error) {
	plan, err := makeUpdatePlan(s, query)
	if err != nil {
		return 0, err
	}
	_, affected, err := applyUpdatePlan(plan)
	if err != nil {
		return 0, err
	}
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.namespace, plan.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.table = table
			rows, _, err := applyUpdatePlan(currentPlan)
			return rows, err
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

type updatePlan struct {
	namespace, name string
	table           catalog.Table
	updates         map[int]string
	matcher         func([]string) bool
	offsetMinutes   int
}

func makeUpdatePlan(s *relationExecutor, query string) (updatePlan, error) {
	target, assignments, where, err := parseUpdateInput(query)
	if err != nil {
		return updatePlan{}, err
	}
	namespace, name, table, err := planTable(s, target)
	if err != nil {
		return updatePlan{}, err
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return updatePlan{}, err
	}
	updates, err := assignmentValues(assignments, indexes)
	if err != nil {
		return updatePlan{}, err
	}
	offsetMinutes, err := sessionTimeZoneOffset(s.session)
	if err != nil {
		return updatePlan{}, err
	}
	matcher, err := rowMatcherAtOffset(where, table, indexes, offsetMinutes)
	if err != nil {
		return updatePlan{}, err
	}
	return updatePlan{namespace: namespace, name: name, table: table, updates: updates, matcher: matcher, offsetMinutes: offsetMinutes}, nil
}

func parseUpdateInput(query string) (string, string, string, error) {
	rest := strings.TrimSpace(query[len("UPDATE "):])
	setAt := keywordAt(rest, "set")
	if setAt < 0 {
		return "", "", "", sqlFailure{1064, "42000", "malformed UPDATE"}
	}
	assignments, where, ok := splitWhere(strings.TrimSpace(rest[setAt+len("set"):]))
	if !ok || assignments == "" {
		return "", "", "", sqlFailure{1064, "42000", "malformed UPDATE"}
	}
	return strings.TrimSpace(rest[:setAt]), assignments, where, nil
}

func planTable(s *relationExecutor, target string) (string, string, catalog.Table, error) {
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return "", "", catalog.Table{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	namespace, name, err := tableTarget(s, parts)
	if err != nil {
		return "", "", catalog.Table{}, err
	}
	table, err := relationTable(s, namespace, name)
	return namespace, name, table, err
}

func assignmentValues(value string, indexes map[string]int) (map[int]string, error) {
	updates := make(map[int]string)
	seen := make(map[int]bool)
	for _, assignment := range splitCSV(value) {
		column, rawValue, ok := splitEquals(assignment)
		if !ok {
			return nil, sqlFailure{1064, "42000", "malformed UPDATE assignment"}
		}
		column, ok = singleIdentifier(column)
		if !ok {
			return nil, sqlFailure{1064, "42000", "invalid UPDATE column"}
		}
		index, found := indexes[catalog.Key(column)]
		if !found || seen[index] {
			return nil, sqlFailure{1054, "42S22", "unknown or duplicate column '" + column + "'"}
		}
		seen[index], updates[index] = true, scalar(rawValue)
	}
	return updates, nil
}

func applyUpdatePlan(plan updatePlan) ([][]string, uint64, error) {
	updates, err := canonicalUpdates(plan)
	if err != nil {
		return nil, 0, err
	}
	rows, affected := cloneRows(plan.table.Rows), uint64(0)
	for rowIndex, row := range rows {
		if !plan.matcher(row) {
			continue
		}
		changed := false
		for column, value := range updates {
			if rows[rowIndex][column] != value {
				changed = true
			}
			rows[rowIndex][column] = value
		}
		if changed {
			affected++
		}
	}
	return rows, affected, nil
}

// canonicalUpdates validates every assignment constant against its column type
// before any row changes, so a rejected UPDATE leaves the table untouched.
func canonicalUpdates(plan updatePlan) (map[int]string, error) {
	updates := make(map[int]string, len(plan.updates))
	for column, value := range plan.updates {
		canonical, err := canonicalColumnValueAtOffset(plan.table, column, value, 1, plan.offsetMinutes)
		if err != nil {
			return nil, err
		}
		updates[column] = canonical
	}
	return updates, nil
}

func deleteRows(s *relationExecutor, query string) (uint64, error) {
	plan, err := makeDeletePlan(s, query)
	if err != nil {
		return 0, err
	}
	_, affected := applyDeletePlan(plan)
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.namespace, plan.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.table = table
			rows, _ := applyDeletePlan(currentPlan)
			return rows, nil
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

type deletePlan struct {
	namespace, name string
	table           catalog.Table
	matcher         func([]string) bool
	offsetMinutes   int
}

func makeDeletePlan(s *relationExecutor, query string) (deletePlan, error) {
	target, where, err := parseDeleteInput(query)
	if err != nil {
		return deletePlan{}, err
	}
	namespace, name, table, err := planTable(s, target)
	if err != nil {
		return deletePlan{}, err
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return deletePlan{}, err
	}
	offsetMinutes, err := sessionTimeZoneOffset(s.session)
	if err != nil {
		return deletePlan{}, err
	}
	matcher, err := rowMatcherAtOffset(where, table, indexes, offsetMinutes)
	if err != nil {
		return deletePlan{}, err
	}
	return deletePlan{namespace: namespace, name: name, table: table, matcher: matcher, offsetMinutes: offsetMinutes}, nil
}

func parseDeleteInput(query string) (string, string, error) {
	rest := strings.TrimSpace(query[len("DELETE FROM "):])
	target, where, ok := splitWhere(rest)
	if !ok || target == "" {
		return "", "", sqlFailure{1064, "42000", "malformed DELETE"}
	}
	return target, where, nil
}

func applyDeletePlan(plan deletePlan) ([][]string, uint64) {
	rows, affected := make([][]string, 0, len(plan.table.Rows)), uint64(0)
	for _, row := range plan.table.Rows {
		if plan.matcher(row) {
			affected++
			continue
		}
		rows = append(rows, append([]string(nil), row...))
	}
	return rows, affected
}

func relationTable(s *relationExecutor, namespace, name string) (catalog.Table, error) {
	ns, found := snapshotNamespace(s, namespace)
	if !found {
		return catalog.Table{}, sqlFailure{1049, "42000", "unknown database '" + namespace + "'"}
	}
	table, found := ns.Tables[catalog.Key(name)]
	if !found {
		return catalog.Table{}, sqlFailure{1146, "42S02", "table does not exist"}
	}
	return table, nil
}

func tableColumnIndexes(table catalog.Table) (map[string]int, error) {
	indexes := make(map[string]int, len(table.Columns))
	for index, column := range table.Columns {
		key := catalog.Key(column)
		if _, duplicate := indexes[key]; duplicate {
			return nil, sqlFailure{1105, "HY000", "catalog contains duplicate column '" + column + "'"}
		}
		indexes[key] = index
	}
	return indexes, nil
}

func cloneRows(rows [][]string) [][]string {
	copy := make([][]string, len(rows))
	for index, row := range rows {
		copy[index] = append([]string(nil), row...)
	}
	return copy
}

func splitInsert(query string) (string, string, bool) {
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	position := keywordAt(rest, "values")
	if position < 0 {
		return "", "", false
	}
	return strings.TrimSpace(rest[:position]), strings.TrimSpace(rest[position+len("values"):]), true
}

func insertTarget(value string) ([]string, []string, bool) {
	value = strings.TrimSpace(value)
	open := strings.IndexByte(value, '(')
	if open < 0 {
		parts, ok := splitQualifiedIdentifier(value)
		return parts, nil, ok
	}
	close, ok := matchingParenthesis(value, open)
	if !ok || strings.TrimSpace(value[close+1:]) != "" {
		return nil, nil, false
	}
	parts, ok := splitQualifiedIdentifier(strings.TrimSpace(value[:open]))
	if !ok {
		return nil, nil, false
	}
	columns := splitCSV(value[open+1 : close])
	if len(columns) == 0 || columns[0] == "" {
		return nil, nil, false
	}
	for index, column := range columns {
		name, valid := singleIdentifier(column)
		if !valid {
			return nil, nil, false
		}
		columns[index] = name
	}
	return parts, columns, true
}

func valueGroups(value string) ([][]string, bool) {
	groups := make([][]string, 0)
	for value = strings.TrimSpace(value); value != ""; value = strings.TrimSpace(value) {
		if value[0] != '(' {
			return nil, false
		}
		close, ok := matchingParenthesis(value, 0)
		if !ok {
			return nil, false
		}
		groups = append(groups, splitCSV(value[1:close]))
		value = strings.TrimSpace(value[close+1:])
		if value == "" {
			break
		}
		if value[0] != ',' {
			return nil, false
		}
		value = strings.TrimSpace(value[1:])
		if value == "" {
			return nil, false
		}
	}
	return groups, true
}

func matchingParenthesis(value string, open int) (int, bool) {
	depth := 0
	limit := len(value)
	for index := open; index < limit; index++ {
		if value[index] == '\'' {
			index = skipQuoted(value, index)
			continue
		}
		switch value[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

func keywordAt(value, keyword string) int {
	lower := strings.ToLower(value)
	limit := len(lower) - len(keyword)
	depth := 0
	for index := 0; index <= limit; index++ {
		if lower[index] == '\'' {
			index = skipQuoted(lower, index)
			continue
		}
		switch lower[index] {
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if lower[index:index+len(keyword)] != keyword || !keywordBoundary(lower, index, len(keyword)) {
			continue
		}
		return index
	}
	return -1
}

func skipQuoted(value string, start int) int {
	limit := len(value)
	for index := start + 1; index < limit; index++ {
		if value[index] != '\'' {
			continue
		}
		if index+1 < len(value) && value[index+1] == '\'' {
			index++
			continue
		}
		return index
	}
	return len(value) - 1
}

func keywordBoundary(value string, index, length int) bool {
	before := index == 0 || strings.ContainsRune(" \t\n", rune(value[index-1]))
	after := index+length == len(value) || strings.ContainsRune(" \t\n", rune(value[index+length]))
	return before && after
}

func splitWhere(value string) (string, string, bool) {
	position := keywordAt(value, "where")
	if position < 0 {
		return strings.TrimSpace(value), "", true
	}
	before, after := strings.TrimSpace(value[:position]), strings.TrimSpace(value[position+len("where"):])
	return before, after, before != "" && after != ""
}

func splitEquals(value string) (string, string, bool) {
	quoted := false
	limit := len(value)
	for index := 0; index < limit; index++ {
		if value[index] == '\'' {
			if quoted && index+1 < len(value) && value[index+1] == '\'' {
				index++
				continue
			}
			quoted = !quoted
			continue
		}
		if !quoted && value[index] == '=' {
			left, right := strings.TrimSpace(value[:index]), strings.TrimSpace(value[index+1:])
			return left, right, left != "" && right != ""
		}
	}
	return "", "", false
}

func rowMatcher(where string, table catalog.Table, indexes map[string]int) (func([]string) bool, error) {
	return rowMatcherAtOffset(where, table, indexes, 0)
}

func rowMatcherAtOffset(where string, table catalog.Table, indexes map[string]int, offsetMinutes int) (func([]string) bool, error) {
	if where == "" {
		return func([]string) bool { return true }, nil
	}
	column, value, ok := splitEquals(where)
	if !ok {
		return nil, sqlFailure{1064, "42000", "unsupported WHERE clause"}
	}
	column, ok = singleIdentifier(column)
	if !ok {
		return nil, sqlFailure{1064, "42000", "invalid WHERE column"}
	}
	index, found := indexes[catalog.Key(column)]
	if !found {
		return nil, sqlFailure{1054, "42S22", "unknown column '" + column + "'"}
	}
	if isSQLNullLiteral(value) {
		return func([]string) bool { return false }, nil
	}
	want, err := matcherValueAtOffsetChecked(table, index, value, offsetMinutes)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(want, "null") {
		return func([]string) bool { return false }, nil
	}
	equalityKey := columnEqualityKey(table, index)
	wantKey := equalityKey(want)
	return func(row []string) bool { return index < len(row) && equalityKey(row[index]) == wantKey }, nil
}

// columnEqualityKey returns the transform that decides equality for a column.
// A character column compares through its collation key so utf8mb4_0900_ai_ci
// matches case- and accent-insensitively and utf8mb4_bin matches bytewise;
// every other column compares its stored representation verbatim.
func columnEqualityKey(table catalog.Table, index int) func(string) string {
	typeName, known := table.ColumnType(index)
	if !known {
		return func(value string) string { return value }
	}
	typ, err := parseCharacterType(typeName)
	if err != nil || typ.kind == characterNone {
		return func(value string) string { return value }
	}
	return func(value string) string { return characterComparisonKey(typ, value) }
}

// matcherValue canonicalizes an equality literal to the stored representation of
// its column, so a numeric predicate compares against the same canonical form a
// write produced (for example WHERE n = 007 matches a stored 7). The execution
// path uses the checked form so malformed temporal predicates fail explicitly.
func matcherValue(table catalog.Table, index int, value string) string {
	return matcherValueAtOffset(table, index, value, 0)
}

func matcherValueAtOffset(table catalog.Table, index int, value string, offsetMinutes int) string {
	canonical, _ := matcherValueAtOffsetChecked(table, index, value, offsetMinutes)
	return canonical
}

func matcherValueAtOffsetChecked(table catalog.Table, index int, value string, offsetMinutes int) (string, error) {
	raw := scalar(value)
	_, want, _ := decodePreparedTemporalLiteral(raw)
	typeName, known := table.ColumnType(index)
	if !known {
		return want, nil
	}
	if isSQLNullSpelling(want) && !isSQLNullLiteral(value) {
		typ, err := parseTemporalType(typeName)
		if err != nil {
			return want, err
		}
		if typ.kind != temporalNone {
			return want, incorrectTemporal(temporalLabel(typ.kind), table.Columns[index], want, 1)
		}
	}
	if canonical, ok := canonicalMatcherNumeric(typeName, want, table.Columns[index]); ok {
		return canonical, nil
	}
	return canonicalMatcherTemporal(typeName, want, raw, table.Columns[index], offsetMinutes)
}

func canonicalMatcherNumeric(typeName, value, column string) (string, bool) {
	typ, err := parseNumericType(typeName)
	if err != nil || typ.kind == numericNone {
		return value, false
	}
	canonical, err := canonicalNumericValue(typ, value, column, 1)
	if err != nil {
		return value, true
	}
	return canonical, true
}

func canonicalMatcherTemporal(typeName, value, raw, column string, offsetMinutes int) (string, error) {
	typ, err := parseTemporalType(typeName)
	if err != nil {
		return value, err
	}
	if typ.kind == temporalNone {
		return value, nil
	}
	if wire, _, prepared := decodePreparedTemporalLiteral(raw); prepared && wire == mysqlTypeDate && typ.kind == temporalDatetime {
		date, err := canonicalDateValue(value, column, 1)
		if err != nil {
			return value, err
		}
		value = date + " 00:00:00"
	}
	canonical, err := canonicalTemporalValueAtOffset(typ, value, column, 1, offsetMinutes)
	if err != nil {
		return value, err
	}
	return canonical, nil
}

func sessionTimeZoneOffset(s *session) (int, error) {
	return parseFixedOffset(s.timeZone)
}
func selectLiteral(expression string) (*queryResult, error) {
	value, isNull, metadata, err := scalarColumn(expression)
	if err != nil {
		return nil, err
	}
	return &queryResult{columns: []string{expression}, rows: [][]string{{value}}, nulls: [][]bool{{isNull}}, metadata: []columnMetadata{metadata}}, nil
}

func selectFrom(s *relationExecutor, query, projectionText, sourceText string) (*queryResult, error) {
	projection, source := strings.TrimSpace(projectionText), strings.TrimSpace(sourceText)
	if isInformationSchemaSource(source) {
		informationSchema := informationSchemaExecutor{s.session}
		return informationSchema.selectInformationSchema(query)
	}
	target, where, valid := splitWhere(source)
	if !valid {
		return nil, sqlFailure{1064, "42000", "malformed SELECT"}
	}
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return nil, sqlFailure{1064, "42000", "invalid table name"}
	}
	return selectRows(s, projection, parts, where)
}

func isInformationSchemaSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, informationSchemaName+".") || strings.HasPrefix(lower, "`information_schema`.")
}

func selectRows(s *relationExecutor, projection string, parts []string, where string) (*queryResult, error) {
	namespace, tableName, err := tableTarget(s, parts)
	if err != nil {
		return nil, err
	}
	table, err := relationTable(s, namespace, tableName)
	if err != nil {
		return nil, err
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return nil, err
	}
	selected, columns, err := selectedColumns(table, projection, indexes)
	if err != nil {
		return nil, err
	}
	offsetMinutes, err := sessionTimeZoneOffset(s.session)
	if err != nil {
		return nil, err
	}
	matches, err := rowMatcherAtOffset(where, table, indexes, offsetMinutes)
	if err != nil {
		return nil, err
	}
	rows := projectRows(table.Rows, selected, matches)
	rows, err = renderSelectedTemporalRows(rows, selected, table, offsetMinutes)
	if err != nil {
		return nil, err
	}
	nulls := resultNulls(rows)
	displayStoredNulls(rows)
	return &queryResult{
		columns: columns, rows: rows, nulls: nulls,
		metadata: tableMetadata(namespace, tableName, table, selected),
	}, nil
}

func selectedColumns(table catalog.Table, projection string, indexes map[string]int) ([]int, []string, error) {
	if projection == "*" {
		return allColumns(table), append([]string(nil), table.Columns...), nil
	}
	return projectedColumns(table, projection, indexes)
}

func allColumns(table catalog.Table) []int {
	selected := make([]int, len(table.Columns))
	for index := range selected {
		selected[index] = index
	}
	return selected
}

func projectedColumns(table catalog.Table, projection string, indexes map[string]int) ([]int, []string, error) {
	expressions := splitCSV(projection)
	selected := make([]int, 0, len(expressions))
	columns := make([]string, 0, len(expressions))
	for _, expression := range expressions {
		column, valid := singleIdentifier(expression)
		if !valid {
			return nil, nil, sqlFailure{1064, "42000", "unsupported SELECT projection"}
		}
		index, found := indexes[catalog.Key(column)]
		if !found {
			return nil, nil, sqlFailure{1054, "42S22", "unknown column '" + column + "'"}
		}
		selected = append(selected, index)
		columns = append(columns, table.Columns[index])
	}
	return selected, columns, nil
}

func projectRows(source [][]string, selected []int, matches func([]string) bool) [][]string {
	rows := make([][]string, 0, len(source))
	for _, row := range source {
		if !matches(row) {
			continue
		}
		projected := make([]string, len(selected))
		for resultIndex, sourceIndex := range selected {
			projected[resultIndex] = row[sourceIndex]
		}
		rows = append(rows, projected)
	}
	return rows
}

func resultNulls(rows [][]string) [][]bool {
	nulls := make([][]bool, len(rows))
	for rowIndex, row := range rows {
		nulls[rowIndex] = make([]bool, len(row))
		for columnIndex, value := range row {
			nulls[rowIndex][columnIndex] = value == storedSQLNullValue
		}
	}
	return nulls
}

func isSQLNullLiteral(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "null")
}

func isSQLNullSpelling(value string) bool {
	return strings.EqualFold(value, "null")
}

func displayStoredNulls(rows [][]string) {
	for _, row := range rows {
		for columnIndex, value := range row {
			if value == storedSQLNullValue {
				row[columnIndex] = "NULL"
			}
		}
	}
}

func renderSelectedTemporalRows(rows [][]string, selected []int, table catalog.Table, offsetMinutes int) ([][]string, error) {
	for rowIndex, row := range rows {
		for resultIndex, columnIndex := range selected {
			typeName, known := table.ColumnType(columnIndex)
			if !known {
				continue
			}
			typ, err := parseTemporalType(typeName)
			if err != nil || typ.kind != temporalTimestamp || row[resultIndex] == storedSQLNullValue {
				continue
			}
			rendered, err := renderTimestampFixedOffset(row[resultIndex], offsetMinutes, typ.precision)
			if err != nil {
				return nil, err
			}
			rows[rowIndex][resultIndex] = rendered
		}
	}
	return rows, nil
}

func renderStoredTemporalValue(typeName, value string, offsetMinutes int) (string, error) {
	typ, err := parseTemporalType(typeName)
	if err != nil || typ.kind != temporalTimestamp || value == storedSQLNullValue {
		return value, err
	}
	return renderTimestampFixedOffset(value, offsetMinutes, typ.precision)
}

func tableMetadata(namespace, tableName string, table catalog.Table, selected []int) []columnMetadata {
	metadata := make([]columnMetadata, len(selected))
	for resultIndex, columnIndex := range selected {
		name := table.Columns[columnIndex]
		definition := columnMetadata{catalog: "def", schema: namespace, table: tableName, originalTable: tableName, name: name, originalName: name, characterSet: mysqlCharsetUTF8MB40900AICI, typ: mysqlTypeVarString}
		if typeName, known := table.ColumnType(columnIndex); known {
			definition.typ, definition.length, definition.characterSet = catalogColumnWireType(typeName)
			if temporal, err := parseTemporalType(typeName); err == nil && temporal.kind != temporalNone {
				definition.decimals = byte(temporal.precision)
			}
			if strings.HasSuffix(strings.ToUpper(strings.TrimSpace(typeName)), " UNSIGNED") {
				definition.flags |= mysqlUnsignedFlag
			}
		}
		metadata[resultIndex] = definition
	}
	return metadata
}

func catalogColumnWireType(typeName string) (byte, uint32, uint16) {
	if typ, err := parseNumericType(typeName); err == nil && typ.kind != numericNone {
		return numericWireType(typ)
	}
	if typ, err := parseCharacterType(typeName); err == nil && typ.kind != characterNone {
		return typ.wire, characterWireLength(typ), characterWireCharset(typ)
	}
	if typ, err := parseTemporalType(typeName); err == nil && typ.kind != temporalNone {
		return temporalWireType(typ)
	}
	return mysqlTypeVarString, 0, mysqlCharsetUTF8MB40900AICI
}

// characterWireLength reports the result-column display length: a bounded text
// column advertises up to four utf8mb4 bytes per declared character, a bounded
// binary column advertises its declared byte length, and an unbounded family
// advertises zero.
func characterWireLength(typ characterType) uint32 {
	if !typ.bounded {
		return 0
	}
	if typ.kind == characterText {
		return uint32(typ.length) * 4
	}
	return uint32(typ.length)
}

// validateIdentifierLength enforces the fixed identifier ceiling, counted in
// Unicode scalar values of the declared spelling, before any durable effect.
func validateIdentifierLength(name string) error {
	if catalog.IdentifierLength(name) > catalog.IdentifierLimit {
		return sqlFailure{1059, "42000", fmt.Sprintf("Identifier name '%s' is too long", name)}
	}
	return nil
}

// tableTarget resolves an unqualified table against the current namespace and
// a qualified table against its named namespace. Keeping this resolution at
// the protocol seam makes DDL, writes, and reads agree about namespace scope.
func tableTarget(s *relationExecutor, parts []string) (string, string, error) {
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
	if err := (&databaseSelector{s.session}).databaseExists(namespace); err != nil {
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
		case "not", "null", "default", "primary", "unique", "references", "check", "constraint", "auto_increment", "generated", "comment":
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

// parseLiteralResult evaluates a FROM-less SELECT expression for prepared-
// statement column metadata. It shares the scalar expression engine with text
// execution, so a prepared and a text SELECT of the same expression advertise
// identical columns. An expression the engine cannot evaluate returns an
// unsupported result, leaving execution to surface the specific error.
func parseLiteralResult(expression string) literalQueryResult {
	value, isNull, metadata, err := scalarColumn(expression)
	if err != nil {
		return literalQueryResult{}
	}
	return literalQueryResult{value: value, metadata: metadata, isNull: isNull, supported: true}
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
	return columnMetadata{catalog: "def", name: fmt.Sprintf("param%d", index+1), characterSet: mysqlCharsetUTF8MB40900AICI, typ: mysqlTypeVarchar}
}

func (s *preparedPreparation) preparedColumns(query string) ([]columnMetadata, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	lower := strings.ToLower(query)
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "insert into ") && !strings.HasPrefix(lower, "update ") && !strings.HasPrefix(lower, "delete from ") {
		return nil, sqlFailure{1064, "42000", "unsupported prepared statement"}
	}
	if !strings.HasPrefix(lower, "select ") {
		return nil, nil
	}
	parameters := nullPreparedParameters(parameterCount(query))
	validated, err := bindPreparedQuery(query, parameters)
	if err != nil {
		return nil, sqlFailure{1064, "42000", "malformed prepared statement"}
	}
	if len(parameters) > 0 {
		return s.parameterizedColumns(query, validated, len(parameters))
	}
	if literal := parseLiteralResult(strings.TrimSpace(query[len("select "):])); literal.supported {
		return []columnMetadata{literal.metadata}, nil
	}
	return s.queryColumns(validated, true)
}

func (s *preparedPreparation) parameterizedColumns(query, validated string, parameters int) ([]columnMetadata, error) {
	if expression, ok := preparedScalarExpression(query); ok {
		if metadata, ok := preparedScalarMetadata(expression, parameters); ok {
			return metadata, nil
		}
	}
	metadata, err := s.queryColumns(validated, true)
	if err == nil && !hasNullMetadata(metadata) {
		return restorePreparedColumnNames(query, metadata), nil
	}
	if candidate, ok := s.representativeParameterizedColumns(query, parameters); ok {
		return restorePreparedColumnNames(query, candidate), nil
	}
	if err == nil {
		return metadata, nil
	}
	return nil, err
}

func (s *preparedPreparation) representativeParameterizedColumns(query string, parameters int) ([]columnMetadata, bool) {
	for _, values := range representativeParameterValues(parameters) {
		candidate, bindErr := bindPreparedQuery(query, values)
		if bindErr != nil {
			continue
		}
		candidateMetadata, candidateErr := s.queryColumns(candidate, true)
		if candidateErr == nil && !hasNullMetadata(candidateMetadata) {
			return candidateMetadata, true
		}
	}
	return nil, false
}

func representativeParameterValues(parameters int) [][]string {
	replacements := []string{"0", quote(""), "0.0"}
	if parameters > 4 {
		values := make([][]string, 0, len(replacements))
		for _, replacement := range replacements {
			values = append(values, repeatedParameterValue(parameters, replacement))
		}
		return values
	}
	values := make([][]string, 0)
	appendParameterCombinations(&values, make([]string, parameters), replacements, 0)
	return values
}

func appendParameterCombinations(result *[][]string, current, replacements []string, index int) {
	if index == len(current) {
		*result = append(*result, append([]string(nil), current...))
		return
	}
	for _, replacement := range replacements {
		current[index] = replacement
		appendParameterCombinations(result, current, replacements, index+1)
	}
}

func repeatedParameterValue(parameters int, replacement string) []string {
	values := make([]string, parameters)
	for index := range values {
		values[index] = replacement
	}
	return values
}

func restorePreparedColumnNames(query string, metadata []columnMetadata) []columnMetadata {
	expression := strings.TrimSpace(query[len("select "):])
	if from := keywordAt(expression, "from"); from >= 0 {
		expression = strings.TrimSpace(expression[:from])
	}
	_, expression = parseDistinctProjection(expression)
	items := splitCSV(expression)
	if len(items) != len(metadata) {
		return metadata
	}
	for index, item := range items {
		expression, alias, err := splitProjectionAlias(item)
		if err == nil && alias != "" {
			metadata[index].name = alias
		} else {
			metadata[index].name = strings.TrimSpace(expression)
		}
	}
	return metadata
}

func hasNullMetadata(metadata []columnMetadata) bool {
	for _, column := range metadata {
		if column.typ == mysqlTypeNull {
			return true
		}
	}
	return false
}

func preparedScalarExpression(query string) (string, bool) {
	expression := strings.TrimSpace(query[len("select "):])
	return expression, keywordAt(expression, "from") < 0
}

// preparedScalarMetadata evaluates a FROM-less expression with representative
// parameter domains. Preparing with NULL makes every NULL-propagating function
// advertise NULL metadata, even though the bound execution will return its
// actual result type. String and numeric representatives cover the strict
// function families without allowing prepare-time metadata to depend on a
// particular bound value.
func preparedScalarMetadata(expression string, parameters int) ([]columnMetadata, bool) {
	for _, replacement := range []string{quote(""), "0", "0.0"} {
		values := make([]string, parameters)
		for index := range values {
			values[index] = replacement
		}
		bound, err := bindPreparedQuery(expression, values)
		if err != nil {
			continue
		}
		_, _, metadata, err := scalarColumn(bound)
		if err != nil {
			continue
		}
		metadata.name = expression
		return []columnMetadata{metadata}, true
	}
	return nil, false
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
		metadata[index] = columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB40900AICI, typ: mysqlTypeVarString}
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
	result, err := queries.executeProtocol(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	if len(result.columns) == 0 {
		return writePacket(connection, sequence, okPacket(result.affected))
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
		if isPreparedTemporalType(types[index].typ) {
			value = preparedTemporalLiteral(types[index].typ, scalar(value))
		}
		values[index], offset = value, next
	}
	return values, offset, nil
}

func isPreparedTemporalType(wire byte) bool {
	switch wire {
	case mysqlTypeDate, mysqlTypeDatetime, mysqlTypeTimestamp, mysqlTypeTime:
		return true
	default:
		return false
	}
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
	mysqlTypeYear: preparedIntegerReader(2),
	mysqlTypeDate: readPreparedTemporal, mysqlTypeDatetime: readPreparedTemporal, mysqlTypeTimestamp: readPreparedTemporal, mysqlTypeTime: readPreparedTemporal,
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
	s.autocommitOff = false
	s.isolation = isolationRepeatableRead
	s.readOnly = false
	s.nextIsolation = isolationRepeatableRead
	s.nextReadOnly = false
	s.database = s.initialDB
	s.timeZone = s.initialTimeZone
	s.closeAllPrepared()
	s.longDataBytes = 0
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

func okPacket(affected ...uint64) []byte {
	count := uint64(0)
	if len(affected) > 0 {
		count = affected[0]
	}
	payload := []byte{0x00}
	payload = append(payload, lengthEncodedUint(count)...)
	payload = append(payload, 0x00, 0x02, 0x00, 0x00, 0x00)
	return payload
}

func errorPacket(code uint16, state, message string) []byte {
	if len(message) > 255 {
		message = message[:252] + "..."
	}
	payload := []byte{0xff, byte(code), byte(code >> 8), '#'}
	payload = append(payload, state...)
	payload = append(payload, message...)
	return payload
}

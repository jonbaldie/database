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
	"github.com/jonbaldie/database/internal/instance"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

// maximumPendingConnections is a private handshake-resource safeguard. It is
// intentionally not part of the server configuration registry or public
// compatibility profile; MaxConnections remains the session ceiling.
const (
	maximumPendingConnections = 128
	clientLongPassword        = 1
	clientFoundRows           = 1 << 1
	clientLongFlag            = 1 << 2
	clientConnectWithDB       = 1 << 3
	clientIgnoreSpace         = 1 << 6
	clientODBC                = 1 << 8
	clientInteractive         = 1 << 10
	clientIgnoreSigpipe       = 1 << 12
	clientReserved            = 1 << 14
	clientLocalFiles          = 1 << 7
	clientProtocol41          = 1 << 9
	clientTransactions        = 1 << 13
	clientSecureConnection    = 1 << 15
	clientMultiStatements     = 1 << 16
	clientMultiResults        = 1 << 17
	clientPSMultiResults      = 1 << 18
	clientCanHandleExpiredPwd = 1 << 22
	clientSessionTrack        = 1 << 23
	clientDeprecateEOF        = 1 << 24
	clientOptionalMetadata    = 1 << 25
	clientQueryAttributes     = 1 << 27
	clientMultiFactorAuth     = 1 << 28
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
	Catalog      *catalog.Store
	Username     string
	PasswordHash string
	// Instance is the durable instance identity copied into online backups.
	Instance             instance.Metadata
	Version              string
	TLSCertFile          string
	TLSKeyFile           string
	MaxPreparedStmtCount int
	MaxConnections       int
	MaxAllowedPacket     int64
	// LockWaitTimeout bounds the time a statement waits for a conflicting row
	// lock. It defaults to five seconds.
	LockWaitTimeout time.Duration
	ResourceLimits  ResourceLimits
	// TimeZone is the fixed-offset session time zone that TIMESTAMP instants and
	// current-time functions render through. It defaults to UTC and accepts UTC
	// or a ±HH:MM offset within ±14:00.
	TimeZone string
	// Clock supplies the current instant for current-time functions. It defaults
	// to time.Now and is injectable so rendering is reproducible under test.
	Clock func() time.Time
}

// ResourceLimits is the server resource policy that applies to every session.
// Later session settings may tighten its statement-scoped values, but they may
// never enlarge or disable these server ceilings.
type ResourceLimits struct {
	StatementTimeout                    time.Duration
	ExecutionMemoryLimitBytes           int64
	AggregateExecutionMemoryLimitBytes  int64
	TemporaryStorageLimitBytes          int64
	AggregateTemporaryStorageLimitBytes int64
}

type Server struct {
	Listener            net.Listener
	config              Config
	connections         *connectionRegistry
	auth                authenticator
	locks               *lockManager
	resources           *resourceManager
	explanations        *activeExplanationRegistry
	Diagnostics         ResourceDiagnostics
	shutdown            chan struct{}
	shutdownOnce        sync.Once
	shutdownOperationID string
}

// ResourceDiagnostics provides the non-sensitive server evidence that the
// lifecycle diagnostics listener publishes.
type ResourceDiagnostics struct{ manager *resourceManager }

func (d ResourceDiagnostics) Usage() ResourceUsage {
	return d.manager.usage()
}

// connectionRegistry owns admission and graceful-drain accounting. It is kept
// separate from wire handling so transport lifecycle can evolve independently
// of command and SQL compatibility work.
type connectionRegistry struct {
	mu               sync.Mutex
	stopping         bool
	connections      map[net.Conn]struct{}
	sessions         map[string]map[*conversation]struct{}
	connectionW      sync.WaitGroup
	statementW       sync.WaitGroup
	pendingMax       int
	pendingCount     int
	sessionMax       int
	sessionCount     int
	preparedCount    int
	preparedLimit    int
	nextConnectionID uint32
}

// New retains a small unauthenticated protocol probe seam for callers that do
// not attach an initialized instance. A serving database uses NewWithConfig.
func New(address string) (*Server, error) {
	return NewWithConfig(address, Config{Version: "0.2.0-dev"})
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
		sessions:      make(map[string]map[*conversation]struct{}),
		pendingMax:    maximumPendingConnections,
		sessionMax:    config.MaxConnections,
		preparedLimit: config.MaxPreparedStmtCount,
	}
	resources := newResourceManager(config)
	if config.Catalog != nil {
		grants := []catalog.Grant{{Privilege: "ACCOUNT_MANAGER"}, {Privilege: "NAMESPACE_MANAGER"}, {Privilege: "OPERATIONAL_OBSERVATION"}, {Privilege: "OPERATIONAL_CONTROL"}}
		if err := config.Catalog.EnsureAccount(config.Username, config.PasswordHash, grants); err != nil {
			_ = listener.Close()
			return nil, err
		}
		config.Catalog.SetPublishValidator(validateConstraintDefinition)
	}
	return &Server{
		Listener: listener, config: config, connections: registry, auth: auth, locks: newLockManager(config.LockWaitTimeout),
		resources: resources, explanations: newActiveExplanationRegistry(), Diagnostics: ResourceDiagnostics{manager: resources},
		shutdown: make(chan struct{}),
	}, nil
}

func normalizedConfig(config Config) Config {
	if config.Version == "" {
		config.Version = "0.2.0-dev"
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
	if config.LockWaitTimeout <= 0 {
		config.LockWaitTimeout = 5 * time.Second
	}
	config.ResourceLimits = normalizedResourceLimits(config.ResourceLimits)
	if config.TimeZone == "" {
		config.TimeZone = "UTC"
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return config
}

func normalizedResourceLimits(limits ResourceLimits) ResourceLimits {
	if limits.StatementTimeout <= 0 {
		limits.StatementTimeout = 5 * time.Minute
	}
	if limits.ExecutionMemoryLimitBytes <= 0 {
		limits.ExecutionMemoryLimitBytes = 64 * 1024 * 1024
	}
	if limits.AggregateExecutionMemoryLimitBytes <= 0 {
		limits.AggregateExecutionMemoryLimitBytes = 2 * 1024 * 1024 * 1024
	}
	if limits.TemporaryStorageLimitBytes <= 0 {
		limits.TemporaryStorageLimitBytes = 16 * 1024 * 1024 * 1024
	}
	if limits.AggregateTemporaryStorageLimitBytes <= 0 {
		limits.AggregateTemporaryStorageLimitBytes = 32 * 1024 * 1024 * 1024
	}
	return limits
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

func (r *connectionRegistry) registerConversation(conv *conversation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[conv.session.username] == nil {
		r.sessions[conv.session.username] = map[*conversation]struct{}{}
	}
	r.sessions[conv.session.username][conv] = struct{}{}
}

func (r *connectionRegistry) unregisterConversation(conv *conversation) {
	if conv.session == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := r.sessions[conv.session.username]
	delete(sessions, conv)
	if len(sessions) == 0 {
		delete(r.sessions, conv.session.username)
	}
}

func (r *connectionRegistry) revokeAccount(name string) {
	r.mu.Lock()
	sessions := make([]*conversation, 0, len(r.sessions[name]))
	for conversation := range r.sessions[name] {
		sessions = append(sessions, conversation)
	}
	r.mu.Unlock()
	for _, conversation := range sessions {
		conversation.control.revoked.Store(true)
		if !conversation.control.running.Load() {
			_ = conversation.connection.Close()
		}
	}
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

func (r *connectionRegistry) allocateConnectionID() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextConnectionID++
	if r.nextConnectionID == 0 {
		r.nextConnectionID++
	}
	return r.nextConnectionID
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
	connectionID    uint32
	username        string
	database        string
	initialDB       string
	timeZone        string
	initialTimeZone string
	settings        sessionSettings
	statements      map[uint32]*preparedStatement
	prepared        preparedCounters
	statementCancel <-chan struct{}
	resources       *statementResources
	runtimeMetrics  *queryexplanation.RuntimeMetrics
	transactionState
}

type transactionState struct {
	transactionSettings
	transactionWork
	savepointState
	statementState
}

type sessionSettings struct {
	collationConnection                         collationKind
	statementTimeout, lockWaitTimeout           time.Duration
	executionMemoryLimit, temporaryStorageLimit int64
}

type preparedCounters struct {
	nextStmtID    uint32
	longDataBytes int
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
	savepoints []savepoint
}

type savepoint struct {
	name          string
	snapshot      catalog.Definition
	revision      uint64
	dirty         bool
	mutationCount int
	read          bool
}

type statementState struct {
	statementDefinition    catalog.Definition
	statementDefinitionSet bool
}

type preparedStatement struct {
	query       string
	parameters  int
	types       []preparedParameterType
	longData    map[uint16][]byte
	explanation *queryexplanation.Document
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
	composed   *composedQueryContext
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
	// Connector/J carries the seed through an ASCII String internally. Keep
	// the random seed printable so that the driver preserves the exact bytes
	// when it computes the caching_sha2_password scramble.
	for index := range nonce {
		var byteValue [1]byte
		for {
			if _, err := rand.Read(byteValue[:]); err != nil {
				return []byte("database-authentication")
			}
			if byteValue[0] >= 33 && byteValue[0] <= 126 {
				nonce[index] = byteValue[0]
				break
			}
		}
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
	return serverCapabilities(tlsEnabled) |
		clientFoundRows | clientLongFlag | clientConnectWithDB | clientIgnoreSpace | clientODBC | clientInteractive |
		clientIgnoreSigpipe | clientReserved | clientLocalFiles | clientMultiStatements | clientMultiResults | clientPSMultiResults |
		clientConnectAttrs | clientCanHandleExpiredPwd | clientSessionTrack |
		clientDeprecateEOF | clientOptionalMetadata | clientQueryAttributes | clientMultiFactorAuth
}

func handshake(version string, nonce []byte, tlsEnabled bool, connectionID uint32) []byte {
	capabilities := serverCapabilities(tlsEnabled)
	p := []byte{0x0a}
	// Drivers parse the handshake version as a MySQL semantic version. Keep
	// the product identity as a suffix so strict clients (for example
	// Connector/Python) accept the connection while VERSION() remains honest.
	p = append(p, []byte("8.4.11-database-"+version)...)
	p = append(p, 0)
	p = append(p, byte(connectionID), byte(connectionID>>8), byte(connectionID>>16), byte(connectionID>>24))
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
	coercibility                                              byte
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
	{name: "statistics", columns: []informationSchemaColumn{
		{name: "TABLE_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
		{name: "NON_UNIQUE", typeName: "INT"},
		{name: "INDEX_NAME", typeName: "VARCHAR(64)"},
		{name: "SEQ_IN_INDEX", typeName: "INT"},
		{name: "COLUMN_NAME", typeName: "VARCHAR(64)"},
		{name: "COLLATION", typeName: "VARCHAR(1)"},
		{name: "SUB_PART", typeName: "INT"},
		{name: "NULLABLE", typeName: "VARCHAR(3)"},
		{name: "INDEX_TYPE", typeName: "VARCHAR(16)"},
		{name: "COMMENT", typeName: "VARCHAR(16)"},
		{name: "INDEX_COMMENT", typeName: "VARCHAR(2048)"},
		{name: "VISIBLE", typeName: "VARCHAR(3)"},
		{name: "EXPRESSION", typeName: "VARCHAR(64)"},
	}},
	{name: "table_constraints", columns: []informationSchemaColumn{
		{name: "CONSTRAINT_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "CONSTRAINT_NAME", typeName: "VARCHAR(64)"},
		{name: "TABLE_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
		{name: "CONSTRAINT_TYPE", typeName: "VARCHAR(64)"},
	}},
	{name: "key_column_usage", columns: []informationSchemaColumn{
		{name: "CONSTRAINT_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "CONSTRAINT_NAME", typeName: "VARCHAR(64)"},
		{name: "TABLE_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
		{name: "COLUMN_NAME", typeName: "VARCHAR(64)"},
		{name: "ORDINAL_POSITION", typeName: "INT"},
		{name: "REFERENCED_TABLE_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "REFERENCED_TABLE_NAME", typeName: "VARCHAR(64)"},
		{name: "REFERENCED_COLUMN_NAME", typeName: "VARCHAR(64)"},
	}},
	{name: "referential_constraints", columns: []informationSchemaColumn{
		{name: "CONSTRAINT_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "CONSTRAINT_NAME", typeName: "VARCHAR(64)"},
		{name: "TABLE_NAME", typeName: "VARCHAR(64)"},
		{name: "REFERENCED_TABLE_NAME", typeName: "VARCHAR(64)"},
	}},
	{name: "check_constraints", columns: []informationSchemaColumn{
		{name: "CONSTRAINT_SCHEMA", typeName: "VARCHAR(64)"},
		{name: "CONSTRAINT_NAME", typeName: "VARCHAR(64)"},
		{name: "CHECK_CLAUSE", typeName: "VARCHAR(2048)"},
	}},
	{name: "character_sets", columns: []informationSchemaColumn{
		{name: "CHARACTER_SET_NAME", typeName: "VARCHAR(64)"},
		{name: "DEFAULT_COLLATE_NAME", typeName: "VARCHAR(64)"},
		{name: "DESCRIPTION", typeName: "VARCHAR(64)"},
		{name: "MAXLEN", typeName: "INT"},
	}},
	{name: "collations", columns: []informationSchemaColumn{
		{name: "COLLATION_NAME", typeName: "VARCHAR(64)"},
		{name: "CHARACTER_SET_NAME", typeName: "VARCHAR(64)"},
		{name: "IS_DEFAULT", typeName: "VARCHAR(3)"},
	}},
	{name: "accounts", columns: []informationSchemaColumn{
		{name: "USER", typeName: "VARCHAR(32)"},
		{name: "LOCKED", typeName: "INT"},
	}},
	{name: "account_grants", columns: []informationSchemaColumn{
		{name: "USER", typeName: "VARCHAR(32)"},
		{name: "PRIVILEGE", typeName: "VARCHAR(64)"},
		{name: "NAMESPACE", typeName: "VARCHAR(64)"},
	}},
	{name: "processlist", columns: []informationSchemaColumn{
		{name: "ID", typeName: "BIGINT"},
		{name: "USER", typeName: "VARCHAR(32)"},
		{name: "HOST", typeName: "VARCHAR(64)"},
		{name: "DB", typeName: "VARCHAR(64)"},
		{name: "COMMAND", typeName: "VARCHAR(16)"},
		{name: "TIME", typeName: "INT"},
		{name: "STATE", typeName: "VARCHAR(64)"},
		{name: "INFO", typeName: "VARCHAR(65535)"},
	}},
}

func (s *queryExecutor) writeQueryResult(connection net.Conn, sequence byte, query string) error {
	statement, err := normalizeStatement(query)
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	executor := s.statements
	executor.streamRows = true
	result, err := newStatementExecutionPolicy(&executor).execute(statement)
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

func (s *queryExecutor) useDatabase(name string) { s.statements.useDatabase(name) }

func (s *queryExecutor) databaseExists(name string) error {
	selector := databaseSelector{s.statements.session}
	return selector.databaseExists(name)
}

// stripLeadingSQLComments removes comments that clients use to annotate an
// otherwise ordinary statement. Connector/J prefixes its server-variable
// probe with a block comment containing its version. Only comments before the
// first statement keyword are removed.
func stripLeadingSQLComments(query string) string {
	for {
		query = strings.TrimSpace(query)
		if !strings.HasPrefix(query, "/*") {
			return query
		}
		end := strings.Index(query[2:], "*/")
		if end < 0 {
			return query
		}
		query = query[end+4:]
	}
}

type statementHandler func(query, lower string) (*queryResult, bool, error)

func (s *textStatementExecutor) statementHandlers() []statementHandler {
	return []statementHandler{
		s.transactionStatement,
		s.settingStatement,
		s.accountStatement,
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
	if strings.HasPrefix(lower, "show ") {
		return s.showVariables(lower)
	}
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
	if strings.HasPrefix(lower, "select @@") {
		variable := strings.TrimSpace(lower[len("select "):])
		if strings.Contains(variable, ",") || strings.Contains(variable, " ") {
			return nil, false, nil
		}
		result, err := s.sessionVariableResult(variable)
		return result, true, err
	}
	result, found := map[string]*queryResult{
		"select version()":  {columns: []string{"VERSION()"}, rows: [][]string{{"8.4.11-database-" + s.server.config.Version}}},
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

func (s *textStatementExecutor) transactionStatement(query, lower string) (*queryResult, bool, error) {
	handler, handled := findTransactionHandler(lower)
	if !handled {
		return nil, false, nil
	}
	return nil, true, handler(s.session, query)
}

func (s *textStatementExecutor) catalogStatement(query, lower string) (*queryResult, bool, error) {
	catalogQueries := catalogExecutor{s.session}
	if showGrantsStatement(lower) {
		result, err := catalogQueries.showGrants(query)
		return result, true, err
	}
	if result, handled, err := showCatalog(&catalogQueries, query, lower); handled {
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

func showCatalog(s *catalogExecutor, query, lower string) (*queryResult, bool, error) {
	switch {
	case lower == "show databases" || strings.HasPrefix(lower, "show databases like "):
		return filterShowLike(s.showDatabases(), query, lower), true, nil
	case strings.HasPrefix(lower, "show create database ") || strings.HasPrefix(lower, "show create schema "):
		result, err := s.showCreateDatabase(query)
		return result, true, err
	case strings.HasPrefix(lower, "show create table "):
		result, err := s.showCreateTable(query)
		return result, true, err
	case showIndexTarget(query) != "":
		result, err := s.showIndexes(query)
		return result, true, err
	case lower == "show tables" || strings.HasPrefix(lower, "show tables like "):
		result, err := s.showTables()
		return filterShowLike(result, query, lower), true, err
	default:
		return nil, false, nil
	}
}

func filterShowLike(result *queryResult, query, lower string) *queryResult {
	if result == nil {
		return result
	}
	pattern, ok := showLikePattern(query, lower)
	if !ok {
		return result
	}
	rows := make([][]string, 0, len(result.rows))
	for _, row := range result.rows {
		if len(row) > 0 && mysqlLike(row[0], pattern) {
			rows = append(rows, row)
		}
	}
	result.rows = rows
	return result
}

func showLikePattern(query, lower string) (string, bool) {
	index := strings.Index(lower, " like ")
	if index < 0 {
		return "", false
	}
	return scalar(strings.TrimSpace(query[index+len(" like "):])), true
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
		return nil, metadataNamespaceFailure(s.server.config.Catalog, s.database)
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
	case strings.HasPrefix(lower, "replace "):
		affected, err := replaceRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case strings.HasPrefix(lower, "update "):
		affected, err := updateRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case strings.HasPrefix(lower, "delete from "):
		affected, err := deleteRows(&relations, query)
		return &queryResult{affected: affected}, true, err
	case isComposedSelectStatement(query):
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
	return visibleCatalogDefinition(s.server.config.Catalog.Snapshot(), s.session.username)
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
		return grantCreatedNamespace(definition, name, s.username)
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
	tableDefinition, err := builtCatalogTable(name, table)
	if err != nil {
		return err
	}
	namespaceDefinition.Tables[key] = tableDefinition
	definition.Namespaces[catalog.Key(namespace)] = namespaceDefinition
	return nil
}

func builtCatalogTable(name string, table tableDefinition) (catalog.Table, error) {
	indexes, err := namedTableIndexes(name, table.indexes, table.constraints)
	if err != nil {
		return catalog.Table{}, err
	}
	tableDefinition := catalog.Table{Name: name, Columns: append([]string(nil), table.columns...), ColumnAttributes: append([]catalog.ColumnAttribute(nil), table.attributes...), Constraints: catalog.CloneConstraints(table.constraints), Indexes: indexes}
	if len(table.types) > 0 {
		tableDefinition.ColumnTypes = append([]string(nil), table.types...)
	}
	if err := applyPrimaryColumnRules(&tableDefinition); err != nil {
		return catalog.Table{}, err
	}
	if err := canonicalizeTableDefaults(&tableDefinition); err != nil {
		return catalog.Table{}, err
	}
	if err := validateTableIndexes(tableDefinition); err != nil {
		return catalog.Table{}, err
	}
	return tableDefinition, nil
}

func applyPrimaryColumnRules(table *catalog.Table) error {
	ensureColumnAttributes(table)
	indexes, err := tableColumnIndexes(*table)
	if err != nil {
		return err
	}
	for _, constraint := range table.Constraints {
		if constraint.Type != catalog.ConstraintTypePrimary {
			continue
		}
		for _, column := range constraint.Columns {
			index, found := indexes[catalog.Key(column)]
			if found {
				table.ColumnAttributes[index].Nullable = false
			}
		}
	}
	return nil
}

func canonicalizeTableDefaults(table *catalog.Table) error {
	for index, attribute := range table.ColumnAttributes {
		if !attribute.HasDefault {
			continue
		}
		canonical, err := canonicalColumnValue(*table, index, attribute.Default, 1)
		if err != nil {
			return err
		}
		table.ColumnAttributes[index].Default = canonical
	}
	return nil
}

type tableDefinition struct {
	target      []string
	columns     []string
	types       []string
	attributes  []catalog.ColumnAttribute
	constraints []catalog.Constraint
	indexes     []catalog.Index
	ifNotExists bool
}

type parsedTableColumns struct {
	columns     []string
	types       []string
	attributes  []catalog.ColumnAttribute
	constraints []catalog.Constraint
	indexes     []catalog.Index
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
	target, err := createTableTarget(head)
	if err != nil {
		return tableDefinition{}, err
	}
	columns, err := parseTableColumns(body)
	if err != nil {
		return tableDefinition{}, err
	}
	if err := validateTableColumns(columns.columns); err != nil {
		return tableDefinition{}, err
	}
	constraints, err := namedTableConstraints(target[len(target)-1], columns.constraints)
	if err != nil {
		return tableDefinition{}, err
	}
	return tableDefinition{target: target, columns: columns.columns, types: columns.types, attributes: columns.attributes, constraints: constraints, indexes: columns.indexes, ifNotExists: ifNotExists}, nil
}

func createTableTarget(head string) ([]string, error) {
	target, ok := splitQualifiedIdentifier(head)
	if !ok || len(target) == 0 || len(target) > 2 {
		return nil, sqlFailure{1064, "42000", "invalid table name"}
	}
	for _, part := range target {
		if err := validateIdentifierLength(part); err != nil {
			return nil, err
		}
	}
	return target, nil
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

func parseTableColumns(body string) (parsedTableColumns, error) {
	parts := splitCSV(body)
	parsed := parsedTableColumns{
		columns:     make([]string, 0, len(parts)),
		types:       make([]string, 0, len(parts)),
		attributes:  make([]catalog.ColumnAttribute, 0, len(parts)),
		constraints: make([]catalog.Constraint, 0, len(parts)),
		indexes:     make([]catalog.Index, 0, len(parts)),
	}
	for _, part := range parts {
		if isTableIndexDefinition(part) {
			index, err := parseTableIndexDefinition(part)
			if err != nil {
				return parsedTableColumns{}, err
			}
			parsed.indexes = append(parsed.indexes, index)
			continue
		}
		if isTableConstraintDefinition(part) {
			constraint, err := parseTableConstraint(part)
			if err != nil {
				return parsedTableColumns{}, err
			}
			parsed.constraints = append(parsed.constraints, constraint)
			continue
		}
		column, typeName, attribute, columnConstraints, err := parseTableColumn(part)
		if err != nil {
			return parsedTableColumns{}, err
		}
		parsed.columns = append(parsed.columns, column)
		parsed.types = append(parsed.types, typeName)
		parsed.attributes = append(parsed.attributes, attribute)
		parsed.constraints = append(parsed.constraints, columnConstraints...)
	}
	return parsed, nil
}

func parseTableColumn(part string) (string, string, catalog.ColumnAttribute, []catalog.Constraint, error) {
	column, remainder, valid := consumeIdentifier(part)
	if !valid {
		return "", "", catalog.ColumnAttribute{}, nil, sqlFailure{1064, "42000", "invalid column definition"}
	}
	if err := validateIdentifierLength(column); err != nil {
		return "", "", catalog.ColumnAttribute{}, nil, err
	}
	typePart, modifiers := splitColumnTypeAndModifiers(remainder)
	fields := strings.Fields(typePart)
	if len(fields) == 0 {
		if strings.TrimSpace(modifiers) != "" {
			return "", "", catalog.ColumnAttribute{}, nil, sqlFailure{1064, "42000", "column type is required"}
		}
		return column, "", catalog.ColumnAttribute{Nullable: true}, nil, nil
	}
	typeName, err := columnTypeName(fields)
	if err != nil {
		return "", "", catalog.ColumnAttribute{}, nil, err
	}
	attribute, constraints, err := parseColumnModifiers(column, modifiers)
	if err != nil {
		return "", "", catalog.ColumnAttribute{}, nil, err
	}
	return column, typeName, attribute, constraints, nil
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
		return nil, metadataNamespaceFailure(s.server.config.Catalog, name)
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
		return nil, metadataNamespaceFailure(s.server.config.Catalog, namespaceName)
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

func (s *catalogExecutor) showIndexes(query string) (*queryResult, error) {
	target := showIndexTarget(query)
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) > 2 {
		return nil, sqlFailure{1064, "42000", "invalid table name"}
	}
	namespaceName, tableName, err := s.qualifiedShowTableTarget(target, parts)
	if err != nil {
		return nil, err
	}
	namespace, ok := s.metadataDefinition().Namespaces[catalog.Key(namespaceName)]
	if !ok {
		return nil, metadataNamespaceFailure(s.server.config.Catalog, namespaceName)
	}
	table, ok := namespace.Tables[catalog.Key(tableName)]
	if !ok {
		return nil, sqlFailure{1146, "42S02", "table '" + namespaceName + "." + tableName + "' doesn't exist"}
	}
	if table.Name == "" {
		table.Name = strings.ToLower(tableName)
	}
	return showTableIndexes(table), nil
}

func showIndexTarget(query string) string {
	value := strings.TrimSpace(query)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"show index from ", "show index in ", "show indexes from ", "show indexes in ", "show keys from ", "show keys in "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return ""
}

func showTableIndexes(table catalog.Table) *queryResult {
	columns := []string{"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name", "Collation", "Cardinality", "Sub_part", "Packed", "Null", "Index_type", "Comment", "Index_comment", "Visible", "Expression"}
	rows := [][]string{}
	nulls := [][]bool{}
	for _, index := range effectiveTableIndexes(table) {
		for number, part := range index.Parts {
			row, null := showIndexRow(table, index, part, number)
			rows = append(rows, row)
			nulls = append(nulls, null)
		}
	}
	return &queryResult{columns: columns, rows: rows, nulls: nulls}
}

func showIndexRow(table catalog.Table, index catalog.Index, part catalog.IndexPart, number int) ([]string, []bool) {
	column, expression := part.Column, part.Expression
	nullable := false
	if column != "" {
		columnIndex := tableColumnIndex(table.Columns, column)
		nullable = columnIndex >= 0 && catalog.ColumnAttributeAt(table, columnIndex).Nullable
	}
	null := []bool{false, false, false, false, column == "", false, false, part.PrefixLength == 0, true, !nullable, false, false, false, false, expression == ""}
	return []string{
		table.Name,
		strconv.Itoa(boolToInt(!index.Unique)),
		index.Name,
		strconv.Itoa(number + 1),
		column,
		indexPartCollation(part),
		strconv.Itoa(len(table.Rows)),
		strconv.Itoa(part.PrefixLength),
		"",
		"YES",
		"BTREE",
		"",
		index.Comment,
		indexVisibility(index),
		expression,
	}, null
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func indexPartCollation(part catalog.IndexPart) string {
	if part.Descending {
		return "D"
	}
	return "A"
}

func indexVisibility(index catalog.Index) string {
	if index.Invisible {
		return "NO"
	}
	return "YES"
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
		attribute := catalog.ColumnAttributeAt(table, index)
		if !attribute.Nullable {
			definition.WriteString(" NOT NULL")
		}
		if attribute.HasDefault {
			definition.WriteString(" DEFAULT ")
			definition.WriteString(canonicalDefaultValue(columnType, attribute.Default))
		}
	}
	for _, constraint := range table.Constraints {
		definition.WriteString(",\n  ")
		definition.WriteString(canonicalConstraintDefinition(constraint))
	}
	for _, index := range table.Indexes {
		definition.WriteString(",\n  ")
		definition.WriteString(canonicalIndexDefinition(index))
	}
	definition.WriteString("\n)")
	return definition.String(), nil
}

func canonicalIndexDefinition(index catalog.Index) string {
	prefix := "INDEX "
	if index.Unique {
		prefix = "UNIQUE INDEX "
	}
	definition := prefix + quoteIdentifier(index.Name) + " " + canonicalIndexParts(index.Parts)
	if index.Invisible {
		definition += " INVISIBLE"
	}
	if index.Comment != "" {
		definition += " COMMENT '" + strings.ReplaceAll(index.Comment, "'", "''") + "'"
	}
	return definition
}

func canonicalIndexParts(parts []catalog.IndexPart) string {
	values := make([]string, len(parts))
	for number, part := range parts {
		value := quoteIdentifier(part.Column)
		if part.Expression != "" {
			value = "(" + part.Expression + ")"
		}
		if part.PrefixLength > 0 {
			value += "(" + strconv.Itoa(part.PrefixLength) + ")"
		}
		if part.Descending {
			value += " DESC"
		}
		values[number] = value
	}
	return "(" + strings.Join(values, ", ") + ")"
}

func canonicalDefaultValue(typeName, value string) string {
	if value == storedSQLNullValue {
		return "NULL"
	}
	if character, err := parseCharacterType(typeName); err == nil && character.kind == characterText {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return value
}

func canonicalConstraintDefinition(constraint catalog.Constraint) string {
	columns := canonicalConstraintColumns(constraint.Columns)
	switch constraint.Type {
	case catalog.ConstraintTypePrimary:
		return "PRIMARY KEY " + columns
	case catalog.ConstraintTypeUnique:
		return "CONSTRAINT " + quoteIdentifier(constraint.Name) + " UNIQUE " + columns
	case catalog.ConstraintTypeForeignKey:
		target := quoteIdentifier(constraint.ReferencedTable)
		if constraint.ReferencedNamespace != "" {
			target = quoteIdentifier(constraint.ReferencedNamespace) + "." + target
		}
		return "CONSTRAINT " + quoteIdentifier(constraint.Name) + " FOREIGN KEY " + columns + " REFERENCES " + target + " " + canonicalConstraintColumns(constraint.ReferencedColumns)
	case catalog.ConstraintTypeCheck:
		return "CONSTRAINT " + quoteIdentifier(constraint.Name) + " CHECK (" + constraint.Check + ")"
	default:
		return "CONSTRAINT " + quoteIdentifier(constraint.Name)
	}
}

func canonicalConstraintColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = quoteIdentifier(column)
	}
	return "(" + strings.Join(quoted, ", ") + ")"
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
	previousLength := len(table.Rows)
	previousIndex := table.PrimaryIndex
	rows, err := transform(table)
	if err != nil {
		return err
	}
	table.Rows = rows
	catalog.MaintainPrimaryIndex(&table, previousLength, previousIndex)
	namespace.Tables[catalog.Key(tableName)] = table
	definition.Namespaces[catalog.Key(namespaceName)] = namespace
	return nil
}

func insertRows(s *relationExecutor, query string) (uint64, error) {
	input, assignments, upsert, err := splitInsertOnDuplicate(query)
	if err != nil {
		return 0, err
	}
	if upsert {
		return upsertRows(s, input, assignments)
	}
	plan, err := makeInsertionPlan(s, input)
	if err != nil {
		return 0, err
	}
	var affected uint64
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.namespace, plan.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.table = table
			rows, count, err := applyInsertPlan(currentPlan)
			affected = count
			return rows, err
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

// splitInsertOnDuplicate keeps the INSERT value input separate from the
// duplicate-key update list. The keyword scanner ignores quoted text and row
// groups, so an ordinary value that contains the phrase stays an ordinary
// value.
func splitInsertOnDuplicate(query string) (string, string, bool, error) {
	position := keywordAt(query, "on duplicate key update")
	if position < 0 {
		return query, "", false, nil
	}
	input, assignments := strings.TrimSpace(query[:position]), strings.TrimSpace(query[position+len("on duplicate key update"):])
	if input == "" || assignments == "" {
		return "", "", false, sqlFailure{1064, "42000", "malformed INSERT ... ON DUPLICATE KEY UPDATE"}
	}
	return input, assignments, true, nil
}

func upsertRows(s *relationExecutor, query, assignments string) (uint64, error) {
	plan, err := makeUpsertPlan(s, query, assignments)
	if err != nil {
		return 0, err
	}
	_, affected, err := applyUpsertPlan(plan)
	if err != nil {
		return 0, err
	}
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.insert.namespace, plan.insert.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.insert.table = table
			rows, _, err := applyUpsertPlan(currentPlan)
			return rows, err
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

type upsertPlan struct {
	insert  insertPlan
	updates []upsertAssignment
}

type upsertAssignment struct {
	column        int
	value         string
	candidateFrom *int
}

func makeUpsertPlan(s *relationExecutor, query, assignments string) (upsertPlan, error) {
	insert, err := makeInsertionPlan(s, query)
	if err != nil {
		return upsertPlan{}, err
	}
	indexes, err := tableColumnIndexes(insert.table)
	if err != nil {
		return upsertPlan{}, err
	}
	updates, err := parseUpsertAssignments(assignments, indexes)
	if err != nil {
		return upsertPlan{}, err
	}
	return upsertPlan{insert: insert, updates: updates}, nil
}

func parseUpsertAssignments(text string, indexes map[string]int) ([]upsertAssignment, error) {
	updates := make([]upsertAssignment, 0, len(splitCSV(text)))
	seen := make(map[int]bool)
	for _, assignment := range splitCSV(text) {
		column, raw, ok := splitEquals(assignment)
		if !ok {
			return nil, sqlFailure{1064, "42000", "malformed ON DUPLICATE KEY UPDATE assignment"}
		}
		column, ok = singleIdentifier(column)
		if !ok {
			return nil, sqlFailure{1064, "42000", "invalid ON DUPLICATE KEY UPDATE column"}
		}
		index, found := indexes[catalog.Key(column)]
		if !found || seen[index] {
			return nil, sqlFailure{1054, "42S22", "unknown or duplicate column '" + column + "'"}
		}
		candidate, valuesReference, err := upsertValuesReference(raw, indexes)
		if err != nil {
			return nil, err
		}
		update := upsertAssignment{column: index}
		if valuesReference {
			update.candidateFrom = &candidate
		} else {
			if !validUpsertLiteral(raw) {
				return nil, sqlFailure{1235, "42000", "unsupported ON DUPLICATE KEY UPDATE expression"}
			}
			update.value = strings.TrimSpace(raw)
		}
		seen[index] = true
		updates = append(updates, update)
	}
	return updates, nil
}

func validUpsertLiteral(raw string) bool {
	if validDefaultLiteral(raw) {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	if !(strings.HasPrefix(value, "b'") && strings.HasSuffix(value, "'") || strings.HasPrefix(value, "0b")) {
		return false
	}
	_, err := parseBitLiteral(value)
	return err == nil
}

func upsertValuesReference(raw string, indexes map[string]int) (int, bool, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "values") {
		return 0, false, nil
	}
	rest := strings.TrimSpace(raw[len("values"):])
	if len(rest) < 3 || rest[0] != '(' {
		return 0, false, sqlFailure{1064, "42000", "malformed VALUES() reference"}
	}
	close, ok := matchingParenthesis(rest, 0)
	if !ok || strings.TrimSpace(rest[close+1:]) != "" {
		return 0, false, sqlFailure{1064, "42000", "malformed VALUES() reference"}
	}
	column, ok := singleIdentifier(rest[1:close])
	if !ok {
		return 0, false, sqlFailure{1064, "42000", "invalid VALUES() column"}
	}
	index, found := indexes[catalog.Key(column)]
	if !found {
		return 0, false, sqlFailure{1054, "42S22", "unknown column '" + column + "'"}
	}
	return index, true, nil
}

func applyUpsertPlan(plan upsertPlan) ([][]string, uint64, error) {
	input := plan.insert
	input.table.Rows = nil
	candidates, _, err := applyInsertPlan(input)
	if err != nil {
		return nil, 0, err
	}
	indexes, err := tableColumnIndexes(plan.insert.table)
	if err != nil {
		return nil, 0, err
	}
	rows, affected := cloneRows(plan.insert.table.Rows), uint64(0)
	for number, candidate := range candidates {
		var changed uint64
		rows, changed, err = applyUpsertCandidate(plan, indexes, rows, candidate, number+1)
		if err != nil {
			return nil, 0, err
		}
		affected += changed
	}
	return rows, affected, nil
}

func applyUpsertCandidate(plan upsertPlan, indexes map[string]int, rows [][]string, candidate []string, rowNumber int) ([][]string, uint64, error) {
	conflicts, err := conflictingUniqueRows(plan.insert.table, indexes, rows, candidate)
	if err != nil {
		return nil, 0, err
	}
	conflict := firstConflictingRow(rows, conflicts)
	if conflict < 0 {
		return append(rows, candidate), 1, nil
	}
	updated, err := applyUpsertAssignments(plan, rows[conflict], candidate, rowNumber)
	if err != nil {
		return nil, 0, err
	}
	changed := uint64(0)
	if !equalTableRow(updated, rows[conflict]) {
		changed = 2
	}
	rows[conflict] = updated
	return rows, changed, nil
}

func firstConflictingRow(rows [][]string, conflicts map[int]bool) int {
	for index := range rows {
		if conflicts[index] {
			return index
		}
	}
	return -1
}

func applyUpsertAssignments(plan upsertPlan, existing, candidate []string, rowNumber int) ([]string, error) {
	updated := append([]string(nil), existing...)
	for _, assignment := range plan.updates {
		raw := assignment.value
		if assignment.candidateFrom != nil {
			column := *assignment.candidateFrom
			raw = storedColumnLiteral(plan.insert.table, column, candidate[column])
		}
		canonical, err := canonicalColumnValueAtOffset(plan.insert.table, assignment.column, raw, rowNumber, plan.insert.offsetMinutes)
		if err != nil {
			return nil, err
		}
		updated[assignment.column] = canonical
	}
	return updated, nil
}

func storedColumnLiteral(table catalog.Table, column int, value string) string {
	if value == storedSQLNullValue {
		return "NULL"
	}
	if typeName, known := table.ColumnType(column); known {
		if typ, err := parseNumericType(typeName); err == nil && typ.kind != numericNone {
			return value
		}
	}
	return quote(value)
}

func equalTableRow(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// replaceRows implements REPLACE as a delete of every conflicting unique-key
// row followed by an insert of the submitted row. The final image is committed
// through mutateCatalog, so all row and cross-table constraints are checked
// before it becomes visible.
func replaceRows(s *relationExecutor, query string) (uint64, error) {
	plan, err := makeReplacePlan(s, query)
	if err != nil {
		return 0, err
	}
	_, affected, err := applyReplacePlan(plan)
	if err != nil {
		return 0, err
	}
	action := func(definition *catalog.Definition) error {
		return mutateTableRows(definition, plan.namespace, plan.name, func(table catalog.Table) ([][]string, error) {
			currentPlan := plan
			currentPlan.table = table
			if err := validateReplaceDeletePhases(*definition, currentPlan); err != nil {
				return nil, err
			}
			rows, _, err := applyReplacePlan(currentPlan)
			return rows, err
		})
	}
	if err := s.mutateCatalog(action); err != nil {
		return 0, catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	return affected, nil
}

func makeReplacePlan(s *relationExecutor, query string) (insertPlan, error) {
	return makeInsertionPlan(s, replaceInsertInput(query))
}

func makeReplaceExplanationPlan(s *relationExecutor, query string) (insertPlan, error) {
	return makeInsertionExplanationPlan(s, replaceInsertInput(query))
}

func replaceInsertInput(query string) string {
	rest := strings.TrimSpace(query[len("REPLACE"):])
	if strings.HasPrefix(strings.ToLower(rest), "into ") {
		return "INSERT " + rest
	}
	return "INSERT INTO " + rest
}

func applyReplacePlan(plan insertPlan) ([][]string, uint64, error) {
	candidates, err := replacementCandidates(plan)
	if err != nil {
		return nil, 0, err
	}
	indexes, err := tableColumnIndexes(plan.table)
	if err != nil {
		return nil, 0, err
	}
	rows, affected := cloneRows(plan.table.Rows), uint64(0)
	for _, candidate := range candidates {
		conflicts, err := conflictingUniqueRows(plan.table, indexes, rows, candidate)
		if err != nil {
			return nil, 0, err
		}
		rows = append(rowsWithoutConflicts(rows, conflicts), candidate)
		affected += uint64(len(conflicts) + 1)
	}
	return rows, affected, nil
}

func replacementCandidates(plan insertPlan) ([][]string, error) {
	input := plan
	input.table.Rows = nil
	candidates, _, err := applyInsertPlan(input)
	return candidates, err
}

func rowsWithoutConflicts(rows [][]string, conflicts map[int]bool) [][]string {
	kept := make([][]string, 0, len(rows)-len(conflicts))
	for index, row := range rows {
		if !conflicts[index] {
			kept = append(kept, row)
		}
	}
	return kept
}

// validateReplaceDeletePhases observes the delete stage of each replacement.
// A later insert cannot repair a foreign-key violation that the delete caused.
func validateReplaceDeletePhases(definition catalog.Definition, plan insertPlan) error {
	candidates, err := replacementCandidates(plan)
	if err != nil {
		return err
	}
	indexes, err := tableColumnIndexes(plan.table)
	if err != nil {
		return err
	}
	current, rows := definition, cloneRows(plan.table.Rows)
	for _, candidate := range candidates {
		conflicts, err := conflictingUniqueRows(plan.table, indexes, rows, candidate)
		if err != nil {
			return err
		}
		without := rowsWithoutConflicts(rows, conflicts)
		deleted, err := replaceDefinitionRows(current, plan, without)
		if err != nil {
			return err
		}
		if err := validateConstraintDefinition(current, deleted); err != nil {
			return err
		}
		rows = append(without, candidate)
		current, err = replaceDefinitionRows(deleted, plan, rows)
		if err != nil {
			return err
		}
	}
	return nil
}

func replaceDefinitionRows(definition catalog.Definition, plan insertPlan, rows [][]string) (catalog.Definition, error) {
	return catalog.Apply(definition, func(staged *catalog.Definition) error {
		return mutateTableRows(staged, plan.namespace, plan.name, func(catalog.Table) ([][]string, error) {
			return rows, nil
		})
	})
}

// conflictingUniqueRows returns every stored row that shares a primary,
// unique-constraint, or unique-index key with candidate. A replacement can
// therefore remove more than one row when different unique keys collide.
func conflictingUniqueRows(table catalog.Table, indexes map[string]int, rows [][]string, candidate []string) (map[int]bool, error) {
	conflicts := make(map[int]bool)
	if err := addUniqueConstraintConflicts(table, indexes, rows, candidate, conflicts); err != nil {
		return nil, err
	}
	if err := addUniqueIndexConflicts(table, indexes, rows, candidate, conflicts); err != nil {
		return nil, err
	}
	return conflicts, nil
}

func addUniqueConstraintConflicts(table catalog.Table, indexes map[string]int, rows [][]string, candidate []string, conflicts map[int]bool) error {
	for _, constraint := range table.Constraints {
		if isUniqueConstraint(constraint) {
			addConstraintConflicts(table, indexes, rows, candidate, constraint, conflicts)
		}
	}
	return nil
}

func isUniqueConstraint(constraint catalog.Constraint) bool {
	return constraint.Type == catalog.ConstraintTypePrimary || constraint.Type == catalog.ConstraintTypeUnique
}

func addConstraintConflicts(table catalog.Table, indexes map[string]int, rows [][]string, candidate []string, constraint catalog.Constraint, conflicts map[int]bool) {
	columns := constraintIndexes(constraint.Columns, indexes)
	candidateKey, candidateNullable := constraintRowKey(table, candidate, columns)
	if candidateNullable && constraint.Type == catalog.ConstraintTypeUnique {
		return
	}
	for index, row := range rows {
		key, rowNullable := constraintRowKey(table, row, columns)
		if (!rowNullable || constraint.Type != catalog.ConstraintTypeUnique) && key == candidateKey {
			conflicts[index] = true
		}
	}
}

func addUniqueIndexConflicts(table catalog.Table, indexes map[string]int, rows [][]string, candidate []string, conflicts map[int]bool) error {
	for _, index := range table.Indexes {
		if index.Unique {
			if err := addIndexConflicts(table, indexes, rows, candidate, index, conflicts); err != nil {
				return err
			}
		}
	}
	return nil
}

func addIndexConflicts(table catalog.Table, indexes map[string]int, rows [][]string, candidate []string, index catalog.Index, conflicts map[int]bool) error {
	candidateKey, nullable, err := uniqueIndexRowKey(table, index, indexes, candidate)
	if err != nil || nullable {
		return err
	}
	for rowIndex, row := range rows {
		key, rowNullable, err := uniqueIndexRowKey(table, index, indexes, row)
		if err != nil {
			return err
		}
		if !rowNullable && key == candidateKey {
			conflicts[rowIndex] = true
		}
	}
	return nil
}

type insertPlan struct {
	namespace, name string
	table           catalog.Table
	columns         []int
	groups          [][]string
	defaultValues   bool
	sourceSQL       string
	offsetMinutes   int
}

// makeInsertionPlan parses the two supported insert sources. INSERT ... SELECT
// first evaluates the read plan against the statement snapshot. The resulting
// literal groups are then written through the same strict value and constraint
// path as INSERT ... VALUES.
func makeInsertionPlan(s *relationExecutor, query string) (insertPlan, error) {
	if err := rejectUnsupportedInsertionVariant(query); err != nil {
		return insertPlan{}, err
	}
	if plan, selected, err := makeInsertSelectPlan(s, query, true); selected || err != nil {
		return plan, err
	}
	if plan, set, err := makeInsertSetPlan(s, query); set || err != nil {
		return plan, err
	}
	return makeInsertPlan(s, query)
}

// makeInsertionExplanationPlan validates the same mutation grammar without
// reading source rows. EXPLAIN must describe INSERT ... SELECT, not run it.
func makeInsertionExplanationPlan(s *relationExecutor, query string) (insertPlan, error) {
	if err := rejectUnsupportedInsertionVariant(query); err != nil {
		return insertPlan{}, err
	}
	if plan, selected, err := makeInsertSelectPlan(s, query, false); selected || err != nil {
		return plan, err
	}
	if plan, set, err := makeInsertSetPlan(s, query); set || err != nil {
		return plan, err
	}
	return makeInsertPlan(s, query)
}

func rejectUnsupportedInsertionVariant(query string) error {
	if keywordAt(query, "returning") >= 0 {
		return sqlFailure{1235, "42000", "INSERT and REPLACE RETURNING are not supported in v0.1"}
	}
	return nil
}

func makeInsertSelectPlan(s *relationExecutor, query string, materialize bool) (insertPlan, bool, error) {
	parts, columns, source, selected, err := parseInsertSelectInput(query)
	if !selected || err != nil {
		return insertPlan{}, selected, err
	}
	plan, err := makeExplicitInsertPlan(s, parts, columns, nil)
	if err != nil {
		return insertPlan{}, true, err
	}
	selectPlan, err := parseRelationalSelect(s, source)
	if err != nil {
		return insertPlan{}, true, err
	}
	if len(selectPlan.projection) != len(plan.columns) {
		return insertPlan{}, true, sqlFailure{1136, "21S01", "column count does not match value count"}
	}
	groups, err := materializedInsertSelectGroups(selectPlan, materialize)
	if err != nil {
		return insertPlan{}, true, err
	}
	plan.groups, plan.sourceSQL = groups, source
	return plan, true, nil
}

func parseInsertSelectInput(query string) ([]string, []string, string, bool, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "insert into ") {
		return nil, nil, "", false, nil
	}
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	position := keywordAt(rest, "select")
	if position < 0 {
		return nil, nil, "", false, nil
	}
	parts, columns, ok := insertTarget(strings.TrimSpace(rest[:position]))
	source := strings.TrimSpace(rest[position:])
	if !ok || len(parts) == 0 || len(parts) > 2 || source == "" {
		return nil, nil, "", true, sqlFailure{1064, "42000", "malformed INSERT ... SELECT"}
	}
	return parts, columns, source, true, nil
}

func materializedInsertSelectGroups(plan *relationalSelectPlan, materialize bool) ([][]string, error) {
	if !materialize {
		return nil, nil
	}
	resultRows, err := collectRelationalResultRows(plan)
	if err != nil {
		return nil, err
	}
	return insertSelectGroups(plan.shapeRows(resultRows)), nil
}

func insertSelectGroups(rows []relationalResultRow) [][]string {
	groups := make([][]string, len(rows))
	for rowIndex, row := range rows {
		groups[rowIndex] = make([]string, len(row.projections))
		for valueIndex, value := range row.projections {
			groups[rowIndex][valueIndex] = expressionValueLiteral(value)
		}
	}
	return groups
}

func expressionValueLiteral(value exprValue) string {
	if value.isNull() {
		return "NULL"
	}
	if value.kind == valueString {
		return quote(value.s)
	}
	return value.render()
}

func makeInsertSetPlan(s *relationExecutor, query string) (insertPlan, bool, error) {
	parts, columns, values, set, err := parseInsertSetInput(query)
	if !set || err != nil {
		return insertPlan{}, set, err
	}
	plan, err := makeExplicitInsertPlan(s, parts, columns, [][]string{values})
	return plan, true, err
}

func parseInsertSetInput(query string) ([]string, []string, []string, bool, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "insert into ") {
		return nil, nil, nil, false, nil
	}
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	position := keywordAt(rest, "set")
	if position < 0 {
		return nil, nil, nil, false, nil
	}
	parts, ok := splitQualifiedIdentifier(strings.TrimSpace(rest[:position]))
	assignments := strings.TrimSpace(rest[position+len("set"):])
	if !ok || len(parts) == 0 || len(parts) > 2 || assignments == "" {
		return nil, nil, nil, true, sqlFailure{1064, "42000", "malformed INSERT ... SET"}
	}
	columns, values, err := insertSetAssignments(assignments)
	return parts, columns, values, true, err
}

func insertSetAssignments(text string) ([]string, []string, error) {
	parts := splitCSV(text)
	columns, values := make([]string, len(parts)), make([]string, len(parts))
	for index, assignment := range parts {
		column, value, ok := splitEquals(assignment)
		if !ok {
			return nil, nil, sqlFailure{1064, "42000", "malformed INSERT ... SET assignment"}
		}
		column, ok = singleIdentifier(column)
		if !ok {
			return nil, nil, sqlFailure{1064, "42000", "invalid INSERT ... SET column"}
		}
		columns[index], values[index] = column, strings.TrimSpace(value)
	}
	return columns, values, nil
}

func makeInsertPlan(s *relationExecutor, query string) (insertPlan, error) {
	parts, columns, groups, err := parseInsertInput(query)
	if err != nil {
		return insertPlan{}, err
	}
	return makeExplicitInsertPlan(s, parts, columns, groups)
}

func makeExplicitInsertPlan(s *relationExecutor, parts, columns []string, groups [][]string) (insertPlan, error) {
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
	return insertPlan{namespace: namespace, name: name, table: table, columns: indexes, groups: groups, defaultValues: len(columns) == 0, offsetMinutes: offsetMinutes}, nil
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
	added := make([][]string, 0, len(plan.groups))
	rowNumber := 1
	for _, group := range plan.groups {
		if len(group) == 0 && plan.defaultValues {
			added = append(added, defaultTableRow(plan.table))
			rowNumber++
			continue
		}
		if len(group) != len(plan.columns) {
			return nil, 0, sqlFailure{1136, "21S01", "column count does not match value count"}
		}
		row := defaultTableRow(plan.table)
		for valueIndex, value := range group {
			columnIndex := plan.columns[valueIndex]
			canonical, err := insertColumnValue(plan.table, columnIndex, value, rowNumber, plan.offsetMinutes)
			if err != nil {
				return nil, 0, err
			}
			row[columnIndex] = canonical
		}
		added = append(added, row)
		rowNumber++
	}
	rows := plan.table.Rows
	if cap(rows) < len(rows)+len(added) {
		owned := make([][]string, len(rows), len(rows)+len(added)+256)
		copy(owned, rows)
		rows = owned
	}
	return append(rows, added...), uint64(len(added)), nil
}

func insertColumnValue(table catalog.Table, columnIndex int, raw string, row, offsetMinutes int) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(raw), "DEFAULT") {
		return canonicalColumnValueAtOffset(table, columnIndex, raw, row, offsetMinutes)
	}
	attribute := catalog.ColumnAttributeAt(table, columnIndex)
	if attribute.HasDefault {
		return attribute.Default, nil
	}
	if attribute.Nullable {
		return storedSQLNullValue, nil
	}
	return "", sqlFailure{1364, "HY000", fmt.Sprintf("Field '%s' doesn't have a default value", table.Columns[columnIndex])}
}

func defaultTableRow(table catalog.Table) []string {
	row := make([]string, len(table.Columns))
	for index := range row {
		attribute := catalog.ColumnAttributeAt(table, index)
		if attribute.HasDefault {
			row[index] = attribute.Default
		} else {
			row[index] = storedSQLNullValue
		}
	}
	return row
}

// canonicalColumnValue enforces the strict value contract for a written column.
// A column without a recorded numeric, bit, character, or temporal type keeps
// its literal scalar so a typeless column is unaffected by this seam.
func canonicalColumnValue(table catalog.Table, columnIndex int, raw string, row int) (string, error) {
	return canonicalColumnValueAtOffset(table, columnIndex, raw, row, 0)
}

func canonicalColumnValueAtOffset(table catalog.Table, columnIndex int, raw string, row, offsetMinutes int) (string, error) {
	return assignCanonicalColumnValue(table, columnIndex, raw, row, offsetMinutes, nil)
}

func assignCanonicalColumnValue(table catalog.Table, columnIndex int, raw string, row, offsetMinutes int, resolve func(string) (exprValue, error)) (string, error) {
	value, isNull, err := evaluatedAssignmentText(raw, resolve)
	if err != nil {
		return "", err
	}
	if isNull {
		return storedSQLNullValue, nil
	}
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
	value, err = normalizePreparedDate(typeName, value, preparedWire, prepared, table.Columns[columnIndex], row)
	if err != nil {
		return "", err
	}
	return canonicalTypedValueAtOffset(typeName, value, table.Columns[columnIndex], row, offsetMinutes)
}

func evaluatedAssignmentText(raw string, resolve func(string) (exprValue, error)) (string, bool, error) {
	if isSQLNullLiteral(raw) {
		return storedSQLNullValue, true, nil
	}
	evaluated, err := evaluateScalarWithResolver(raw, resolve)
	if err != nil {
		if !isTypedLiteralSpelling(raw) {
			return "", false, err
		}
		return scalar(raw), false, nil
	}
	if evaluated.isNull() {
		return storedSQLNullValue, true, nil
	}
	return evaluated.render(), false, nil
}

func isTypedLiteralSpelling(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) < 3 {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.HasPrefix(lower, "b'") || strings.HasPrefix(lower, "0b") || strings.HasPrefix(lower, "x'") || strings.HasPrefix(lower, "0x")
}

func tableSchemaResolver(table catalog.Table) func(string) (exprValue, error) {
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return func(string) (exprValue, error) {
			return exprValue{}, err
		}
	}
	return func(name string) (exprValue, error) {
		if _, found := indexes[catalog.Key(name)]; !found {
			return exprValue{}, unknownColumnError(name)
		}
		return stringValue(""), nil
	}
}

func tableRowResolver(table catalog.Table, row []string) func(string) (exprValue, error) {
	if row == nil {
		return nil
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return func(name string) (exprValue, error) {
			return exprValue{}, err
		}
	}
	return func(name string) (exprValue, error) {
		index, found := indexes[catalog.Key(name)]
		if !found || index >= len(row) {
			return exprValue{}, unknownColumnError(name)
		}
		return storedAssignmentValue(table, index, row[index])
	}
}

func storedAssignmentValue(table catalog.Table, index int, raw string) (exprValue, error) {
	if raw == storedSQLNullValue {
		return nullValue(), nil
	}
	typeName, known := table.ColumnType(index)
	if known {
		typ, err := parseNumericType(typeName)
		if err != nil {
			return exprValue{}, err
		}
		if typ.kind != numericNone {
			return evaluateScalar(raw)
		}
	}
	return stringValue(raw), nil
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
	locks := matchingRowLocks(plan.namespace, plan.name, plan.table.Rows, plan.matcher)
	if plan.primaryKey != "" {
		if row, ok := pointUpdateRow(plan); ok {
			locks = []rowLockResource{{namespace: plan.namespace, table: plan.name, key: rowLockKey(row)}}
		}
	}
	if err := s.acquireWriteLocks(locks); err != nil {
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

func pointUpdateRow(plan updatePlan) ([]string, bool) {
	index := catalog.EnsurePrimaryIndex(&plan.table)
	position, ok := index[plan.primaryKey]
	if !ok || position < 0 || position >= len(plan.table.Rows) {
		return nil, false
	}
	return plan.table.Rows[position], true
}

type updatePlan struct {
	namespace, name string
	table           catalog.Table
	updates         []updateAssignment
	matcher         func([]string) bool
	primaryKey      string
	offsetMinutes   int
}

type updateAssignment struct {
	column int
	value  string
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
	primaryKey := ""
	if column, value, ok := parseSimpleEqualityWhere(where); ok && primaryColumn(table) == column {
		primaryKey = value
	}
	return updatePlan{namespace: namespace, name: name, table: table, updates: updates, matcher: matcher, primaryKey: primaryKey, offsetMinutes: offsetMinutes}, nil
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

func assignmentValues(value string, indexes map[string]int) ([]updateAssignment, error) {
	updates := make([]updateAssignment, 0, len(splitCSV(value)))
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
		seen[index] = true
		updates = append(updates, updateAssignment{column: index, value: strings.TrimSpace(rawValue)})
	}
	return updates, nil
}

func applyUpdatePlan(plan updatePlan) ([][]string, uint64, error) {
	if err := validateUpdateAssignments(plan); err != nil {
		return nil, 0, err
	}
	if key, ok := pointUpdatePrimaryKey(plan); ok {
		return applyPointUpdatePlan(plan, key)
	}
	changed := changedUpdateRows(plan)
	if len(changed) == 0 {
		return plan.table.Rows, 0, nil
	}
	return applyChangedUpdateRows(plan, changed)
}

func changedUpdateRows(plan updatePlan) []int {
	changed := make([]int, 0)
	for rowIndex, row := range plan.table.Rows {
		if plan.matcher(row) {
			changed = append(changed, rowIndex)
		}
	}
	return changed
}

func applyChangedUpdateRows(plan updatePlan, changed []int) ([][]string, uint64, error) {
	rows := make([][]string, len(plan.table.Rows))
	copy(rows, plan.table.Rows)
	affected := uint64(0)
	for _, rowIndex := range changed {
		next := append([]string(nil), rows[rowIndex]...)
		assigned, err := assignUpdateRow(plan, next, rowIndex+1)
		if err != nil {
			return nil, 0, err
		}
		rowChanged := false
		for column, value := range assigned {
			if next[column] != value {
				rowChanged = true
			}
			next[column] = value
		}
		rows[rowIndex] = next
		if rowChanged {
			affected++
		}
	}
	return rows, affected, nil
}

func pointUpdatePrimaryKey(plan updatePlan) (string, bool) {
	if plan.matcher == nil {
		return "", false
	}
	primary := ""
	for _, constraint := range plan.table.Constraints {
		if constraint.Type == catalog.ConstraintTypePrimary && len(constraint.Columns) == 1 {
			primary = constraint.Columns[0]
			break
		}
	}
	if primary == "" {
		return "", false
	}
	// Matcher closures from WHERE id = const are not introspectable; probe the
	// primary index by testing candidate keys from a tiny synthetic row when the
	// caller attached an exact primary value through makeUpdatePlan.
	if plan.primaryKey == "" {
		return "", false
	}
	return plan.primaryKey, true
}

func applyPointUpdatePlan(plan updatePlan, key string) ([][]string, uint64, error) {
	index := catalog.EnsurePrimaryIndex(&plan.table)
	position, ok := index[key]
	if !ok {
		return plan.table.Rows, 0, nil
	}
	rows := make([][]string, len(plan.table.Rows))
	copy(rows, plan.table.Rows)
	next := append([]string(nil), rows[position]...)
	assigned, err := assignUpdateRow(plan, next, position+1)
	if err != nil {
		return nil, 0, err
	}
	changed := false
	for column, value := range assigned {
		if next[column] != value {
			changed = true
		}
		next[column] = value
	}
	rows[position] = next
	if !changed {
		return rows, 0, nil
	}
	return rows, 1, nil
}

// validateUpdateAssignments type-checks assignment expressions that do not need
// a source row, so a rejected UPDATE leaves the table untouched even when no
// row matches.
func validateUpdateAssignments(plan updatePlan) error {
	resolve := tableSchemaResolver(plan.table)
	for _, assignment := range plan.updates {
		_, err := assignCanonicalColumnValue(plan.table, assignment.column, assignment.value, 1, plan.offsetMinutes, resolve)
		if isUnknownColumnAssignment(err) {
			return err
		}
	}
	return nil
}

func assignUpdateRow(plan updatePlan, row []string, rowNumber int) (map[int]string, error) {
	working := append([]string(nil), row...)
	resolve := tableRowResolver(plan.table, working)
	updates := make(map[int]string, len(plan.updates))
	for _, assignment := range plan.updates {
		canonical, err := assignCanonicalColumnValue(plan.table, assignment.column, assignment.value, rowNumber, plan.offsetMinutes, resolve)
		if err != nil {
			return nil, err
		}
		working[assignment.column] = canonical
		updates[assignment.column] = canonical
	}
	return updates, nil
}

func isUnknownColumnAssignment(err error) bool {
	var failure sqlFailure
	return errors.As(err, &failure) && failure.code == 1054
}

func deleteRows(s *relationExecutor, query string) (uint64, error) {
	plan, err := makeDeletePlan(s, query)
	if err != nil {
		return 0, err
	}
	if err := s.acquireWriteLocks(matchingRowLocks(plan.namespace, plan.name, plan.table.Rows, plan.matcher)); err != nil {
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
	keyword := "values"
	position := keywordAt(rest, keyword)
	if position < 0 {
		keyword = "value"
		position = keywordAt(rest, keyword)
		if position < 0 {
			return "", "", false
		}
	}
	return strings.TrimSpace(rest[:position]), strings.TrimSpace(rest[position+len(keyword):]), true
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
	columns := normalizeEmptyCSV(splitCSV(value[open+1 : close]))
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
		groups = append(groups, normalizeEmptyCSV(splitCSV(value[1:close])))
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
	evaluated, isNull, err := evaluatedAssignmentText(value, nil)
	if err != nil {
		return "", err
	}
	if isNull {
		return evaluated, nil
	}
	raw := evaluated
	_, want, _ := decodePreparedTemporalLiteral(raw)
	typeName, known := table.ColumnType(index)
	if !known {
		return want, nil
	}
	if err := rejectCrossFamilyPredicate(typeName, value); err != nil {
		return "", err
	}
	if err := rejectQuotedTemporalMatcherNull(typeName, value, want, table.Columns[index]); err != nil {
		return "", err
	}
	if canonical, ok := canonicalMatcherNumeric(typeName, want, table.Columns[index]); ok {
		return canonical, nil
	}
	return canonicalMatcherTemporal(typeName, want, raw, table.Columns[index], offsetMinutes)
}

func rejectQuotedTemporalMatcherNull(typeName, raw, want, column string) error {
	if !isSQLNullSpelling(want) || isSQLNullLiteral(raw) {
		return nil
	}
	typ, err := parseTemporalType(typeName)
	if err != nil {
		return err
	}
	if typ.kind == temporalNone {
		return nil
	}
	return incorrectTemporal(temporalLabel(typ.kind), column, want, 1)
}

func rejectCrossFamilyPredicate(typeName, raw string) error {
	evaluated, err := evaluateScalar(raw)
	if err != nil {
		return nil
	}
	return rejectCrossFamilyValue(typeName, evaluated)
}

func columnIsNumeric(typeName string) bool {
	typ, err := parseNumericType(typeName)
	return err == nil && typ.kind != numericNone
}

func columnIsCharacter(typeName string) bool {
	typ, err := parseCharacterType(typeName)
	return err == nil && typ.kind == characterText
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
		if !catalog.ColumnAttributeAt(table, columnIndex).Nullable {
			definition.flags |= mysqlNotNullFlag
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
	rows := informationSchemaRows(view.name, s.session)
	return projectInformationSchemaRows(view, projection, rows), nil
}

func informationSchemaQueryResult(s *session, view informationSchemaView) *queryResult {
	return projectInformationSchemaRows(view, everyInformationSchemaColumn(view), informationSchemaRows(view.name, s))
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

func informationSchemaRows(viewName string, s *session) [][]metadataValue {
	builder, ok := informationSchemaRowBuilders[strings.ToLower(viewName)]
	if !ok {
		return nil
	}
	return builder(s, informationSchemaDefinition(s))
}

func informationSchemaDefinition(s *session) catalog.Definition {
	if s == nil || s.server == nil || s.server.config.Catalog == nil {
		return emptyDefinition()
	}
	return visibleCatalogDefinition(s.server.config.Catalog.Snapshot(), s.username)
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
	id := s.prepared.nextStmtID
	s.prepared.nextStmtID++
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
	statement := normalizeStatementText(query)
	query, lower := statement.query, statement.lower
	if !isPreparedStatement(lower) {
		return nil, sqlFailure{1064, "42000", "unsupported prepared statement"}
	}
	if !isPreparedRead(lower) {
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

func isPreparedStatement(lower string) bool {
	if isComposedSelectStatement(lower) || isSettingControl(lower) || accountAdministrationStatement(lower) {
		return true
	}
	for _, prefix := range []string{"select ", "with ", "insert into ", "replace ", "update ", "delete from "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func isPreparedRead(lower string) bool {
	return isComposedSelectStatement(lower)
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
	if isComposedSelectStatement(query) {
		relations := &relationExecutor{session: s.session}
		result, err := describeComposedSelect(newComposedQueryContext(relations), query, nil)
		if err != nil {
			return nil, err
		}
		metadata := make([]columnMetadata, len(result.columns))
		for index, name := range result.columns {
			metadata[index] = resultColumnDefinition(name, index, result.metadata)
		}
		return metadata, nil
	}
	statement, err := normalizeStatement(query)
	if err != nil {
		return nil, err
	}
	executor := textStatementExecutor{session: s.session}
	result, err := newStatementExecutionPolicy(&executor).execute(statement)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return preparedResultMetadata(result, preserveMetadata), nil
}

func preparedResultMetadata(result *queryResult, preserveMetadata bool) []columnMetadata {
	metadata := make([]columnMetadata, len(result.columns))
	for index, name := range result.columns {
		metadata[index] = columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB40900AICI, typ: mysqlTypeVarString}
		if preserveMetadata && index < len(result.metadata) {
			metadata[index] = result.metadata[index]
		}
	}
	return metadata
}

func (s *preparedExecution) executePrepared(connection net.Conn, sequence byte, payload []byte) error {
	if len(payload) < 5 {
		return writePacket(connection, sequence, errorPacket(1210, "HY000", "malformed prepared statement"))
	}
	id := binary.LittleEndian.Uint32(payload[1:5])
	statement, ok := s.statements[id]
	if !ok {
		return writePacket(connection, sequence, errorPacket(1243, "HY000", "unknown prepared statement handler"))
	}
	boundStatement, err := s.boundStatement(payload, statement)
	if err != nil {
		return writePacket(connection, sequence, mysqlError(preparedStatementError(err)))
	}
	finishExplanation := s.recordPreparedExplanation(statement)
	defer finishExplanation()
	executor := textStatementExecutor{session: s.session, streamRows: true}
	result, err := newStatementExecutionPolicy(&executor).execute(boundStatement)
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

// boundStatement makes the common policy input only after the prepared values
// have been decoded and bound into the SQL text.
func (s *preparedExecution) boundStatement(payload []byte, statement *preparedStatement) (normalizedStatement, error) {
	params, err := s.preparedValues(payload, statement)
	if err != nil {
		return normalizedStatement{}, preparedStatementError(err)
	}
	query, err := bindPreparedQuery(statement.query, params)
	if err != nil {
		return normalizedStatement{}, preparedStatementError(err)
	}
	return normalizeStatement(query)
}

func preparedStatementError(err error) error {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return err
	}
	return sqlFailure{code: 1210, state: "HY000", message: err.Error()}
}

// recordPreparedExplanation plans a value-free copy of the prepared SQL once
// per statement. The executable query can contain bound values, but the public
// document must not.
func (s *preparedExecution) recordPreparedExplanation(statement *preparedStatement) func() {
	if statement == nil || s.session == nil {
		return func() {}
	}
	if statement.explanation == nil {
		maskedValues := make([]string, statement.parameters)
		for index := range maskedValues {
			maskedValues[index] = "0"
		}
		maskedQuery, err := bindPreparedQuery(statement.query, maskedValues)
		if err != nil {
			return func() {}
		}
		started := time.Now()
		planner := textStatementExecutor{session: s.session}
		plan, err := planner.planExplanation(maskedQuery)
		if err != nil {
			return func() {}
		}
		plan.Timing.PlanningMS = float64(time.Since(started)) / float64(time.Millisecond)
		plan.Statement.SQL = statement.query
		plan.Statement.Parameters = preparedExplanationParameters(statement.types)
		statement.explanation = plan
	}
	return s.server.explanations.begin(s.session.connectionID, statement.explanation, s.session)
}

func preparedExplanationParameters(types []preparedParameterType) []queryexplanation.Parameter {
	parameters := make([]queryexplanation.Parameter, len(types))
	for index, parameter := range types {
		parameters[index] = queryexplanation.Parameter{Position: index + 1, Type: preparedExplanationType(parameter)}
	}
	return parameters
}

var preparedExplanationTypes = map[byte]string{
	mysqlTypeTiny: "TINYINT", mysqlTypeShort: "SMALLINT",
	mysqlTypeLong: "INT", mysqlTypeInt24: "INT", mysqlTypeLongLong: "BIGINT",
	mysqlTypeFloat: "FLOAT", mysqlTypeDouble: "DOUBLE",
	mysqlTypeDate: "DATE", mysqlTypeDatetime: "DATETIME", mysqlTypeTimestamp: "TIMESTAMP", mysqlTypeTime: "TIME",
	mysqlTypeJSON: "JSON", mysqlTypeNewDecimal: "DECIMAL",
	mysqlTypeBlob: "BLOB", mysqlTypeTinyBlob: "BLOB", mysqlTypeMediumBlob: "BLOB", mysqlTypeLongBlob: "BLOB",
}

func preparedExplanationType(parameter preparedParameterType) string {
	if typeName, found := preparedExplanationTypes[parameter.typ]; found {
		return typeName
	}
	return "VARCHAR"
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
	if s.prepared.longDataBytes+len(payload[7:]) > maxPreparedLongDataBytes {
		return sqlFailure{1153, "08S01", "prepared statement long data exceeds maximum size"}
	}
	statement.longData[parameter] = append(statement.longData[parameter], payload[7:]...)
	s.prepared.longDataBytes += len(payload[7:])
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
	s.resetSessionSettings()
	s.closeAllPrepared()
	s.prepared.longDataBytes = 0
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
		session.prepared.longDataBytes -= len(value)
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

func normalizeEmptyCSV(parts []string) []string {
	if len(parts) == 1 && strings.TrimSpace(parts[0]) == "" {
		return nil
	}
	return parts
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

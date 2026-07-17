// Package mysql contains the public classic-protocol server seam.
package mysql

import (
	"bytes"
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

const (
	clientLongPassword     = 1
	clientFoundRows        = 1 << 1
	clientLongFlag         = 1 << 2
	clientConnectWithDB    = 1 << 3
	clientLocalFiles       = 1 << 7
	clientProtocol41       = 1 << 9
	clientTransactions     = 1 << 13
	clientSecureConnection = 1 << 15
	clientMultiResults     = 1 << 17
	clientPluginAuth       = 1 << 19
	clientConnectAttrs     = 1 << 20
	clientPluginLenencData = 1 << 21
	clientSSL              = 1 << 11

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
	mysqlNotNullFlag             uint16 = 1
	mysqlBinaryFlag              uint16 = 1 << 7
	mysqlUnsignedFlag            uint16 = 1 << 5
	maxPreparedParameters               = 65535
	maxPreparedLongDataBytes            = 16 * 1024 * 1024
)

type Config struct {
	Catalog              *catalog.Store
	Username             string
	PasswordHash         string
	Version              string
	TLSCertFile          string
	TLSKeyFile           string
	MaxPreparedStmtCount int
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
	if config.Version == "" {
		config.Version = "0.1.0-dev"
	}
	if config.MaxPreparedStmtCount == 0 {
		config.MaxPreparedStmtCount = 4096
	}
	if config.MaxAllowedPacket == 0 {
		config.MaxAllowedPacket = 64 * 1024 * 1024
	}
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		_ = listener.Close()
		return nil, errors.New("TLS certificate and key must be provided together")
	}
	registry := &connectionRegistry{
		connections:   make(map[net.Conn]struct{}),
		preparedLimit: config.MaxPreparedStmtCount,
	}
	server := &Server{Listener: listener, config: config, connections: registry}
	auth := authenticator{config: config}
	if config.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
		auth.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("generate authentication key: %w", err)
	}
	auth.rsaKey = key
	server.auth = auth
	return server, nil
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
		go connectionWorker{server: s}.serve(connection)
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
	if r.stopping {
		return false
	}
	r.connections[connection] = struct{}{}
	r.connectionW.Add(1)
	return true
}

func (r *connectionRegistry) unregister(connection net.Conn) {
	r.mu.Lock()
	delete(r.connections, connection)
	r.mu.Unlock()
	r.connectionW.Done()
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

// connectionWorker owns one authenticated classic-protocol conversation. Its
// collaborators deliberately separate wire dispatch, SQL/catalog behaviour,
// and prepared-statement behaviour for the follow-on strict-gate slices.
type connectionWorker struct{ server *Server }

type queryExecutor struct{ *session }

type preparedExecutor struct {
	*session
	queries *queryExecutor
}

func (w connectionWorker) serve(connection net.Conn) {
	defer connection.Close()
	defer w.server.connections.unregister(connection)
	var current *session
	var prepared *preparedExecutor
	defer func() {
		if current != nil && current.transaction && w.server.config.Catalog != nil {
			_ = w.server.config.Catalog.Replace(current.transactionSnapshot)
		}
		if prepared != nil {
			prepared.closeAllPrepared()
		}
	}()
	nonce := makeNonce()
	if err := writePacket(connection, 0, handshake(w.server.config.Version, nonce, w.server.auth.tlsConfig != nil)); err != nil {
		return
	}
	authentication, err := w.server.auth.authenticate(connection, nonce)
	if err != nil {
		_ = writePacket(authentication.connection, authentication.nextSequence, mysqlError(err))
		return
	}
	connection = authentication.connection
	if err := writePacket(connection, authentication.nextSequence, okPacket()); err != nil {
		return
	}
	session := &session{server: w.server, username: authentication.username, database: authentication.database, initialDB: authentication.database, statements: map[uint32]*preparedStatement{}, nextStmtID: 1, savepoints: map[string]catalog.Definition{}}
	current = session
	queries := &queryExecutor{session}
	prepared = &preparedExecutor{session: session, queries: queries}
	for {
		sequence, payload, err := readPacket(connection, w.server.config.MaxAllowedPacket)
		if err != nil || len(payload) == 0 {
			return
		}
		if !w.server.connections.acceptingWork() {
			return
		}
		switch payload[0] {
		case 0x01: // COM_QUIT
			return
		case 0x02: // COM_INIT_DB
			queries.useDatabase(string(payload[1:]))
			if err := queries.databaseExists(session.database); err != nil {
				if writePacket(connection, sequence+1, mysqlError(err)) != nil {
					return
				}
				continue
			}
			if writePacket(connection, sequence+1, okPacket()); err != nil {
				return
			}
		case 0x03: // COM_QUERY
			if !w.server.connections.beginStatement() {
				return
			}
			err := queries.writeQueryResult(connection, sequence+1, string(payload[1:]))
			w.server.connections.endStatement()
			if err != nil {
				return
			}
		case 0x0e: // COM_PING
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		case 0x16: // COM_STMT_PREPARE
			if err := prepared.prepare(connection, sequence+1, string(payload[1:])); err != nil {
				return
			}
		case 0x17: // COM_STMT_EXECUTE
			if !w.server.connections.beginStatement() {
				return
			}
			err := prepared.executePrepared(connection, sequence+1, payload)
			w.server.connections.endStatement()
			if err != nil {
				return
			}
		case 0x19: // COM_STMT_CLOSE
			if len(payload) != 5 {
				if writePacket(connection, sequence+1, errorPacket(1210, "HY000", "malformed prepared statement close")) != nil {
					return
				}
				continue
			}
			id := binary.LittleEndian.Uint32(payload[1:5])
			prepared.closePrepared(id)
		case 0x18: // COM_STMT_SEND_LONG_DATA
			if err := prepared.sendLongData(payload); err != nil {
				if writePacket(connection, sequence+1, mysqlError(err)) != nil {
					return
				}
			}
		case 0x1a: // COM_STMT_RESET
			if err := prepared.resetPrepared(payload); err != nil {
				if writePacket(connection, sequence+1, mysqlError(err)) != nil {
					return
				}
				continue
			}
			if writePacket(connection, sequence+1, okPacket()) != nil {
				return
			}
		case 0x1f: // COM_RESET_CONNECTION
			if len(payload) != 1 {
				if writePacket(connection, sequence+1, errorPacket(1210, "HY000", "malformed connection reset")) != nil {
					return
				}
				continue
			}
			if err := prepared.resetConnection(); err != nil {
				if writePacket(connection, sequence+1, mysqlError(err)) != nil {
					return
				}
				continue
			}
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

type authenticationResult struct {
	connection   net.Conn
	username     string
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

func (a authenticator) authenticate(connection net.Conn, nonce []byte) (authenticationResult, error) {
	sequence, payload, err := readPacket(connection, a.config.MaxAllowedPacket)
	if err != nil {
		return authenticationResult{connection: connection, nextSequence: 2}, err
	}
	secure := false
	if len(payload) == 32 && binary.LittleEndian.Uint32(payload[:4])&clientSSL != 0 {
		if a.tlsConfig == nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "TLS capability is not supported"}
		}
		tlsConnection := tls.Server(connection, a.tlsConfig)
		if err := tlsConnection.Handshake(); err != nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, err
		}
		connection = tlsConnection
		sequence, payload, err = readPacket(connection, a.config.MaxAllowedPacket)
		if err != nil {
			return authenticationResult{connection: connection, nextSequence: 3}, err
		}
		secure = true
	}
	if len(payload) < 32 {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "malformed handshake response"}
	}
	capabilities := binary.LittleEndian.Uint32(payload[:4])
	if capabilities&^acceptedClientCapabilities(a.tlsConfig != nil) != 0 {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "unsupported client capabilities"}
	}
	if capabilities&clientProtocol41 == 0 || capabilities&clientSecureConnection == 0 || capabilities&clientPluginAuth == 0 {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "required protocol capabilities are missing"}
	}
	offset := 32
	username, offset, ok := readNullString(payload, offset)
	if !ok {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "malformed username"}
	}
	var token []byte
	if capabilities&clientPluginLenencData != 0 {
		token, offset, ok = readLengthEncoded(payload, offset)
	} else if capabilities&clientSecureConnection != 0 {
		if offset >= len(payload) {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "missing authentication response"}
		}
		length := int(payload[offset])
		offset++
		if offset+length > len(payload) {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "malformed authentication response"}
		}
		token, offset = payload[offset:offset+length], offset+length
	} else {
		token, offset = readNullBytes(payload, offset)
		ok = token != nil
	}
	if !ok {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1043, "08S01", "malformed authentication response"}
	}
	database := ""
	if capabilities&clientConnectWithDB != 0 {
		if databaseName, next, found := readNullString(payload, offset); found {
			database = databaseName
			offset = next
		}
	}
	plugin, _, pluginOK := readNullString(payload, offset)
	if !pluginOK || plugin != "caching_sha2_password" {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1251, "08004", "client does not support caching_sha2_password"}
	}
	if a.config.Username != "" && username != a.config.Username {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1045, "28000", "access denied"}
	}
	if a.config.PasswordHash != "" && !validPasswordToken(token, nonce, a.config.PasswordHash) {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1045, "28000", "access denied"}
	}
	if database != "" {
		if err := a.databaseExists(database); err != nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, err
		}
	}
	// Do not downgrade a full caching_sha2_password exchange to an accepted
	// scramble. A secure channel receives the clear password only inside TLS;
	// an insecure channel must request and use the server's RSA public key.
	if err := writePacket(connection, sequence+1, []byte{0x01, 0x04}); err != nil {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, err
	}
	sequence, response, err := readPacket(connection, a.config.MaxAllowedPacket)
	if err != nil {
		return authenticationResult{connection: connection, nextSequence: sequence + 2}, err
	}
	var password []byte
	if secure {
		password = response
	} else {
		if len(response) != 1 || response[0] != 0x02 {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1045, "28000", "secure authentication exchange required"}
		}
		if err := writePacket(connection, sequence+1, publicKeyPacket(a.rsaKey)); err != nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, err
		}
		sequence, response, err = readPacket(connection, a.config.MaxAllowedPacket)
		if err != nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 2}, err
		}
		password, err = decryptPassword(a.rsaKey, response, nonce)
		if err != nil {
			return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1045, "28000", "access denied"}
		}
	}
	password = bytes.TrimSuffix(password, []byte{0})
	if !validPlainPassword(password, a.config.PasswordHash) {
		return authenticationResult{connection: connection, nextSequence: sequence + 1}, sqlFailure{1045, "28000", "access denied"}
	}
	return authenticationResult{connection: connection, username: username, database: database, nextSequence: sequence + 1}, nil
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
	return writeResult(connection, sequence, result.columns, result.rows, result.nulls, result.metadata, s.server.config.MaxAllowedPacket)
}

func (s *queryExecutor) execute(query string) (*queryResult, error) {
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
		return nil, createTable(s, query)
	}
	if strings.HasPrefix(lower, "insert into ") {
		return nil, insert(s, query)
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

func (s *queryExecutor) use(name string) error {
	name = identifier(name)
	if err := s.databaseExists(name); err != nil {
		return err
	}
	s.database = name
	return nil
}
func (s *queryExecutor) useDatabase(name string) { _ = s.use(name) }
func (s *queryExecutor) databaseExists(name string) error {
	return s.server.auth.databaseExists(identifier(name))
}

func (s *queryExecutor) metadataDefinition() catalog.Definition {
	if s.transaction {
		return s.transactionSnapshot
	}
	if s.server.config.Catalog == nil {
		return catalog.Definition{Namespaces: map[string]catalog.Namespace{}}
	}
	return s.server.config.Catalog.Snapshot()
}

func (s *queryExecutor) snapshotNamespace(name string) (catalog.Namespace, bool) {
	if s.server.config.Catalog == nil {
		return catalog.Namespace{}, false
	}
	ns, ok := s.server.config.Catalog.Snapshot().Namespaces[strings.ToLower(name)]
	return ns, ok
}
func (s *queryExecutor) createDatabase(query string) error {
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
func createTable(s *queryExecutor, query string) error {
	open := strings.Index(query, "(")
	close := strings.LastIndex(query, ")")
	if open < 0 || close <= open {
		return sqlFailure{1064, "42000", "malformed CREATE TABLE"}
	}
	if strings.TrimSpace(query[close+1:]) != "" {
		return sqlFailure{1235, "42000", "unsupported table definition"}
	}
	head := strings.TrimSpace(query[len("CREATE TABLE "):open])
	partsForTable, ok := splitQualifiedIdentifier(head)
	if !ok || len(partsForTable) == 0 || len(partsForTable) > 2 {
		return sqlFailure{1064, "42000", "invalid table name"}
	}
	namespace, name, err := s.tableTarget(partsForTable)
	if err != nil {
		return err
	}
	parts := splitCSV(query[open+1 : close])
	columns := make([]string, 0, len(parts))
	columnTypes := make([]string, 0, len(parts))
	for _, part := range parts {
		column, remainder, valid := consumeIdentifier(part)
		if !valid {
			return sqlFailure{1064, "42000", "invalid column definition"}
		}
		fields := strings.Fields(remainder)
		if isUnsupportedTableDefinition(column) || hasUnsupportedColumnModifier(fields) {
			return sqlFailure{1235, "42000", "unsupported table definition"}
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
	if err := s.server.config.Catalog.CreateTableWithTypes(namespace, name, columns, columnTypes); err != nil {
		return sqlFailure{1050, "42S01", err.Error()}
	}
	return nil
}

func (s *queryExecutor) showCreateDatabase(query string) (*queryResult, error) {
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

func (s *queryExecutor) showCreateTable(query string) (*queryResult, error) {
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
func insert(s *queryExecutor, query string) error {
	rest := strings.TrimSpace(query[len("INSERT INTO "):])
	valuesAt := strings.Index(strings.ToLower(rest), "values")
	if valuesAt < 0 {
		return sqlFailure{1064, "42000", "malformed INSERT"}
	}
	head := strings.TrimSpace(rest[:valuesAt])
	valueText := strings.TrimSpace(rest[valuesAt+len("values"):])
	target := strings.TrimSpace(head)
	if open := strings.IndexByte(target, '('); open >= 0 {
		target = strings.TrimSpace(target[:open])
	}
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return sqlFailure{1064, "42000", "malformed INSERT"}
	}
	namespace, name, err := s.tableTarget(parts)
	if err != nil {
		return err
	}
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
	if err := s.server.config.Catalog.Insert(namespace, name, row); err != nil {
		return sqlFailure{1136, "21S01", err.Error()}
	}
	return nil
}
func (s *queryExecutor) selectQuery(query string) (*queryResult, error) {
	expression := strings.TrimSpace(query[len("SELECT "):])
	lower := strings.ToLower(expression)
	if from := strings.Index(lower, " from "); from >= 0 {
		projection := strings.TrimSpace(expression[:from])
		source := strings.TrimSpace(expression[from+6:])
		if strings.HasPrefix(strings.ToLower(source), informationSchemaName+".") || strings.HasPrefix(strings.ToLower(source), "`information_schema`.") {
			return s.selectInformationSchema(query)
		}
		parts, valid := splitQualifiedIdentifier(strings.Fields(source)[0])
		if !valid || len(parts) == 0 || len(parts) > 2 {
			return nil, sqlFailure{1064, "42000", "invalid table name"}
		}
		namespace, tableName, err := s.tableTarget(parts)
		if err != nil {
			return nil, err
		}
		ns, ok := s.snapshotNamespace(namespace)
		if !ok {
			return nil, sqlFailure{1049, "42000", "unknown database '" + namespace + "'"}
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
	literal := parseLiteralResult(expression)
	if !literal.supported {
		return nil, sqlFailure{1064, "42000", "unsupported expression"}
	}
	return &queryResult{columns: []string{expression}, rows: [][]string{{literal.value}}, nulls: [][]bool{{literal.isNull}}, metadata: []columnMetadata{literal.metadata}}, nil
}

// tableTarget resolves an unqualified table against the current namespace and
// a qualified table against its named namespace. Keeping this resolution at
// the protocol seam makes DDL, writes, and reads agree about namespace scope.
func (s *queryExecutor) tableTarget(parts []string) (string, string, error) {
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

func (s *queryExecutor) selectInformationSchema(query string) (*queryResult, error) {
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

func (s *preparedExecutor) prepare(connection net.Conn, sequence byte, query string) error {
	parameters, withinLimit := countPreparedParameters(query, maxPreparedParameters)
	if !withinLimit {
		return writePacket(connection, sequence, errorPacket(1390, "HY000", "prepared statement contains too many placeholders"))
	}
	if !s.server.connections.reservePreparedStatement() {
		return writePacket(connection, sequence, errorPacket(1461, "HY000", "can't create more than max_prepared_stmt_count statements"))
	}
	id := s.nextStmtID
	s.nextStmtID++
	statement := &preparedStatement{query: query, parameters: parameters, longData: make(map[uint16][]byte)}
	s.statements[id] = statement
	metadata, err := s.preparedColumns(query)
	if err != nil {
		delete(s.statements, id)
		s.server.connections.releasePreparedStatement()
		return writePacket(connection, sequence, mysqlError(err))
	}
	response := []byte{0x00, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), byte(len(metadata)), 0, byte(parameters), byte(parameters >> 8), 0, 0, 0}
	maximum := s.server.config.MaxAllowedPacket
	if int64(len(response)) > maximum {
		delete(s.statements, id)
		s.server.connections.releasePreparedStatement()
		return writePacket(connection, sequence, errorPacket(1153, "08S01", "prepared statement metadata exceeds maximum packet size"))
	}
	for i := 0; i < parameters; i++ {
		if int64(len(columnDefinition(columnMetadata{catalog: "def", name: fmt.Sprintf("param%d", i+1), characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarchar}))) > maximum {
			delete(s.statements, id)
			s.server.connections.releasePreparedStatement()
			return writePacket(connection, sequence, errorPacket(1153, "08S01", "prepared statement metadata exceeds maximum packet size"))
		}
	}
	for _, definition := range metadata {
		if int64(len(columnDefinition(definition))) > maximum {
			delete(s.statements, id)
			s.server.connections.releasePreparedStatement()
			return writePacket(connection, sequence, errorPacket(1153, "08S01", "prepared statement metadata exceeds maximum packet size"))
		}
	}
	if err := writeBoundedPacket(connection, sequence, response, maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, response)
	if parameters > 0 {
		for i := 0; i < parameters; i++ {
			payload := columnDefinition(columnMetadata{catalog: "def", name: fmt.Sprintf("param%d", i+1), characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarchar})
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

func (s *preparedExecutor) preparedColumns(query string) ([]columnMetadata, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if !strings.HasPrefix(strings.ToLower(query), "select ") {
		return nil, sqlFailure{1064, "42000", "prepared statements support SELECT only"}
	}
	expression := strings.TrimSpace(query[len("select "):])
	parameters := make([]string, parameterCount(query))
	for index := range parameters {
		parameters[index] = "NULL"
	}
	validated, err := bindPreparedQuery(query, parameters)
	if err != nil {
		return nil, sqlFailure{1064, "42000", "malformed prepared statement"}
	}
	if len(parameters) > 0 {
		result, err := s.queries.execute(validated)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		metadata := make([]columnMetadata, len(result.columns))
		for index, name := range result.columns {
			metadata[index] = columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
		}
		return metadata, nil
	}
	if literal := parseLiteralResult(expression); literal.supported {
		return []columnMetadata{literal.metadata}, nil
	}
	result, err := s.queries.execute(validated)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	metadata := make([]columnMetadata, len(result.columns))
	for index, name := range result.columns {
		metadata[index] = columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
		if index < len(result.metadata) {
			metadata[index] = result.metadata[index]
		}
	}
	return metadata, nil
}

func (s *preparedExecutor) executePrepared(connection net.Conn, sequence byte, payload []byte) error {
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
	result, err := s.queries.execute(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	if err != nil {
		return writePacket(connection, sequence, mysqlError(err))
	}
	if result == nil {
		return writePacket(connection, sequence, okPacket())
	}
	return writeBinaryResult(connection, sequence, result.columns, result.rows, result.nulls, result.metadata, s.server.config.MaxAllowedPacket)
}

func (s *preparedExecutor) preparedValues(payload []byte, statement *preparedStatement) ([]string, error) {
	count := statement.parameters
	if len(payload) < 10 {
		return nil, errors.New("malformed prepared statement")
	}
	if payload[5] != 0 || binary.LittleEndian.Uint32(payload[6:10]) != 1 {
		return nil, errors.New("unsupported prepared statement execute header")
	}
	if count == 0 {
		if len(payload) != 10 {
			return nil, errors.New("malformed prepared statement trailing data")
		}
		return nil, nil
	}
	nullBytes := (count + 7) / 8
	if len(payload) < 10+nullBytes+1 {
		return nil, errors.New("malformed prepared statement parameters")
	}
	offset := 10 + nullBytes
	newTypes := payload[offset]
	offset++
	if newTypes > 1 {
		return nil, errors.New("malformed prepared statement type flag")
	}
	types := statement.types
	if newTypes != 0 {
		if len(payload[offset:]) < count*2 {
			return nil, errors.New("malformed prepared statement types")
		}
		types = make([]preparedParameterType, count)
		for i := range types {
			types[i] = preparedParameterType{typ: payload[offset+i*2], unsigned: payload[offset+i*2+1]&0x80 != 0}
		}
		offset += count * 2
	} else if len(types) != count {
		return nil, errors.New("prepared statement parameter types are unavailable")
	}
	values := make([]string, count)
	for i := 0; i < count; i++ {
		if payload[10+i/8]&(1<<uint(i%8)) != 0 {
			values[i] = "NULL"
			continue
		}
		if long, ok := statement.longData[uint16(i)]; ok {
			values[i] = quote(string(long))
			continue
		}
		value, next, err := readPreparedValue(payload, offset, types[i])
		if err != nil {
			return nil, err
		}
		values[i], offset = value, next
	}
	if offset != len(payload) {
		return nil, errors.New("malformed prepared statement trailing data")
	}
	statement.types = types
	s.clearLongData(statement)
	return values, nil
}

func readPreparedValue(payload []byte, offset int, typ preparedParameterType) (string, int, error) {
	need := func(size int) error {
		if offset+size > len(payload) {
			return errors.New("malformed prepared parameter")
		}
		return nil
	}
	integer := func(size int) (string, int, error) {
		if err := need(size); err != nil {
			return "", offset, err
		}
		var value uint64
		for i := 0; i < size; i++ {
			value |= uint64(payload[offset+i]) << (8 * i)
		}
		if typ.unsigned {
			return strconv.FormatUint(value, 10), offset + size, nil
		}
		switch size {
		case 1:
			return strconv.FormatInt(int64(int8(value)), 10), offset + size, nil
		case 2:
			return strconv.FormatInt(int64(int16(value)), 10), offset + size, nil
		case 4:
			return strconv.FormatInt(int64(int32(value)), 10), offset + size, nil
		default:
			return strconv.FormatInt(int64(value), 10), offset + size, nil
		}
	}
	switch typ.typ {
	case mysqlTypeNull:
		return "NULL", offset, nil
	case mysqlTypeTiny:
		return integer(1)
	case mysqlTypeShort:
		return integer(2)
	case mysqlTypeLong:
		return integer(4)
	case mysqlTypeLongLong:
		return integer(8)
	case mysqlTypeFloat:
		if err := need(4); err != nil {
			return "", offset, err
		}
		return strconv.FormatFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(payload[offset:offset+4]))), 'g', -1, 32), offset + 4, nil
	case mysqlTypeDouble:
		if err := need(8); err != nil {
			return "", offset, err
		}
		return strconv.FormatFloat(math.Float64frombits(binary.LittleEndian.Uint64(payload[offset:offset+8])), 'g', -1, 64), offset + 8, nil
	case mysqlTypeVarchar, mysqlTypeVarString, 0xfe, 0xfc, 0xfb, 0xfa, 0xf9, 0xf5, 0xf6:
		raw, next, ok := readLengthEncoded(payload, offset)
		if !ok || raw == nil {
			return "", offset, errors.New("malformed string prepared parameter")
		}
		return quote(string(raw)), next, nil
	default:
		return "", offset, errors.New("unsupported prepared parameter type")
	}
}

func (s *preparedExecutor) sendLongData(payload []byte) error {
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

func (s *preparedExecutor) resetPrepared(payload []byte) error {
	if len(payload) != 5 {
		return sqlFailure{1210, "HY000", "malformed prepared statement reset"}
	}
	statement, ok := s.statements[binary.LittleEndian.Uint32(payload[1:5])]
	if !ok {
		return sqlFailure{1243, "HY000", "unknown prepared statement handler"}
	}
	s.clearLongData(statement)
	return nil
}

func (s *preparedExecutor) resetConnection() error {
	if err := rollbackTransaction(s.session); err != nil {
		return err
	}
	s.database = s.initialDB
	s.closeAllPrepared()
	s.longDataBytes = 0
	s.savepoints = make(map[string]catalog.Definition)
	return nil
}

func (s *preparedExecutor) closePrepared(id uint32) {
	if statement, ok := s.statements[id]; ok {
		s.clearLongData(statement)
		delete(s.statements, id)
		s.server.connections.releasePreparedStatement()
	}
}

func (s *preparedExecutor) closeAllPrepared() {
	for id := range s.statements {
		s.closePrepared(id)
	}
}

func (s *preparedExecutor) clearLongData(statement *preparedStatement) {
	for _, value := range statement.longData {
		s.longDataBytes -= len(value)
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

func parameterCount(query string) int { return len(preparedPlaceholders(query)) }

func countPreparedParameters(query string, maximum int) (int, bool) {
	count := 0
	quote, escaped := byte(0), false
	for index := 0; index < len(query); index++ {
		character := query[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				if quote == '\'' && index+1 < len(query) && query[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' || character == '`' {
			quote = character
			continue
		}
		if character == '?' {
			count++
			if count > maximum {
				return count, false
			}
		}
	}
	return count, true
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

func preparedPlaceholders(query string) []int {
	positions := make([]int, 0)
	quote, escaped := byte(0), false
	for index := 0; index < len(query); index++ {
		character := query[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				if quote == '\'' && index+1 < len(query) && query[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' || character == '`' {
			quote = character
			continue
		}
		if character == '?' {
			positions = append(positions, index)
		}
	}
	return positions
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

func writeResult(connection net.Conn, sequence byte, columns []string, rows [][]string, nulls [][]bool, metadata []columnMetadata, maximum int64) error {
	count := lengthEncodedInt(len(columns))
	if err := writeBoundedPacket(connection, sequence, count, maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, count)
	for index, name := range columns {
		definition := columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
		if index < len(metadata) {
			definition = metadata[index]
		}
		payload := columnDefinition(definition)
		if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, payload)
	}
	if err := writeBoundedPacket(connection, sequence, eofPacket(), maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, eofPacket())
	for rowIndex, row := range rows {
		payload := []byte{}
		for columnIndex, value := range row {
			if rowIndex < len(nulls) && columnIndex < len(nulls[rowIndex]) && nulls[rowIndex][columnIndex] {
				payload = append(payload, 0xfb)
				continue
			}
			payload = append(payload, lengthEncodedString(value)...)
		}
		if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, payload)
	}
	return writeBoundedPacket(connection, sequence, eofPacket(), maximum)
}

func writeBinaryResult(connection net.Conn, sequence byte, columns []string, rows [][]string, nulls [][]bool, metadata []columnMetadata, maximum int64) error {
	count := lengthEncodedInt(len(columns))
	if err := writeBoundedPacket(connection, sequence, count, maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, count)
	definitions := make([]columnMetadata, len(columns))
	for index, name := range columns {
		definition := columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
		if index < len(metadata) {
			definition = metadata[index]
		}
		definitions[index] = definition
		payload := columnDefinition(definition)
		if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, payload)
	}
	if err := writeBoundedPacket(connection, sequence, eofPacket(), maximum); err != nil {
		return err
	}
	sequence = nextPacketSequence(sequence, eofPacket())
	for rowIndex, row := range rows {
		payload, err := binaryRow(row, rowIndex, nulls, definitions)
		if err != nil {
			return err
		}
		if err := writeBoundedPacket(connection, sequence, payload, maximum); err != nil {
			return err
		}
		sequence = nextPacketSequence(sequence, payload)
	}
	return writeBoundedPacket(connection, sequence, eofPacket(), maximum)
}

func binaryRow(row []string, rowIndex int, nulls [][]bool, metadata []columnMetadata) ([]byte, error) {
	payload := make([]byte, 1+(len(metadata)+9)/8)
	for index, value := range row {
		if rowIndex < len(nulls) && index < len(nulls[rowIndex]) && nulls[rowIndex][index] {
			payload[1+(index+2)/8] |= 1 << uint((index+2)%8)
			continue
		}
		definition := columnMetadata{typ: mysqlTypeVarString}
		if index < len(metadata) {
			definition = metadata[index]
		}
		switch definition.typ {
		case mysqlTypeLongLong:
			encoded := make([]byte, 8)
			if definition.flags&mysqlUnsignedFlag != 0 {
				value, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return nil, err
				}
				binary.LittleEndian.PutUint64(encoded, value)
			} else {
				value, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, err
				}
				binary.LittleEndian.PutUint64(encoded, uint64(value))
			}
			payload = append(payload, encoded...)
		case mysqlTypeLong:
			value, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return nil, err
			}
			encoded := make([]byte, 4)
			binary.LittleEndian.PutUint32(encoded, uint32(value))
			payload = append(payload, encoded...)
		case mysqlTypeDouble:
			value, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, err
			}
			encoded := make([]byte, 8)
			binary.LittleEndian.PutUint64(encoded, math.Float64bits(value))
			payload = append(payload, encoded...)
		default:
			payload = append(payload, lengthEncodedString(value)...)
		}
	}
	return payload, nil
}

func columnDefinition(definition columnMetadata) []byte {
	payload := append(lengthEncodedString(definition.catalog), lengthEncodedString(definition.schema)...)
	payload = append(payload, lengthEncodedString(definition.table)...)
	payload = append(payload, lengthEncodedString(definition.originalTable)...)
	payload = append(payload, lengthEncodedString(definition.name)...)
	payload = append(payload, lengthEncodedString(definition.originalName)...)
	payload = append(payload, 0x0c, byte(definition.characterSet), byte(definition.characterSet>>8), byte(definition.length), byte(definition.length>>8), byte(definition.length>>16), byte(definition.length>>24), definition.typ, byte(definition.flags), byte(definition.flags>>8), definition.decimals, 0, 0)
	return payload
}
func eofPacket() []byte { return []byte{0xfe, 0, 0, 2, 0} }
func lengthEncodedInt(value int) []byte {
	if value < 251 {
		return []byte{byte(value)}
	}
	if value <= 0xffff {
		return []byte{0xfc, byte(value), byte(value >> 8)}
	}
	if value <= 0xffffff {
		return []byte{0xfd, byte(value), byte(value >> 8), byte(value >> 16)}
	}
	return []byte{0xfe, byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24), byte(value >> 32), byte(value >> 40), byte(value >> 48), byte(value >> 56)}
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
	} else if length == 0xfd {
		if offset+3 > len(payload) {
			return nil, offset, false
		}
		length = int(payload[offset]) | int(payload[offset+1])<<8 | int(payload[offset+2])<<16
		offset += 3
	} else if length == 0xfe {
		if offset+8 > len(payload) {
			return nil, offset, false
		}
		length64 := binary.LittleEndian.Uint64(payload[offset : offset+8])
		if length64 > uint64(len(payload)-offset-8) {
			return nil, offset, false
		}
		length = int(length64)
		offset += 8
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

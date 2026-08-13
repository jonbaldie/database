package mysql

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// conversation owns one accepted connection from greeting through cleanup.
// Command behaviour remains in the established query and prepared collaborators.
type conversation struct {
	server            *Server
	accepted          net.Conn
	connection        net.Conn
	session           *session
	queries           *queryExecutor
	preparation       *preparedPreparation
	execution         *preparedExecution
	preparedLifecycle *preparedLifecycle
	admitted          bool
	connectionID      uint32
	control           *conversationControl
}

type conversationControl struct {
	watch       *statementWatch
	watchMu     sync.Mutex
	revoked     atomic.Bool
	running     atomic.Bool
	activeQuery atomic.Value
}

type pendingCommand struct {
	sequence byte
	payload  []byte
}

type statementWatch struct {
	finished   chan *pendingCommand
	cancelled  chan struct{}
	cancelOnce sync.Once
}

const (
	comQuit             byte = 0x01
	comInitDB           byte = 0x02
	comQuery            byte = 0x03
	comPing             byte = 0x0e
	comStmtPrepare      byte = 0x16
	comStmtExecute      byte = 0x17
	comStmtSendLongData byte = 0x18
	comStmtClose        byte = 0x19
	comStmtReset        byte = 0x1a
	comResetConnection  byte = 0x1f
)

func newConversation(server *Server, connection net.Conn) *conversation {
	conversation := &conversation{server: server, accepted: connection, connection: connection,
		control: &conversationControl{}}
	conversation.control.activeQuery.Store("")
	return conversation
}

func (c *conversation) serve() {
	defer c.close()
	if !c.authenticate() {
		return
	}
	for c.acceptCommand() {
	}
}

func (c *conversation) close() {
	c.server.connections.unregisterConversation(c)
	if c.session != nil {
		_ = rollbackTransaction(c.session)
	}
	if c.preparedLifecycle != nil {
		c.preparedLifecycle.closeAllPrepared()
	}
	_ = c.accepted.Close()
	c.server.connections.unregister(c.accepted, c.admitted)
}

func (c *conversation) authenticate() bool {
	nonce := makeNonce()
	c.connectionID = c.server.connections.allocateConnectionID()
	if err := writePacket(c.connection, 0, handshake(c.server.config.Version, nonce, c.server.auth.tlsConfig != nil, c.connectionID)); err != nil {
		return false
	}
	authentication, err := c.server.auth.authenticate(c.connection, nonce)
	if err != nil {
		_ = writePacket(authentication.connection, authentication.nextSequence, mysqlError(err))
		return false
	}
	c.connection = authentication.connection
	if !c.server.connections.admitSession() {
		_ = writePacket(c.connection, authentication.nextSequence, errorPacket(1040, "08004", "too many connections"))
		return false
	}
	c.admitted = true
	if err := writePacket(c.connection, authentication.nextSequence, okPacket()); err != nil {
		return false
	}
	c.session = newSession(c.server, authentication, c.connectionID)
	c.server.connections.registerConversation(c)
	c.queries = newQueryExecutor(c.session)
	c.preparation = &preparedPreparation{c.session}
	c.execution = &preparedExecution{c.session}
	c.preparedLifecycle = &preparedLifecycle{c.session}
	return true
}

func newSession(server *Server, authentication authenticationResult, connectionID uint32) *session {
	return &session{
		server: server, connectionID: connectionID, username: authentication.accountName, database: authentication.database,
		initialDB: authentication.database, timeZone: server.config.TimeZone, initialTimeZone: server.config.TimeZone,
		settings: sessionSettings{collationConnection: collation0900AICI, statementTimeout: server.config.ResourceLimits.StatementTimeout,
			lockWaitTimeout: server.config.LockWaitTimeout, executionMemoryLimit: server.config.ResourceLimits.ExecutionMemoryLimitBytes,
			temporaryStorageLimit: server.config.ResourceLimits.TemporaryStorageLimitBytes},
		statements: map[uint32]*preparedStatement{}, prepared: preparedCounters{nextStmtID: 1},
		transactionState: transactionState{},
	}
}

func (c *conversation) acceptCommand() bool {
	if c.control.revoked.Load() {
		return false
	}
	if watch := c.control.takeWatch(); watch != nil {
		pending := <-watch.finished
		if pending == nil {
			return false
		}
		return c.dispatch(pending.sequence, pending.payload)
	}
	sequence, payload, err := readPacket(c.connection, c.server.config.MaxAllowedPacket)
	if err != nil || len(payload) == 0 || !c.server.connections.acceptingWork() {
		return false
	}
	return c.dispatch(sequence+1, payload)
}

func (c *conversation) dispatch(sequence byte, payload []byte) bool {
	handler, found := c.handlers()[payload[0]]
	if !found {
		return c.write(sequence, errorPacket(1047, "08S01", "unsupported command"))
	}
	return handler(sequence, payload)
}

type commandHandler func(sequence byte, payload []byte) bool

func (c *conversation) handlers() map[byte]commandHandler {
	return map[byte]commandHandler{
		comQuit: c.quit, comInitDB: c.initDatabase, comQuery: c.query, comPing: c.ping,
		comStmtPrepare: c.prepare, comStmtExecute: c.executePrepared, comStmtSendLongData: c.sendLongData,
		comStmtClose: c.closePrepared, comStmtReset: c.resetPrepared, comResetConnection: c.resetConnection,
	}
}

func (c *conversation) quit(byte, []byte) bool { return false }

func (c *conversation) initDatabase(sequence byte, payload []byte) bool {
	c.queries.useDatabase(string(payload[1:]))
	if err := c.queries.databaseExists(c.session.database); err != nil {
		return c.write(sequence, mysqlError(err))
	}
	return c.write(sequence, okPacket())
}

func (c *conversation) query(sequence byte, payload []byte) bool {
	query := string(payload[1:])
	return c.runStatement(query, func() error {
		return c.queries.writeQueryResult(c.connection, sequence, query)
	})
}

func (c *conversation) ping(sequence byte, _ []byte) bool { return c.write(sequence, okPacket()) }

func (c *conversation) prepare(sequence byte, payload []byte) bool {
	return c.preparation.prepare(c.connection, sequence, string(payload[1:])) == nil
}

func (c *conversation) executePrepared(sequence byte, payload []byte) bool {
	return c.runStatement("", func() error {
		return c.execution.executePrepared(c.connection, sequence, payload)
	})
}

func (c *conversation) runStatement(query string, run func() error) bool {
	if !c.server.connections.beginStatement() {
		return false
	}
	c.control.running.Store(true)
	defer c.control.running.Store(false)
	c.control.activeQuery.Store(query)
	defer c.control.activeQuery.Store("")
	finishExplanation := c.recordActiveExplanation(query)
	defer finishExplanation()
	watch := c.watchStatement()
	c.control.setWatch(watch)
	c.session.statementCancel = watch.cancelled
	config := c.server.config
	config.ResourceLimits = c.session.resourceLimits()
	resources := newStatementResources(c.server.resources, config, watch.cancelled)
	c.session.resources = resources
	defer func() {
		recordRuntimeResources(c.session.runtimeMetrics, statementResourceSnapshot(resources))
		closeStatementResources(resources)
		if c.session.resources == resources {
			c.session.resources = nil
		}
		c.session.statementCancel = nil
	}()
	err := run()
	c.server.connections.endStatement()
	return err == nil && !c.control.revoked.Load()
}

func (c *conversationControl) setWatch(watch *statementWatch) {
	c.watchMu.Lock()
	c.watch = watch
	c.watchMu.Unlock()
}

func (c *conversationControl) takeWatch() *statementWatch {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	watch := c.watch
	c.watch = nil
	return watch
}

func (c *conversationControl) cancelStatement() {
	c.watchMu.Lock()
	watch := c.watch
	c.watchMu.Unlock()
	if watch != nil {
		watch.cancelOnce.Do(func() { close(watch.cancelled) })
	}
}

func (c *conversation) recordActiveExplanation(query string) func() {
	if c.session == nil {
		return func() {}
	}
	statement, err := normalizeStatement(query)
	if err != nil {
		return func() {}
	}
	started := time.Now()
	planner := textStatementExecutor{session: c.session}
	plan, err := planner.planExplanation(statement.query)
	if err != nil {
		return func() {}
	}
	plan.Timing.PlanningMS = float64(time.Since(started)) / float64(time.Millisecond)
	return c.server.explanations.begin(c.session.connectionID, plan, c.session)
}

func (c *conversation) watchStatement() *statementWatch {
	watch := &statementWatch{
		finished:  make(chan *pendingCommand, 1),
		cancelled: make(chan struct{}),
	}
	go func() {
		sequence, payload, err := readPacket(c.connection, c.server.config.MaxAllowedPacket)
		if err == nil && len(payload) > 0 {
			watch.cancelOnce.Do(func() { close(watch.cancelled) })
			watch.finished <- &pendingCommand{sequence: sequence + 1, payload: payload}
			return
		}
		watch.cancelOnce.Do(func() { close(watch.cancelled) })
		watch.finished <- nil
	}()
	return watch
}

func (c *conversation) closePrepared(sequence byte, payload []byte) bool {
	if len(payload) != 5 {
		return c.write(sequence, errorPacket(1210, "HY000", "malformed prepared statement close"))
	}
	c.preparedLifecycle.closePrepared(binary.LittleEndian.Uint32(payload[1:5]))
	return true
}

func (c *conversation) sendLongData(sequence byte, payload []byte) bool {
	if err := c.preparedLifecycle.sendLongData(payload); err != nil {
		return c.write(sequence, mysqlError(err))
	}
	return true
}

func (c *conversation) resetPrepared(sequence byte, payload []byte) bool {
	if err := c.preparedLifecycle.resetPrepared(payload); err != nil {
		return c.write(sequence, mysqlError(err))
	}
	return c.write(sequence, okPacket())
}

func (c *conversation) resetConnection(sequence byte, payload []byte) bool {
	if len(payload) != 1 {
		return c.write(sequence, errorPacket(1210, "HY000", "malformed connection reset"))
	}
	if err := c.preparedLifecycle.resetConnection(); err != nil {
		return c.write(sequence, mysqlError(err))
	}
	return c.write(sequence, okPacket())
}

func (c *conversation) write(sequence byte, payload []byte) bool {
	return writePacket(c.connection, sequence, payload) == nil
}

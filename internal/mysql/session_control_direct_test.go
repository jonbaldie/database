package mysql

import (
	"net"
	"reflect"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

type controlTestConn struct {
	net.Conn
	closed bool
}

func (c *controlTestConn) Close() error { c.closed = true; return c.Conn.Close() }

func controlTestConversation(t *testing.T, server *Server, id uint32, name string) (*conversation, *controlTestConn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	conn := &controlTestConn{Conn: a}
	c := newConversation(server, conn)
	c.connectionID = id
	c.session = &session{server: server, connectionID: id, username: name, database: "app"}
	server.connections.registerConversation(c)
	return c, conn
}

func controlTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Server{config: Config{Catalog: store}, connections: &connectionRegistry{sessions: make(map[string]map[*conversation]struct{})}, explanations: newActiveExplanationRegistry()}
}

func TestDirectSessionAuthority(t *testing.T) {
	s := controlTestServer(t)
	for _, a := range []catalog.Account{
		{Name: "granted", Grants: []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION"}, {Privilege: "OPERATIONAL_CONTROL"}}},
		{Name: "locked", Locked: true, Grants: []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION"}}},
		{Name: "scoped", Grants: []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION", Namespace: "app"}}},
		{Name: "other", Grants: []catalog.Grant{{Privilege: "OPERATIONAL_CONTROL"}}},
	} {
		a.PasswordHash = "hash"
		if err := s.config.Catalog.CreateAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, privilege string
		all             bool
	}{
		{"", "OPERATIONAL_OBSERVATION", true}, {"granted", "OPERATIONAL_OBSERVATION", true},
		{"granted", "OPERATIONAL_CONTROL", true}, {"locked", "OPERATIONAL_OBSERVATION", false},
		{"scoped", "OPERATIONAL_OBSERVATION", false}, {"other", "OPERATIONAL_OBSERVATION", false},
		{"missing", "OPERATIONAL_OBSERVATION", false},
	} {
		t.Run(tc.name+tc.privilege, func(t *testing.T) {
			a := sessionAuthorityFor(&session{server: s, username: tc.name}, tc.privilege)
			if a.username != tc.name || a.all != tc.all || !a.permits(tc.name) || a.permits("unrelated") != tc.all {
				t.Fatalf("authority = %+v", a)
			}
		})
	}
	a := sessionAuthorityFor(&session{server: &Server{}, username: "alice"}, "OPERATIONAL_CONTROL")
	if a.all || !a.permits("alice") || a.permits("bob") {
		t.Fatalf("no catalog: %+v", a)
	}
}

func TestDirectSessionObservationAndExplanation(t *testing.T) {
	s := controlTestServer(t)
	alice, _ := controlTestConversation(t, s, 11, "alice")
	second, _ := controlTestConversation(t, s, 12, "alice")
	bob, _ := controlTestConversation(t, s, 13, "bob")
	alice.control.running.Store(true)
	alice.control.activeQuery.Store("select 42")
	snapshots := s.connections.observe(alice.session)
	got := map[uint32]sessionSnapshot{}
	for _, snapshot := range snapshots {
		got[snapshot.id] = snapshot
	}
	if len(got) != 2 || got[11] != (sessionSnapshot{id: 11, username: "alice", database: "app", running: true, query: "select 42"}) || got[12] != (sessionSnapshot{id: 12, username: "alice", database: "app"}) {
		t.Fatalf("snapshots = %#v", got)
	}
	for _, tc := range []struct {
		id   uint32
		want *conversation
	}{{11, alice}, {12, second}, {13, bob}, {99, nil}} {
		c, ok := s.connections.sessionByID(tc.id)
		if c != tc.want || ok != (tc.want != nil) {
			t.Fatalf("lookup %d = %p, %v", tc.id, c, ok)
		}
	}
	for _, tc := range []struct {
		id uint32
		ok bool
	}{{11, true}, {12, true}, {13, false}, {99, false}} {
		view, ok := s.connections.observeOne(alice.session, tc.id)
		if ok != tc.ok || (ok && view.conversation.connectionID != tc.id) {
			t.Fatalf("observe %d = %+v, %v", tc.id, view, ok)
		}
	}
	executor := &textStatementExecutor{session: alice.session}
	result := executor.showProcessList()
	rows := map[string][]string{}
	for _, row := range result.rows {
		rows[row[0]] = row
	}
	if !reflect.DeepEqual(result.columns, []string{"Id", "User", "Host", "db", "Command", "Time", "State", "Info"}) || !reflect.DeepEqual(rows["11"], []string{"11", "alice", "", "app", "Query", "0", "", "select 42"}) || !reflect.DeepEqual(rows["12"], []string{"12", "alice", "", "app", "Sleep", "0", "", ""}) || len(rows) != 2 {
		t.Fatalf("processlist = %#v", result)
	}
	metadata := informationSchemaProcessListRows(alice.session)
	metadataRows := map[string][]metadataValue{}
	for _, row := range metadata {
		metadataRows[row[0].value] = row
	}
	if len(metadataRows) != 2 || metadataRows["11"][4].value != "Query" || metadataRows["11"][7].value != "select 42" || metadataRows["12"][4].value != "Sleep" {
		t.Fatalf("metadata processlist = %#v", metadataRows)
	}
	for _, invalid := range []*session{nil, {}, {server: &Server{}}} {
		if rows := informationSchemaProcessListRows(invalid); rows != nil {
			t.Fatalf("invalid processlist = %#v", rows)
		}
	}
	plan := &queryexplanation.Document{Statement: queryexplanation.Statement{SQL: "select 42"}, Plan: &queryexplanation.Operator{}}
	end := s.explanations.begin(11, plan, alice.session)
	defer end()
	endBob := s.explanations.begin(13, plan, bob.session)
	defer endBob()
	view, ok := s.connections.observeOne(alice.session, 11)
	if !ok {
		t.Fatal("cannot observe own active session")
	}
	document, ok := view.explanation()
	if !ok || document.Snapshot == nil || document.Snapshot.ConnectionID != 11 || document.Statement.SQL != "select 42" {
		t.Fatalf("explanation = %#v, %v", document, ok)
	}
	if _, ok := s.explanations.snapshot(&session{server: s, connectionID: 11}); ok {
		t.Fatal("different session received explanation")
	}
	for _, id := range []uint32{12, 13, 99} {
		if _, err := executor.snapshotExplanation("JSON", id); err != (sqlFailure{1094, "HY000", "unknown or inactive connection ID"}) {
			t.Fatalf("explanation %d: %v", id, err)
		}
	}
	if _, err := executor.snapshotExplanation("JSON", 11); err != nil {
		t.Fatalf("active explanation: %v", err)
	}
}

func TestDirectKillParsing(t *testing.T) {
	for _, tc := range []struct {
		query string
		kind  terminationKind
		id    uint32
		fail  bool
	}{
		{"KILL 1", terminateConnection, 1, false}, {" kill QuErY 4294967295 ", terminateQuery, 4294967295, false},
		{"KILL CONNECTION 2", terminateConnection, 2, false}, {"KILL QUERY 0", terminateQuery, 0, true},
		{"KILL -1", terminateConnection, 0, true}, {"KILL 4294967296", terminateConnection, 0, true},
		{"KILL", terminateConnection, 0, true}, {"KILL QUERY", terminateConnection, 0, true},
		{"KILL OTHER 1", terminateConnection, 0, true}, {"KILL QUERY 1 extra", terminateConnection, 0, true},
	} {
		kind, id, err := parseKill(tc.query)
		if kind != tc.kind || id != tc.id || (err != nil) != tc.fail {
			t.Errorf("parse %q = %v, %d, %v", tc.query, kind, id, err)
		}
	}
	for _, query := range []string{"KILL", "KILL OTHER 1", "KILL QUERY 1 extra"} {
		if _, _, err := parseKill(query); err != (sqlFailure{1064, "42000", "malformed KILL statement"}) {
			t.Errorf("parse %q: %v", query, err)
		}
	}
}

func TestDirectTermination(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		kind                                terminationKind
		running, closed, revoked, cancelled bool
	}{
		{"query", terminateQuery, true, false, false, true}, {"connection idle", terminateConnection, false, true, true, false},
		{"connection running", terminateConnection, true, true, true, false}, {"account idle", terminateAfterStatement, false, true, true, false},
		{"account running", terminateAfterStatement, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := controlTestServer(t)
			c, conn := controlTestConversation(t, s, 1, "alice")
			c.control.running.Store(tc.running)
			watch := &statementWatch{cancelled: make(chan struct{})}
			c.control.setWatch(watch)
			if err := s.connections.terminateSession(c.session, 1, tc.kind); err != nil {
				t.Fatal(err)
			}
			cancelled := false
			select {
			case <-watch.cancelled:
				cancelled = true
			default:
			}
			if conn.closed != tc.closed || c.control.revoked.Load() != tc.revoked || cancelled != tc.cancelled {
				t.Fatalf("closed=%v revoked=%v cancelled=%v", conn.closed, c.control.revoked.Load(), cancelled)
			}
		})
	}
	s := controlTestServer(t)
	alice, _ := controlTestConversation(t, s, 1, "alice")
	bob, conn := controlTestConversation(t, s, 2, "bob")
	for _, tc := range []struct {
		id   uint32
		want sqlFailure
	}{
		{2, sqlFailure{1094, "HY000", "access denied"}},
		{99, sqlFailure{1094, "HY000", "unknown thread id"}},
	} {
		if err := s.connections.terminateSession(alice.session, tc.id, terminateConnection); err != tc.want {
			t.Fatalf("kill %d: %v, want %v", tc.id, err, tc.want)
		}
	}
	if conn.closed || bob.control.revoked.Load() {
		t.Fatal("unauthorized termination changed target")
	}
	executor := &textStatementExecutor{session: alice.session}
	if err := executor.killSession("KILL invalid"); err != (sqlFailure{1094, "HY000", "unknown thread id"}) {
		t.Fatalf("invalid kill: %v", err)
	}
	if err := executor.killSession("KILL 2"); err != (sqlFailure{1094, "HY000", "access denied"}) {
		t.Fatalf("unauthorized kill: %v", err)
	}
	if err := executor.killSession("KILL QUERY 1"); err != nil {
		t.Fatal(err)
	}
}

func TestDirectExplanationRegistryBoundaries(t *testing.T) {
	s := controlTestServer(t)
	owner, _ := controlTestConversation(t, s, 1, "alice")
	plan := &queryexplanation.Document{Statement: queryexplanation.Statement{SQL: "select 1"}, Plan: &queryexplanation.Operator{}}
	var absent *activeExplanationRegistry
	absent.begin(1, plan, owner.session)()
	if _, ok := absent.snapshot(owner.session); ok {
		t.Fatal("nil registry returned an explanation")
	}
	for _, session := range []*session{nil, {server: s}} {
		if _, ok := s.explanations.snapshot(session); ok {
			t.Fatal("invalid session received an explanation")
		}
	}
	s.explanations.begin(0, plan, owner.session)()
	s.explanations.begin(1, nil, owner.session)()
	if _, ok := s.explanations.snapshot(owner.session); ok {
		t.Fatal("invalid plan was registered")
	}
	end := s.explanations.begin(1, plan, owner.session)
	if document, ok := s.explanations.snapshot(owner.session); !ok || document.Statement.SQL != "select 1" {
		t.Fatalf("connection 1 explanation = %#v, %v", document, ok)
	}
	end()
	if _, ok := s.explanations.snapshot(owner.session); ok {
		t.Fatal("finished explanation remained registered")
	}
}

func TestDirectCrossAccountControl(t *testing.T) {
	s := controlTestServer(t)
	for _, account := range []catalog.Account{
		{Name: "controller", PasswordHash: "hash", Grants: []catalog.Grant{{Privilege: "OPERATIONAL_CONTROL"}}},
		{Name: "observer", PasswordHash: "hash", Grants: []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION"}}},
	} {
		if err := s.config.Catalog.CreateAccount(account); err != nil {
			t.Fatal(err)
		}
	}
	controller, _ := controlTestConversation(t, s, 1, "controller")
	observer, _ := controlTestConversation(t, s, 2, "observer")
	target, targetConn := controlTestConversation(t, s, 3, "bob")
	watch := &statementWatch{cancelled: make(chan struct{})}
	target.control.setWatch(watch)
	if err := s.connections.terminateSession(observer.session, 3, terminateConnection); err != (sqlFailure{1094, "HY000", "access denied"}) {
		t.Fatalf("observer termination: %v", err)
	}
	if targetConn.closed || target.control.revoked.Load() {
		t.Fatal("observation privilege terminated another account")
	}
	if err := s.connections.terminateSession(controller.session, 3, terminateQuery); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watch.cancelled:
	default:
		t.Fatal("control privilege did not cancel another account's query")
	}
}

func TestDirectRevokeAccount(t *testing.T) {
	s := controlTestServer(t)
	idle, idleConn := controlTestConversation(t, s, 1, "alice")
	active, activeConn := controlTestConversation(t, s, 2, "alice")
	other, otherConn := controlTestConversation(t, s, 3, "bob")
	active.control.running.Store(true)
	s.connections.revokeAccount("alice")
	if !idle.control.revoked.Load() || !idleConn.closed || !active.control.revoked.Load() || activeConn.closed || other.control.revoked.Load() || otherConn.closed {
		t.Fatal("account termination affected wrong sessions")
	}
	s.connections.revokeAccount("missing")
}

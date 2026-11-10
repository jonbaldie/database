package mysql

import (
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/queryexplanation"
)

type sessionSnapshot struct {
	id       uint32
	username string
	database string
	running  bool
	query    string
}

func (r *connectionRegistry) sessionSnapshots() []sessionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]sessionSnapshot, 0, r.sessionCount)
	for _, sessions := range r.sessions {
		for conversation := range sessions {
			query, _ := conversation.control.activeQuery.Load().(string)
			database := ""
			if conversation.session != nil {
				database = conversation.session.database
			}
			result = append(result, sessionSnapshot{id: conversation.connectionID,
				username: conversation.session.username, database: database, running: conversation.control.running.Load(), query: query})
		}
	}
	return result
}

func (r *connectionRegistry) sessionByID(id uint32) (*conversation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sessions := range r.sessions {
		for conversation := range sessions {
			if conversation.connectionID == id {
				return conversation, true
			}
		}
	}
	return nil, false
}

func (r *connectionRegistry) observe(observer *session) []sessionSnapshot {
	authority := sessionAuthorityFor(observer, "OPERATIONAL_OBSERVATION")
	snapshots := r.sessionSnapshots()
	visible := make([]sessionSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if authority.permits(snapshot.username) {
			visible = append(visible, snapshot)
		}
	}
	return visible
}

type sessionView struct {
	conversation *conversation
}

func (r *connectionRegistry) observeOne(observer *session, id uint32) (sessionView, bool) {
	target, found := r.sessionByID(id)
	if !found || !sessionAuthorityFor(observer, "OPERATIONAL_OBSERVATION").permits(target.session.username) {
		return sessionView{}, false
	}
	return sessionView{conversation: target}, true
}

func (view sessionView) explanation() (*queryexplanation.Document, bool) {
	target := view.conversation.session
	return target.server.explanations.snapshot(target)
}

func (s *textStatementExecutor) sessionControlStatement(query, lower string) (*queryResult, bool, error) {
	if lower == "show processlist" || lower == "show full processlist" {
		return s.showProcessList(), true, nil
	}
	if strings.HasPrefix(lower, "kill ") {
		return nil, true, s.killSession(query)
	}
	return nil, false, nil
}

func (s *textStatementExecutor) showProcessList() *queryResult {
	rows := make([][]string, 0)
	for _, snapshot := range s.server.connections.observe(s.session) {
		command, info := "Sleep", ""
		if snapshot.running {
			command, info = "Query", snapshot.query
		}
		rows = append(rows, []string{strconv.FormatUint(uint64(snapshot.id), 10), snapshot.username, "", snapshot.database, command, "0", "", info})
	}
	return &queryResult{columns: []string{"Id", "User", "Host", "db", "Command", "Time", "State", "Info"}, rows: rows}
}

func (s *textStatementExecutor) killSession(query string) error {
	kind, id, err := parseKill(query)
	if err != nil {
		return err
	}
	return s.server.connections.terminateSession(s.session, id, kind)
}

type terminationKind uint8

const (
	terminateQuery terminationKind = iota
	terminateConnection
	terminateAfterStatement
)

func (r *connectionRegistry) terminateSession(observer *session, id uint32, kind terminationKind) error {
	target, found := r.sessionByID(id)
	if !found {
		return sqlFailure{1094, "HY000", "unknown thread id"}
	}
	if !sessionAuthorityFor(observer, "OPERATIONAL_CONTROL").permits(target.session.username) {
		return sqlFailure{1094, "HY000", "access denied"}
	}
	r.terminate(target, kind)
	return nil
}

func (r *connectionRegistry) terminate(target *conversation, kind terminationKind) {
	if kind == terminateQuery {
		target.control.cancelStatement()
		return
	}
	target.control.revoked.Store(true)
	if kind != terminateAfterStatement || !target.control.running.Load() {
		_ = target.connection.Close()
	}
}

func parseKill(query string) (terminationKind, uint32, error) {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 2 {
		id, err := parseConnectionID(fields[1])
		return terminateConnection, id, err
	}
	if len(fields) == 3 && (strings.EqualFold(fields[1], "query") || strings.EqualFold(fields[1], "connection")) {
		id, err := parseConnectionID(fields[2])
		if strings.EqualFold(fields[1], "query") {
			return terminateQuery, id, err
		}
		return terminateConnection, id, err
	}
	return terminateConnection, 0, sqlFailure{1064, "42000", "malformed KILL statement"}
}

func parseConnectionID(value string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id == 0 {
		return 0, sqlFailure{1094, "HY000", "unknown thread id"}
	}
	return uint32(id), nil
}

type sessionAuthority struct {
	username string
	all      bool
}

func sessionAuthorityFor(observer *session, privilege string) sessionAuthority {
	authority := sessionAuthority{username: observer.username, all: observer.username == ""}
	if !authority.all && observer.server.config.Catalog != nil {
		account, found := observer.server.config.Catalog.Account(observer.username)
		authority.all = found && !account.Locked && accountGrantIndex(account, privilege, "") >= 0
	}
	return authority
}

func (authority sessionAuthority) permits(username string) bool {
	return authority.all || authority.username == username
}

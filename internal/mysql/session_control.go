package mysql

import (
	"strconv"
	"strings"
)

type sessionSnapshot struct {
	conversation *conversation
	id           uint32
	username     string
	database     string
	running      bool
	query        string
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
			result = append(result, sessionSnapshot{conversation: conversation, id: conversation.connectionID,
				username: conversation.session.username, database: database, running: conversation.control.running.Load(), query: query})
		}
	}
	return result
}

func (r *connectionRegistry) sessionByID(id uint32) (sessionSnapshot, bool) {
	for _, snapshot := range r.sessionSnapshots() {
		if snapshot.id == id {
			return snapshot, true
		}
	}
	return sessionSnapshot{}, false
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
	for _, snapshot := range s.server.connections.sessionSnapshots() {
		if !s.canObserve(snapshot.username) {
			continue
		}
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
	target, found := s.server.connections.sessionByID(id)
	if !found {
		return sqlFailure{1094, "HY000", "unknown thread id"}
	}
	if !s.canControl(target.username) {
		return sqlFailure{1094, "HY000", "access denied"}
	}
	if kind == "query" {
		target.conversation.control.cancelStatement()
		return nil
	}
	target.conversation.control.revoked.Store(true)
	_ = target.conversation.connection.Close()
	return nil
}

func parseKill(query string) (string, uint32, error) {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 2 {
		id, err := parseConnectionID(fields[1])
		return "connection", id, err
	}
	if len(fields) == 3 && (strings.EqualFold(fields[1], "query") || strings.EqualFold(fields[1], "connection")) {
		id, err := parseConnectionID(fields[2])
		return strings.ToLower(fields[1]), id, err
	}
	return "", 0, sqlFailure{1064, "42000", "malformed KILL statement"}
}

func parseConnectionID(value string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id == 0 {
		return 0, sqlFailure{1094, "HY000", "unknown thread id"}
	}
	return uint32(id), nil
}

func (s *textStatementExecutor) canObserve(username string) bool {
	return s.canOperate(username, "OPERATIONAL_OBSERVATION")
}

func (s *textStatementExecutor) canControl(username string) bool {
	return s.canOperate(username, "OPERATIONAL_CONTROL")
}

func (s *textStatementExecutor) canOperate(username, privilege string) bool {
	if s.session.username == "" || username == s.session.username {
		return true
	}
	if s.session.server.config.Catalog == nil {
		return false
	}
	account, found := s.session.server.config.Catalog.Account(s.session.username)
	return found && !account.Locked && accountGrantIndex(account, privilege, "") >= 0
}

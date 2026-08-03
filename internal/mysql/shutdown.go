package mysql

import (
	"strings"
	"time"
)

func (s *textStatementExecutor) shutdownStatement(query, lower string) (*queryResult, bool, error) {
	operationID, ok := parseShutdownStatement(query, lower)
	if !ok {
		return nil, false, nil
	}
	if err := s.requireOperationalControl(); err != nil {
		return nil, true, err
	}
	requestedAt := time.Now().UTC()
	s.session.server.RequestShutdown(operationID)
	instanceID := s.session.server.config.Instance.InstanceID
	if instanceID == "" {
		instanceID = "unknown"
	}
	return &queryResult{
		columns: []string{"instance_id", "requested_at"},
		rows:    [][]string{{instanceID, requestedAt.Format(time.RFC3339Nano)}},
	}, true, nil
}

func parseShutdownStatement(query, lower string) (string, bool) {
	if lower == "shutdown" {
		return "", true
	}
	if !strings.HasPrefix(lower, "shutdown ") {
		return "", false
	}
	argument := strings.TrimSpace(query[len("shutdown "):])
	if len(argument) < 2 || argument[0] != '\'' || argument[len(argument)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(argument[1:len(argument)-1], "''", "'"), true
}

func (s *Server) RequestShutdown(operationID string) {
	s.shutdownOnce.Do(func() {
		s.shutdownOperationID = operationID
		close(s.shutdown)
	})
}

func (s *Server) ShutdownRequested() <-chan struct{} {
	return s.shutdown
}

func (s *Server) ShutdownOperationID() string {
	return s.shutdownOperationID
}

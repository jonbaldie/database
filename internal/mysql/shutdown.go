package mysql

import (
	"time"
)

func (s *textStatementExecutor) shutdownStatement(lower string) (*queryResult, bool, error) {
	if lower != "shutdown" {
		return nil, false, nil
	}
	if err := s.requireOperationalControl(); err != nil {
		return nil, true, err
	}
	requestedAt := time.Now().UTC()
	s.session.server.RequestShutdown()
	instanceID := s.session.server.config.Instance.InstanceID
	if instanceID == "" {
		instanceID = "unknown"
	}
	return &queryResult{
		columns: []string{"instance_id", "requested_at"},
		rows:    [][]string{{instanceID, requestedAt.Format(time.RFC3339Nano)}},
	}, true, nil
}

func (s *Server) RequestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdown) })
}

func (s *Server) ShutdownRequested() <-chan struct{} {
	return s.shutdown
}

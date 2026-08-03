package mysql

import (
	"encoding/json"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
)

func (s *textStatementExecutor) backupInstanceStatement(lower string) (*queryResult, bool, error) {
	if lower != "backup instance" {
		return nil, false, nil
	}
	if err := s.requireOperationalControl(); err != nil {
		return nil, true, err
	}
	files, err := s.captureInstanceBackup()
	if err != nil {
		return nil, true, err
	}
	rows := make([][]string, 0, len(files))
	for _, path := range []string{"instance.json", "catalog.json"} {
		rows = append(rows, []string{path, string(files[path])})
	}
	return &queryResult{columns: []string{"path", "content"}, rows: rows}, true, nil
}

func (s *textStatementExecutor) requireOperationalControl() error {
	if s.session.username == "" || s.session.server.config.Catalog == nil {
		return sqlFailure{1227, "42000", "access denied"}
	}
	account, found := s.session.server.config.Catalog.Account(s.session.username)
	if !found || account.Locked || !accountHasGrant(account, "OPERATIONAL_CONTROL") {
		return sqlFailure{1227, "42000", "access denied"}
	}
	return nil
}

func (s *textStatementExecutor) captureInstanceBackup() (map[string][]byte, error) {
	metadata := s.session.server.config.Instance
	if metadata.Schema == "" {
		metadata = instance.Metadata{
			Schema: "database.instance/v1", InstanceID: "unknown", State: "stopped",
			AdminAccount: s.session.server.config.Username, PasswordHash: s.session.server.config.PasswordHash,
		}
	}
	if metadata.DataVersion == "" {
		metadata.DataVersion = instance.CurrentDataVersion
	}
	metadata.State = "stopped"
	instanceBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, sqlFailure{1684, "HY000", "backup capture failed"}
	}
	instanceBytes = append(instanceBytes, '\n')
	catalogBytes, err := catalog.Encode(s.session.server.config.Catalog.Snapshot())
	if err != nil {
		return nil, sqlFailure{1684, "HY000", "backup capture failed"}
	}
	return map[string][]byte{"instance.json": instanceBytes, "catalog.json": catalogBytes}, nil
}

func (s *textStatementExecutor) operationStatement(query, lower string) (*queryResult, bool, error) {
	if strings.HasPrefix(lower, "explain ") {
		result, err := s.explainStatement(query)
		return result, true, err
	}
	if result, handled, err := s.backupInstanceStatement(lower); handled {
		return result, true, err
	}
	if result, handled, err := s.shutdownStatement(query, lower); handled {
		return result, true, err
	}
	return s.sessionControlStatement(query, lower)
}

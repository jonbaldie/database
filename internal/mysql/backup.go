package mysql

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
)

const instanceBackupChunkSize = 64 * 1024

func (s *textStatementExecutor) backupInstanceStatement(lower string) (*queryResult, bool, error) {
	if lower != "backup instance" {
		return nil, false, nil
	}
	if err := s.requireOperationalControl(); err != nil {
		return nil, true, err
	}
	metadata, err := s.captureInstanceMetadata()
	if err != nil {
		return nil, true, err
	}
	snapshot := s.session.server.config.Catalog.Snapshot()
	if s.streamRows {
		return &queryResult{
			columns: []string{"path", "content"},
			stream:  streamInstanceBackup(metadata, snapshot),
		}, true, nil
	}
	files, err := captureInstanceBackup(metadata, snapshot)
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

func (s *textStatementExecutor) captureInstanceMetadata() ([]byte, error) {
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
	return append(instanceBytes, '\n'), nil
}

func captureInstanceBackup(metadata []byte, snapshot catalog.Definition) (map[string][]byte, error) {
	var encoded bytes.Buffer
	if err := catalog.Write(&encoded, snapshot); err != nil {
		return nil, sqlFailure{1684, "HY000", "backup capture failed"}
	}
	return map[string][]byte{"instance.json": metadata, "catalog.json": encoded.Bytes()}, nil
}

func streamInstanceBackup(metadata []byte, snapshot catalog.Definition) queryRowStream {
	return func(yield func([]string, []bool) error) error {
		if err := yield([]string{"instance.json", string(metadata)}, nil); err != nil {
			return err
		}
		writer := &backupChunkWriter{yield: yield, buffer: make([]byte, 0, instanceBackupChunkSize)}
		if err := catalog.Write(writer, snapshot); err != nil {
			return err
		}
		return writer.flush()
	}
}

type backupChunkWriter struct {
	yield  func([]string, []bool) error
	buffer []byte
}

func (writer *backupChunkWriter) Write(content []byte) (int, error) {
	written := 0
	for {
		if len(content) == 0 {
			return written, nil
		}
		available := instanceBackupChunkSize - len(writer.buffer)
		copied := len(content)
		if copied > available {
			copied = available
		}
		writer.buffer = append(writer.buffer, content[:copied]...)
		content = content[copied:]
		written += copied
		if len(writer.buffer) == instanceBackupChunkSize {
			if err := writer.flush(); err != nil {
				return written, err
			}
		}
	}
}

func (writer *backupChunkWriter) flush() error {
	if len(writer.buffer) == 0 {
		return nil
	}
	if err := writer.yield([]string{"catalog.json", string(writer.buffer)}, nil); err != nil {
		return err
	}
	writer.buffer = writer.buffer[:0]
	return nil
}

var _ io.Writer = (*backupChunkWriter)(nil)

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

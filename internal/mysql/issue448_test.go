package mysql

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue448OKPacketReportsMySQLInsertID(t *testing.T) {
	executor := newIssue448Executor(t)
	for _, statement := range []string{
		"CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)",
		"CREATE TABLE source (code VARCHAR(8))",
		"CREATE TABLE explicit_source (id INT, code VARCHAR(8))",
		"CREATE TABLE plain (id INT PRIMARY KEY)",
	} {
		assertIssue448OK(t, executor, statement, 0, 0)
	}
	var negative int64 = -5
	negativeID := uint64(negative)
	for _, statement := range []struct {
		sql      string
		affected uint64
		id       uint64
	}{
		{"INSERT INTO source (code) VALUES ('s1'), ('s2')", 2, 0},
		{"INSERT INTO explicit_source (id, code) VALUES (50, 'x'), (51, 'y')", 2, 0},
		{"INSERT INTO ids (code) VALUES ('a')", 1, 1},
		{"INSERT INTO ids (code) VALUES ('b'), ('c')", 2, 2},
		{"INSERT INTO ids (id, code) VALUES (10, 'e')", 1, 10},
		{"INSERT INTO ids (id, code) VALUES (NULL, 'n')", 1, 11},
		{"INSERT INTO ids (id, code) VALUES (20, 'p'), (21, 'q')", 2, 21},
		{"INSERT INTO ids (id, code) VALUES (30, 'r'), (NULL, 's')", 2, 31},
		{"INSERT INTO ids (id, code) VALUES (NULL, 't'), (40, 'u')", 2, 32},
		{"INSERT INTO ids (id, code) VALUES (-5, 'neg')", 1, negativeID},
		{"INSERT INTO ids (code) SELECT code FROM source ORDER BY code", 2, 41},
		{"INSERT INTO ids (id, code) SELECT id, code FROM explicit_source ORDER BY id", 2, 51},
		{"REPLACE INTO ids (id, code) VALUES (1, 'a2')", 2, 1},
		{"REPLACE INTO ids (code) VALUES ('rep')", 1, 52},
		{"INSERT INTO ids (code, n) VALUES ('b', 5) ON DUPLICATE KEY UPDATE n = 5", 2, 2},
		{"INSERT INTO ids (code, n) VALUES ('b', 5) ON DUPLICATE KEY UPDATE n = 5", 0, 0},
		{"UPDATE ids SET n = 7 WHERE id = 1", 1, 0},
		{"DELETE FROM ids WHERE code = 'rep'", 1, 0},
		{"INSERT INTO plain (id) VALUES (5)", 1, 0},
		{"CREATE TABLE extra (id INT PRIMARY KEY)", 0, 0},
	} {
		assertIssue448OK(t, executor, statement.sql, statement.affected, statement.id)
	}

	conflict := newIssue448Executor(t)
	assertIssue448OK(t, conflict, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	assertIssue448OK(t, conflict, "INSERT INTO ids (code, n) VALUES ('b', 1)", 1, 1)
	assertIssue448OK(t, conflict, "INSERT INTO ids (code, n) VALUES ('b', 2) ON DUPLICATE KEY UPDATE n = 2", 2, 1)
	assertIssue448OK(t, conflict, "INSERT INTO ids (code) VALUES ('next')", 1, 3)

	failed := newIssue448Executor(t)
	assertIssue448OK(t, failed, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE)", 0, 0)
	assertIssue448OK(t, failed, "INSERT INTO ids (code) VALUES ('a')", 1, 1)
	if _, _, err := issue448OK(failed, "INSERT INTO ids (code) VALUES ('b'), ('a')"); err == nil {
		t.Fatal("duplicate multi-row insert returned an OK packet")
	}
	assertIssue448OK(t, failed, "INSERT INTO ids (code) VALUES ('c')", 1, 4)
	rows, err := executeStatement(&failed.statements, "SELECT id, code FROM ids ORDER BY id")
	if err != nil || !equalRows(rows.rows, [][]string{{"1", "a"}, {"4", "c"}}) {
		t.Fatalf("rows after failed insert = %#v, err = %v", rows, err)
	}

	explicit := newIssue448Executor(t)
	assertIssue448OK(t, explicit, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8))", 0, 0)
	if _, _, err := issue448OK(explicit, "INSERT INTO ids (id, code) VALUES (100, 'x'), (100, 'y')"); err == nil {
		t.Fatal("duplicate explicit insert returned an OK packet")
	}
	assertIssue448OK(t, explicit, "INSERT INTO ids (code) VALUES ('z')", 1, 101)
	if _, _, err := issue448OK(explicit, "INSERT INTO ids (id, code) VALUES ('nope', 'bad')"); err == nil {
		t.Fatal("type error returned an OK packet")
	}
	assertIssue448OK(t, explicit, "INSERT INTO ids (code) VALUES ('typed')", 1, 102)
	assertIssue448OK(t, explicit, "INSERT INTO ids (id, code) VALUES (0, 'zero')", 1, 0)
	zeroRows, err := executeStatement(&explicit.statements, "SELECT id FROM ids WHERE code = 'zero'")
	if err != nil || !equalRows(zeroRows.rows, [][]string{{"0"}}) {
		t.Fatalf("stored explicit zero = %#v, err = %v", zeroRows, err)
	}
}

func TestIssue448RollbackRestoresAllocatedAutoIncrement(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE)",
		"BEGIN",
		"INSERT INTO ids (code) VALUES ('a')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "INSERT INTO ids (code) VALUES ('b'), ('a')"); err == nil {
		t.Fatal("duplicate multi-row insert succeeded")
	}
	result, err := executeStatement(executor, "INSERT INTO ids (code) VALUES ('c')")
	if err != nil || result.lastInsertID != 4 {
		t.Fatalf("insert inside transaction = %#v, err = %v; want last insert ID 4", result, err)
	}
	if _, err := executeStatement(executor, "ROLLBACK"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	result, err = executeStatement(executor, "INSERT INTO ids (code) VALUES ('d')")
	if err != nil || result.lastInsertID != 1 {
		t.Fatalf("insert after rollback = %#v, err = %v; want last insert ID 1", result, err)
	}
}

func TestIssue448OKPacketEncodesLastInsertIDAbove250(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT)", 0, 0)
	affected, id, payload, err := issue448OKPacket(executor, "INSERT INTO ids (id) VALUES (300)")
	if err != nil {
		t.Fatalf("insert 300: %v", err)
	}
	if affected != 1 || id != 300 {
		t.Fatalf("affected=%d id=%d, want 1 and 300", affected, id)
	}
	if len(payload) < 5 || payload[0] != 0x00 || payload[1] != 1 || payload[2] != 0xfc || payload[3] != 0x2c || payload[4] != 0x01 {
		t.Fatalf("OK payload = %x, want length-encoded 300", payload)
	}
}

func newIssue448Executor(t *testing.T) *queryExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	return newQueryExecutor(&session{
		server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC",
		statements: map[uint32]*preparedStatement{},
	})
}

func assertIssue448OK(t *testing.T, executor *queryExecutor, query string, wantAffected, wantID uint64) {
	t.Helper()
	affected, id, err := issue448OK(executor, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if affected != wantAffected || id != wantID {
		t.Fatalf("%s: affected=%d last_insert_id=%d, want affected=%d last_insert_id=%d", query, affected, id, wantAffected, wantID)
	}
}

func issue448OK(executor *queryExecutor, query string) (uint64, uint64, error) {
	affected, id, _, err := issue448OKPacket(executor, query)
	return affected, id, err
}

func issue448OKPacket(executor *queryExecutor, query string) (uint64, uint64, []byte, error) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- executor.writeQueryResult(server, 0, query)
		_ = server.Close()
	}()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(client, header); err != nil {
		return 0, 0, nil, err
	}
	payload := make([]byte, int(header[0])|int(header[1])<<8|int(header[2])<<16)
	if _, err := io.ReadFull(client, payload); err != nil {
		return 0, 0, nil, err
	}
	if err := <-errCh; err != nil {
		return 0, 0, payload, err
	}
	if len(payload) == 0 || payload[0] != 0x00 {
		message := "not an OK packet"
		if len(payload) > 9 && payload[0] == 0xff {
			message = string(payload[9:])
		}
		return 0, 0, payload, &probeSQLError{message: message}
	}
	affected, next := readLenencUint(payload, 1)
	id, _ := readLenencUint(payload, next)
	return affected, id, payload, nil
}

type probeSQLError struct{ message string }

func (e *probeSQLError) Error() string { return e.message }

func readLenencUint(payload []byte, offset int) (uint64, int) {
	if offset >= len(payload) {
		return 0, offset
	}
	switch payload[offset] {
	case 0xfc:
		if offset+3 > len(payload) {
			return 0, offset
		}
		return uint64(binary.LittleEndian.Uint16(payload[offset+1:])), offset + 3
	case 0xfd:
		if offset+4 > len(payload) {
			return 0, offset
		}
		return uint64(payload[offset+1]) | uint64(payload[offset+2])<<8 | uint64(payload[offset+3])<<16, offset + 4
	case 0xfe:
		if offset+9 > len(payload) {
			return 0, offset
		}
		return binary.LittleEndian.Uint64(payload[offset+1:]), offset + 9
	default:
		return uint64(payload[offset]), offset + 1
	}
}

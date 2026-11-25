package mysql

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
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
	server := newTestServer(t, Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	session := newTestSession(server, testSessionOptions{database: "app"})
	executor := newQueryExecutor(session)
	seedSQL(t, &executor.statements, "CREATE DATABASE app", "USE app")
	return executor
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

func issue448AutoTable() catalog.Table {
	return catalog.Table{
		Name:             "ids",
		Columns:          []string{"id", "code"},
		ColumnTypes:      []string{"INT", "VARCHAR(8)"},
		ColumnAttributes: []catalog.ColumnAttribute{{AutoIncrement: true}, {}},
	}
}

func issue448PlainTable() catalog.Table {
	return catalog.Table{
		Name:        "plain",
		Columns:     []string{"id", "code"},
		ColumnTypes: []string{"INT", "VARCHAR(8)"},
	}
}

func TestAutoIncrementProtocolID(t *testing.T) {
	var neg5 int64 = -5
	var min64 int64 = -9223372036854775808
	negative := uint64(neg5)
	minInt64 := uint64(min64)
	for _, tc := range []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"", 0, false},
		{storedSQLNullValue, 0, false},
		{"0", 0, true},
		{"7", 7, true},
		{"18446744073709551615", 18446744073709551615, true},
		{"-5", negative, true},
		{"-9223372036854775808", minInt64, true},
		{"abc", 0, false},
		{"9223372036854775808", 9223372036854775808, true},
		{"18446744073709551616", 0, false},
		{"-9223372036854775809", 0, false},
	} {
		got, ok := autoIncrementProtocolID(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("autoIncrementProtocolID(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestAutoIncrementAdvancedAndFarther(t *testing.T) {
	for _, tc := range []struct {
		before, after autoIncrementState
		want          bool
	}{
		{autoIncrementState{next: 5}, autoIncrementState{next: 5}, false},
		{autoIncrementState{next: 5}, autoIncrementState{next: 6}, true},
		{autoIncrementState{next: 5}, autoIncrementState{next: 4}, false},
		{autoIncrementState{next: 5}, autoIncrementState{next: 5, exhausted: true}, true},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 5}, false},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 9, exhausted: true}, true},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 4, exhausted: true}, false},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 5, exhausted: true}, false},
	} {
		if got := autoIncrementAdvanced(tc.before, tc.after); got != tc.want {
			t.Fatalf("autoIncrementAdvanced(%+v,%+v) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
	for _, tc := range []struct {
		current, allocated autoIncrementState
		want               autoIncrementState
	}{
		{autoIncrementState{next: 5}, autoIncrementState{next: 6}, autoIncrementState{next: 6}},
		{autoIncrementState{next: 6}, autoIncrementState{next: 6}, autoIncrementState{next: 6}},
		{autoIncrementState{next: 7}, autoIncrementState{next: 6}, autoIncrementState{next: 7}},
		{autoIncrementState{next: 5}, autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 5, exhausted: true}},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 5}, autoIncrementState{next: 5, exhausted: true}},
		{autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 9, exhausted: true}, autoIncrementState{next: 5, exhausted: true}},
		{autoIncrementState{next: 9, exhausted: true}, autoIncrementState{next: 5, exhausted: true}, autoIncrementState{next: 9, exhausted: true}},
	} {
		if got := fartherAutoIncrementState(tc.current, tc.allocated); got != tc.want {
			t.Fatalf("fartherAutoIncrementState(%+v,%+v) = %+v, want %+v", tc.current, tc.allocated, got, tc.want)
		}
	}
}

func TestStatementInsertIDRecordAndValue(t *testing.T) {
	var id statementInsertID
	if id.value() != 0 {
		t.Fatalf("empty value = %d, want 0", id.value())
	}
	id.record(issue448PlainTable(), []string{"9", "x"}, true)
	if id.value() != 0 {
		t.Fatalf("plain-table value = %d, want 0", id.value())
	}
	id.record(issue448AutoTable(), []string{}, false)
	id.record(issue448AutoTable(), []string{"bad", "x"}, true)
	id.record(issue448AutoTable(), []string{""}, true)
	if id.value() != 0 {
		t.Fatalf("non-numeric value = %d, want 0", id.value())
	}
	id.record(issue448AutoTable(), []string{"7", "x"}, false)
	if id.value() != 7 {
		t.Fatalf("stored value = %d, want 7", id.value())
	}
	id.record(issue448AutoTable(), []string{"9", "x"}, false)
	if id.value() != 9 {
		t.Fatalf("last stored value = %d, want 9", id.value())
	}
	id.record(issue448AutoTable(), []string{"3", "x"}, true)
	if id.value() != 3 {
		t.Fatalf("first generated value = %d, want 3", id.value())
	}
	id.record(issue448AutoTable(), []string{"4", "x"}, true)
	if id.value() != 3 {
		t.Fatalf("second generated changed value = %d, want 3", id.value())
	}
}

func TestAdvanceAutoIncrement(t *testing.T) {
	limit := uint64(2147483647)
	for _, tc := range []struct {
		state autoIncrementState
		value uint64
		want  autoIncrementState
	}{
		{autoIncrementState{next: 5}, 3, autoIncrementState{next: 5}},
		{autoIncrementState{next: 5}, 5, autoIncrementState{next: 6}},
		{autoIncrementState{next: 5}, 2147483646, autoIncrementState{next: 2147483647}},
		{autoIncrementState{next: 5}, 2147483647, autoIncrementState{next: limit, exhausted: true}},
		{autoIncrementState{next: 5}, 2147483648, autoIncrementState{next: limit, exhausted: true}},
		{autoIncrementState{next: 5, exhausted: true}, 10, autoIncrementState{next: 5, exhausted: true}},
	} {
		if got := advanceAutoIncrement(tc.state, tc.value, limit); got != tc.want {
			t.Fatalf("advanceAutoIncrement(%+v,%d) = %+v, want %+v", tc.state, tc.value, got, tc.want)
		}
	}
}

func TestFillAutoIncrementValue(t *testing.T) {
	limit := uint64(2147483647)
	row := []string{storedSQLNullValue, "x"}
	next, generated, err := fillAutoIncrementValue(issue448AutoTable(), row, autoIncrementState{next: 1}, 1)
	if err != nil || !generated || next != (autoIncrementState{next: 2}) || row[0] != "1" {
		t.Fatalf("generated = (%+v,%v,%v) row=%q", next, generated, err, row)
	}
	row = []string{storedSQLNullValue, "x"}
	next, generated, err = fillAutoIncrementValue(issue448AutoTable(), row, autoIncrementState{next: limit}, 2)
	if err != nil || !generated || next != (autoIncrementState{next: limit, exhausted: true}) || row[0] != "2147483647" {
		t.Fatalf("at limit = (%+v,%v,%v) row=%q", next, generated, err, row)
	}
	for _, bad := range []autoIncrementState{{next: limit + 1}, {next: 0}, {next: 1, exhausted: true}} {
		row = []string{storedSQLNullValue, "x"}
		_, _, err = fillAutoIncrementValue(issue448AutoTable(), row, bad, 3)
		if err == nil || !strings.Contains(err.Error(), "row 3") {
			t.Fatalf("overflow state %+v err = %v", bad, err)
		}
	}
	row = []string{"10", "x"}
	next, generated, err = fillAutoIncrementValue(issue448AutoTable(), row, autoIncrementState{next: 1}, 1)
	if err != nil || generated || next != (autoIncrementState{next: 11}) {
		t.Fatalf("explicit = (%+v,%v,%v)", next, generated, err)
	}
	row = []string{"3", "x"}
	next, generated, err = fillAutoIncrementValue(issue448AutoTable(), row, autoIncrementState{next: 10}, 1)
	if err != nil || generated || next != (autoIncrementState{next: 10}) {
		t.Fatalf("below = (%+v,%v,%v)", next, generated, err)
	}
	for _, keep := range []string{"0", "-5", "", "bad"} {
		row = []string{keep, "x"}
		next, generated, err = fillAutoIncrementValue(issue448AutoTable(), row, autoIncrementState{next: 4}, 1)
		if err != nil || generated || next != (autoIncrementState{next: 4}) {
			t.Fatalf("keep %q = (%+v,%v,%v)", keep, next, generated, err)
		}
	}
	plainRow := []string{storedSQLNullValue, "x"}
	state := autoIncrementState{next: 9}
	next, generated, err = fillAutoIncrementValue(issue448PlainTable(), plainRow, state, 1)
	if err != nil || generated || next != state {
		t.Fatalf("plain = (%+v,%v,%v)", next, generated, err)
	}
}

func TestIssue448UpsertInsertNewReportsID(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('new', 1) ON DUPLICATE KEY UPDATE n = VALUES(n)", 1, 1)
	assertIssue448OK(t, executor, "INSERT INTO ids (id, code) VALUES (60, 'fresh') ON DUPLICATE KEY UPDATE n = VALUES(n)", 1, 60)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('new2', 2), ('fresh', 3) ON DUPLICATE KEY UPDATE n = 3", 3, 61)
	assertIssue448OK(t, executor, "INSERT INTO ids (id, code) VALUES (70, 'third') ON DUPLICATE KEY UPDATE code = 'x'", 1, 70)
}

func TestIssue448FailedStatementsPreserveCounter(t *testing.T) {
	upsert := newIssue448Executor(t)
	assertIssue448OK(t, upsert, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	assertIssue448OK(t, upsert, "INSERT INTO ids (code, n) VALUES ('a', 1)", 1, 1)
	if _, _, err := issue448OK(upsert, "INSERT INTO ids (code, n) VALUES ('b', 'bad') ON DUPLICATE KEY UPDATE n = 2"); err == nil {
		t.Fatal("bad upsert type returned an OK packet")
	}
	assertIssue448OK(t, upsert, "INSERT INTO ids (code, n) VALUES ('c', 1)", 1, 2)
	replace := newIssue448Executor(t)
	assertIssue448OK(t, replace, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE)", 0, 0)
	assertIssue448OK(t, replace, "INSERT INTO ids (code) VALUES ('a')", 1, 1)
	if _, _, err := issue448OK(replace, "REPLACE INTO ids (id, code) VALUES (2, 'x'), ('bad', 'y')"); err == nil {
		t.Fatal("bad replace type returned an OK packet")
	}
	assertIssue448OK(t, replace, "INSERT INTO ids (code) VALUES ('z')", 1, 2)
}

func TestIssue448BadPlansReturnErrors(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	for _, statement := range []string{
		"INSERT INTO missing (id) VALUES (1)",
		"INSERT INTO ids (missing) VALUES ('x')",
		"INSERT INTO ids (code) VALUES ('x') ON DUPLICATE KEY UPDATE missing = 1",
		"INSERT INTO ids (code) VALUES ('x') ON DUPLICATE KEY UPDATE n =",
		"REPLACE INTO missing (id) VALUES (1)",
		"INSERT INTO ids (code) VALUES (1, 2)",
	} {
		if _, _, err := issue448OK(executor, statement); err == nil {
			t.Fatalf("%s returned an OK packet", statement)
		}
	}
}

func TestIssue448AutoIncrementOverflow(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE tiny (id TINYINT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8))", 0, 0)
	assertIssue448OK(t, executor, "INSERT INTO tiny (id, code) VALUES (127, 'max')", 1, 127)
	if _, _, err := issue448OK(executor, "INSERT INTO tiny (code) VALUES ('over')"); err == nil {
		t.Fatal("overflow insert returned an OK packet")
	}
}

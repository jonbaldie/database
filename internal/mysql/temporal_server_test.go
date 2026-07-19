package mysql

import (
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

func currentTimeExecutor(t *testing.T, zone string, instant time.Time) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{
		Catalog:  store,
		Version:  "0.1.0",
		TimeZone: zone,
		Clock:    func() time.Time { return instant },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: zone, initialTimeZone: zone, statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session}
}

func TestCurrentTimeRendersThroughFixedOffset(t *testing.T) {
	// 22:00 UTC on 2026-07-18 seen through +05:30 is 03:30 on 2026-07-19, so the
	// offset is observable on both the date and the clock.
	instant := time.Date(2026, 7, 18, 22, 0, 0, 0, time.UTC)
	executor := currentTimeExecutor(t, "+05:30", instant)
	cases := map[string]string{
		"SELECT CURRENT_DATE":      "2026-07-19",
		"SELECT CURRENT_TIME":      "03:30:00",
		"SELECT CURRENT_TIMESTAMP": "2026-07-19 03:30:00",
		"SELECT NOW()":             "2026-07-19 03:30:00",
		"SELECT CURDATE()":         "2026-07-19",
	}
	for query, want := range cases {
		result, err := executor.execute(query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", query, err)
		}
		if got := result.rows[0][0]; got != want {
			t.Errorf("execute(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestCurrentTimeAtUTCIsStableWithinAStatement(t *testing.T) {
	instant := time.Date(2026, 7, 18, 14, 5, 6, 0, time.UTC)
	executor := currentTimeExecutor(t, "UTC", instant)
	result, err := executor.execute("SELECT CURRENT_TIMESTAMP")
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if got := result.rows[0][0]; got != "2026-07-18 14:05:06" {
		t.Errorf("UTC CURRENT_TIMESTAMP = %q, want 2026-07-18 14:05:06", got)
	}
}

func TestCurrentTimeResultsExposeTemporalMetadata(t *testing.T) {
	executor := currentTimeExecutor(t, "UTC", time.Date(2026, 7, 18, 14, 5, 6, 0, time.UTC))
	cases := map[string]struct {
		kind temporalKind
		wire byte
	}{
		"SELECT CURRENT_DATE":      {kind: temporalDate, wire: mysqlTypeDate},
		"SELECT CURRENT_TIME":      {kind: temporalTime, wire: mysqlTypeTime},
		"SELECT CURRENT_TIMESTAMP": {kind: temporalDatetime, wire: mysqlTypeDatetime},
	}
	for query, want := range cases {
		result, err := executor.execute(query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", query, err)
		}
		if len(result.metadata) != 1 {
			t.Fatalf("execute(%q) metadata = %#v, want one definition", query, result.metadata)
		}
		definition := result.metadata[0]
		if definition.typ != want.wire {
			t.Errorf("execute(%q) type = %#x, want %#x", query, definition.typ, want.wire)
		}
		if parsed, err := parseTemporalType(map[temporalKind]string{
			temporalDate:     "DATE",
			temporalTime:     "TIME",
			temporalDatetime: "DATETIME",
		}[want.kind]); err != nil || definition.length != parsed.length || definition.characterSet != mysqlCharsetBinary {
			t.Errorf("execute(%q) metadata = %#v, want temporal metadata for %v", query, definition, want.kind)
		}
	}
}

func TestCurrentTimePreservesRequestedFractionalPrecision(t *testing.T) {
	instant := time.Date(2026, 7, 18, 22, 0, 0, 123456789, time.UTC)
	executor := currentTimeExecutor(t, "+05:30", instant)
	cases := map[string]string{
		"SELECT CURRENT_TIME(3)":      "03:30:00.123",
		"SELECT CURRENT_TIMESTAMP(6)": "2026-07-19 03:30:00.123456",
		"SELECT NOW(2)":               "2026-07-19 03:30:00.12",
		"SELECT CURTIME(0)":           "03:30:00",
	}
	for query, want := range cases {
		result, err := executor.execute(query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", query, err)
		}
		if got := result.rows[0][0]; got != want {
			t.Errorf("execute(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestTimestampSessionOffsetAppliesToWritePredicateAndRead(t *testing.T) {
	executor := currentTimeExecutor(t, "+05:30", time.Date(2026, 7, 18, 22, 0, 0, 0, time.UTC))
	if _, err := executor.execute("CREATE TABLE events (at TIMESTAMP)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := executor.execute("INSERT INTO events VALUES ('2021-01-02 03:04:05')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	stored := executor.server.config.Catalog.Snapshot().Namespaces["app"].Tables["events"].Rows[0][0]
	if stored != "2021-01-01 21:34:05" {
		t.Fatalf("stored TIMESTAMP = %q, want UTC instant", stored)
	}
	result, err := executor.execute("SELECT at FROM events WHERE at = '2021-01-02 03:04:05'")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "2021-01-02 03:04:05" {
		t.Fatalf("selected TIMESTAMP = %#v, want session-local value", result.rows)
	}
}

func TestTimestampOffsetBelongsToTheSession(t *testing.T) {
	executor := currentTimeExecutor(t, "UTC", time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC))
	if result, err := executor.execute("SELECT @@time_zone"); err != nil || result.rows[0][0] != "+00:00" {
		t.Fatalf("initial session time zone = %#v err %v", result, err)
	}
	if _, err := executor.execute("SET time_zone = '+05:30'"); err != nil {
		t.Fatalf("set time zone: %v", err)
	}
	if result, err := executor.execute("SELECT CURRENT_TIMESTAMP"); err != nil || result.rows[0][0] != "2021-01-02 08:34:05" {
		t.Fatalf("session current timestamp = %#v err %v", result, err)
	}
	if _, err := executor.execute("CREATE TABLE events (at TIMESTAMP)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := executor.execute("INSERT INTO events VALUES ('2021-01-02 08:34:05')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	stored := executor.server.config.Catalog.Snapshot().Namespaces["app"].Tables["events"].Rows[0][0]
	if stored != "2021-01-02 03:04:05" {
		t.Fatalf("session-adjusted TIMESTAMP storage = %q", stored)
	}
	if _, err := executor.execute("SET time_zone = DEFAULT"); err != nil {
		t.Fatalf("reset time zone: %v", err)
	}
	if result, err := executor.execute("SELECT at FROM events WHERE at = '2021-01-02 03:04:05'"); err != nil || len(result.rows) != 1 {
		t.Fatalf("default-zone TIMESTAMP read/predicate = %#v err %v", result, err)
	}
}

// Prepared binary temporal inputs must decode to the exact canonical spelling a
// text literal carries, so both paths store equivalent values.
func TestPreparedTemporalMatchesTextInput(t *testing.T) {
	cases := []struct {
		wire    byte
		payload []byte
		want    string
	}{
		{mysqlTypeDate, []byte{4, 0xE5, 0x07, 1, 2}, "2021-01-02"},
		{mysqlTypeDatetime, []byte{7, 0xE5, 0x07, 1, 2, 3, 4, 5}, "2021-01-02 03:04:05"},
		{mysqlTypeDatetime, []byte{11, 0xE5, 0x07, 1, 2, 3, 4, 5, 0x40, 0xE2, 0x01, 0x00}, "2021-01-02 03:04:05.123456"},
		{mysqlTypeTimestamp, []byte{7, 0xE5, 0x07, 1, 2, 3, 4, 5}, "2021-01-02 03:04:05"},
		{mysqlTypeTime, []byte{8, 0, 0, 0, 0, 0, 12, 0, 0}, "12:00:00"},
		{mysqlTypeTime, []byte{8, 1, 0, 0, 0, 0, 12, 0, 0}, "-12:00:00"},
		{mysqlTypeTime, []byte{8, 0, 1, 0, 0, 0, 14, 0, 0}, "38:00:00"},
		{mysqlTypeDate, []byte{0}, "0000-00-00"},
	}
	for _, c := range cases {
		got, next, err := readPreparedTemporal(c.payload, 0, preparedParameterType{typ: c.wire})
		if err != nil {
			t.Fatalf("readPreparedTemporal(%#x) error: %v", c.wire, err)
		}
		if next != len(c.payload) {
			t.Errorf("readPreparedTemporal(%#x) next = %d, want %d", c.wire, next, len(c.payload))
		}
		if unquoted := scalar(got); unquoted != c.want {
			t.Errorf("readPreparedTemporal(%#x) = %q, want %q", c.wire, unquoted, c.want)
		}
	}
}

func TestPreparedAndTextDatetimeCanonicalizeEqually(t *testing.T) {
	typ, _ := parseTemporalType("DATETIME(6)")
	prepared, _, err := readPreparedTemporal([]byte{11, 0xE5, 0x07, 1, 2, 3, 4, 5, 0x40, 0xE2, 0x01, 0x00}, 0, preparedParameterType{typ: mysqlTypeDatetime})
	if err != nil {
		t.Fatalf("prepared decode error: %v", err)
	}
	fromPrepared, perr := canonicalTemporalValue(typ, scalar(prepared), "at", 1)
	fromText, terr := canonicalTemporalValue(typ, "2021-01-02 03:04:05.123456", "at", 1)
	if perr != nil || terr != nil {
		t.Fatalf("canonicalization errors: prepared=%v text=%v", perr, terr)
	}
	if fromPrepared != fromText {
		t.Errorf("prepared %q and text %q disagree", fromPrepared, fromText)
	}
}

func TestPreparedMicrosecondsDoNotNormalize(t *testing.T) {
	for _, test := range []struct {
		wire    byte
		payload []byte
	}{
		{mysqlTypeDatetime, []byte{11, 0xE5, 0x07, 1, 2, 3, 4, 5, 0x41, 0x42, 0x0F, 0x00}},
		{mysqlTypeTime, []byte{12, 0, 0, 0, 0, 0, 12, 0, 0, 0x41, 0x42, 0x0F, 0x00}},
	} {
		if _, _, err := readPreparedTemporal(test.payload, 0, preparedParameterType{typ: test.wire}); err == nil {
			t.Errorf("readPreparedTemporal(%#x) normalized excessive microseconds", test.wire)
		}
	}
}

func TestPreparedTemporalRejectsMalformedLength(t *testing.T) {
	if _, _, err := readPreparedTemporal([]byte{7, 0xE5, 0x07}, 0, preparedParameterType{typ: mysqlTypeDatetime}); err == nil {
		t.Fatalf("readPreparedTemporal accepted a truncated payload")
	}
	if _, _, err := readPreparedTemporal([]byte{}, 0, preparedParameterType{typ: mysqlTypeDate}); err == nil {
		t.Fatalf("readPreparedTemporal accepted an empty payload")
	}
	for _, test := range []struct {
		wire    byte
		payload []byte
	}{
		{mysqlTypeDate, []byte{1, 1}},
		{mysqlTypeDatetime, []byte{5, 0, 0, 1, 1, 1}},
		{mysqlTypeTime, []byte{9, 0, 0, 0, 0, 0, 1, 2, 3, 4}},
	} {
		if _, _, err := readPreparedTemporal(test.payload, 0, preparedParameterType{typ: test.wire}); err == nil {
			t.Errorf("readPreparedTemporal(%#x) accepted body length %d", test.wire, len(test.payload)-1)
		}
	}
	for _, test := range []struct {
		wire    byte
		payload []byte
	}{
		{mysqlTypeTime, []byte{8, 2, 0, 0, 0, 0, 0, 0, 0}},
		{mysqlTypeTime, []byte{8, 0, 0, 0, 0, 24, 0, 0, 0}},
	} {
		if _, _, err := readPreparedTemporal(test.payload, 0, preparedParameterType{typ: test.wire}); err == nil {
			t.Errorf("readPreparedTemporal(%#x) accepted malformed TIME fields", test.wire)
		}
	}
}

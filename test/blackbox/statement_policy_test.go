package blackbox_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestMySQLTextReadKeepsWireContract protects the text read that enters the
// statement execution policy. It checks only the MySQL wire contract.
func TestMySQLTextReadKeepsWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, statement := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE contacts (id INT, name VARCHAR(32))",
		"INSERT INTO contacts VALUES (1, 'Ada')",
	} {
		mustQuery(t, client, statement)
	}

	read := client.query("SELECT id, name FROM contacts WHERE id = 1")
	if read.err != "" || !reflect.DeepEqual(read.columns, []string{"id", "name"}) || !reflect.DeepEqual(read.rows, [][]string{{"1", "Ada"}}) {
		t.Fatalf("text read = %#v", read)
	}
	if len(read.metadata) != 2 || read.metadata[0].name != "id" || read.metadata[1].name != "name" {
		t.Fatalf("text read metadata = %#v", read.metadata)
	}

	missing := client.query("SELECT * FROM missing")
	if missing.err == "" || missing.errCode != 1146 {
		t.Fatalf("missing table read = %#v", missing)
	}
}

// TestMySQLPreparedReadKeepsTextWireContract protects the prepared read after
// it has bound values. It checks only the MySQL wire contract.
func TestMySQLPreparedReadKeepsTextWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, statement := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE contacts (id INT, name VARCHAR(32))",
		"INSERT INTO contacts VALUES (1, 'Ada')",
	} {
		mustQuery(t, client, statement)
	}

	text := client.query("SELECT id, name FROM contacts WHERE id = 1")
	prepared := client.prepare("SELECT id, name FROM contacts WHERE id = ?")
	if prepared.err != "" {
		t.Fatalf("prepare read = %#v", prepared)
	}
	defer client.closePrepared(prepared.id)
	bound := client.executePreparedValues(prepared.id, []preparedParameter{{typ: 0x03, value: []byte{1, 0, 0, 0}}})
	if text.err != "" || bound.err != "" || !reflect.DeepEqual(bound.rows, text.rows) || !reflect.DeepEqual(bound.metadata, text.metadata) {
		t.Fatalf("prepared read differs from text read: text=%#v prepared=%#v", text, bound)
	}

	missingPrepared := client.prepare("SELECT * FROM contacts")
	if missingPrepared.err != "" {
		t.Fatalf("prepare missing read = %#v", missingPrepared)
	}
	defer client.closePrepared(missingPrepared.id)
	mustQuery(t, client, "DROP TABLE contacts")
	missingText := client.query("SELECT * FROM contacts")
	missingBound := client.executePrepared(missingPrepared.id)
	if missingText.errCode != 1146 || missingBound.errCode != missingText.errCode || missingBound.errState != missingText.errState || missingBound.err != missingText.err {
		t.Fatalf("prepared missing read differs from text read: text=%#v prepared=%#v", missingText, missingBound)
	}
}

// TestMySQLDataChangesAndTransactionsKeepWireContract proves that the
// statement execution policy preserves data-change and transaction Query
// behaviour for text and prepared SQL.
func TestMySQLDataChangesAndTransactionsKeepWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	firstProcess := process
	defer func() { _ = firstProcess.Stop(); _ = firstProcess.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	observer := newWireClient(t, address, "admin", "lifecycle-secret")
	defer observer.close()

	for _, statement := range []string{
		"CREATE DATABASE policy_transactions",
		"USE policy_transactions",
		"CREATE TABLE entries (id INT PRIMARY KEY)",
	} {
		mustQuery(t, client, statement)
	}
	mustQuery(t, observer, "USE policy_transactions")
	if result := client.query("INSERT INTO entries VALUES (1)"); result.err != "" || result.affected != 1 {
		t.Fatalf("text autocommit data change = %#v", result)
	}
	if rows := observer.query("SELECT id FROM entries ORDER BY id"); rows.err != "" || !reflect.DeepEqual(rows.rows, [][]string{{"1"}}) {
		t.Fatalf("text autocommit visibility = %#v", rows)
	}

	insert := client.prepare("INSERT INTO entries VALUES (?)")
	if insert.err != "" {
		t.Fatalf("prepare data change = %#v", insert)
	}
	insertValue := func(value byte) wireResult {
		return client.executePreparedValues(insert.id, []preparedParameter{{typ: 0x03, value: []byte{value, 0, 0, 0}}})
	}
	if result := insertValue(2); result.err != "" || result.affected != 1 {
		t.Fatalf("prepared autocommit data change = %#v", result)
	}
	if rows := observer.query("SELECT id FROM entries ORDER BY id"); rows.err != "" || !reflect.DeepEqual(rows.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("prepared autocommit visibility = %#v", rows)
	}

	mustQuery(t, client, "BEGIN")
	if result := insertValue(3); result.err != "" || result.affected != 1 {
		t.Fatalf("prepared transaction data change = %#v", result)
	}
	mustQuery(t, client, "SAVEPOINT keep_first_change")
	if result := insertValue(4); result.err != "" || result.affected != 1 {
		t.Fatalf("prepared savepoint data change = %#v", result)
	}
	mustQuery(t, client, "ROLLBACK TO SAVEPOINT keep_first_change")
	if result := insertValue(3); result.err == "" || result.errCode != 1062 || result.errState != "23000" {
		t.Fatalf("failed prepared data change = %#v", result)
	}
	if result := insertValue(5); result.err != "" || result.affected != 1 {
		t.Fatalf("prepared data change after failure = %#v", result)
	}
	mustQuery(t, client, "COMMIT")
	mustQuery(t, client, "BEGIN")
	if result := client.query("INSERT INTO entries VALUES (6)"); result.err != "" || result.affected != 1 {
		t.Fatalf("text transaction data change = %#v", result)
	}
	mustQuery(t, client, "ROLLBACK")

	rows := client.query("SELECT id FROM entries ORDER BY id")
	if rows.err != "" || !reflect.DeepEqual(rows.rows, [][]string{{"1"}, {"2"}, {"3"}, {"5"}}) {
		t.Fatalf("committed data changes = %#v", rows)
	}
	client.closePrepared(insert.id)
	if err := observer.close(); err != nil {
		t.Fatal(err)
	}
	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	if err := firstProcess.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := firstProcess.Wait(); result.ExitCode != 0 {
		t.Fatalf("durability shutdown = %#v", result)
	}

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client = newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	mustQuery(t, client, "USE policy_transactions")
	rows = client.query("SELECT id FROM entries ORDER BY id")
	if rows.err != "" || !reflect.DeepEqual(rows.rows, [][]string{{"1"}, {"2"}, {"3"}, {"5"}}) {
		t.Fatalf("durable data changes = %#v", rows)
	}
}

// TestMySQLSettingsAndAccountChangesKeepWireContract proves that the
// statement execution policy preserves immediate session-setting changes and
// durable database-account changes.
func TestMySQLSettingsAndAccountChangesKeepWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	firstProcess := process
	defer func() { _ = firstProcess.Stop(); _ = firstProcess.Wait() }()
	admin := newWireClient(t, address, "admin", "lifecycle-secret")

	setting := admin.prepare("/* application setting */ SET time_zone = ?")
	if setting.err != "" {
		t.Fatalf("prepare session setting = %#v", setting)
	}
	if result := admin.executePreparedValues(setting.id, []preparedParameter{stringPreparedParameter("+05:30")}); result.err != "" {
		t.Fatalf("prepared session setting = %#v", result)
	}
	admin.closePrepared(setting.id)
	if result := admin.query("SELECT @@time_zone"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"+05:30"}}) {
		t.Fatalf("immediate session setting = %#v", result)
	}
	unsupportedSetting := admin.prepare("SET foreign_key_checks = ?")
	if unsupportedSetting.err != "" {
		t.Fatalf("prepare unsupported session setting = %#v", unsupportedSetting)
	}
	if result := admin.executePreparedValues(unsupportedSetting.id, []preparedParameter{stringPreparedParameter("0")}); result.errCode != 1193 || result.errState != "HY000" {
		t.Fatalf("unsupported session setting = %#v", result)
	}
	admin.closePrepared(unsupportedSetting.id)

	for _, statement := range []string{
		"CREATE DATABASE policy_accounts",
		"USE policy_accounts",
		"CREATE TABLE entries (id INT PRIMARY KEY)",
		"INSERT INTO entries VALUES (1)",
		"BEGIN",
		"INSERT INTO entries VALUES (2)",
	} {
		mustQuery(t, admin, statement)
	}
	createAccount := admin.prepare("/* application account */ CREATE USER ? IDENTIFIED BY ?")
	if createAccount.err != "" {
		t.Fatalf("prepare account creation = %#v", createAccount)
	}
	if result := admin.executePreparedValues(createAccount.id, []preparedParameter{
		stringPreparedParameter("reader"),
		stringPreparedParameter("reader-policy-secret"),
	}); result.err != "" {
		t.Fatalf("prepared account creation = %#v", result)
	}
	admin.closePrepared(createAccount.id)
	mustQuery(t, admin, "ROLLBACK")
	if result := admin.query("SELECT id FROM entries ORDER BY id"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("account change implicit commit = %#v", result)
	}
	mustQuery(t, admin, "GRANT DATA_READ ON policy_accounts.* TO 'reader'")
	unsupportedGrant := admin.prepare("GRANT UNSUPPORTED ON *.* TO ?")
	if unsupportedGrant.err != "" {
		t.Fatalf("prepare unsupported account grant = %#v", unsupportedGrant)
	}
	if result := admin.executePreparedValues(unsupportedGrant.id, []preparedParameter{stringPreparedParameter("reader")}); result.errCode != 1064 || result.errState != "42000" {
		t.Fatalf("unsupported account grant = %#v", result)
	}
	admin.closePrepared(unsupportedGrant.id)

	reader := newWireClient(t, address, "reader", "reader-policy-secret")
	mustQuery(t, reader, "USE policy_accounts")
	if result := reader.query("SELECT id FROM entries ORDER BY id"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("authorized database account read = %#v", result)
	}
	deniedAccount := reader.prepare("CREATE USER ? IDENTIFIED BY ?")
	if deniedAccount.err != "" {
		t.Fatalf("prepare denied account change = %#v", deniedAccount)
	}
	if result := reader.executePreparedValues(deniedAccount.id, []preparedParameter{
		stringPreparedParameter("denied"),
		stringPreparedParameter("denied-policy-secret"),
	}); result.errCode != 1227 || result.errState != "42000" {
		t.Fatalf("denied account change = %#v", result)
	}
	reader.closePrepared(deniedAccount.id)
	if err := reader.close(); err != nil {
		t.Fatal(err)
	}
	if err := admin.close(); err != nil {
		t.Fatal(err)
	}
	if err := firstProcess.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := firstProcess.Wait(); result.ExitCode != 0 {
		t.Fatalf("account durability shutdown = %#v", result)
	}

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	reader = newWireClient(t, address, "reader", "reader-policy-secret")
	defer reader.close()
	mustQuery(t, reader, "USE policy_accounts")
	if result := reader.query("SELECT id FROM entries ORDER BY id"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("durable database account grant = %#v", result)
	}
}

// TestMySQLLocksResourcesCancellationAndExplanationKeepWireContract proves
// that statement policy keeps concurrent Query behaviour and its evidence.
func TestMySQLLocksResourcesCancellationAndExplanationKeepWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=1000")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	owner := newWireClient(t, address, "admin", "lifecycle-secret")
	defer owner.close()
	worker := newWireClient(t, address, "admin", "lifecycle-secret")
	defer worker.close()
	observer := newWireClient(t, address, "admin", "lifecycle-secret")
	defer observer.close()
	for _, statement := range []string{
		"CREATE DATABASE policy_concurrency",
		"USE policy_concurrency",
		"CREATE TABLE entries (id INT PRIMARY KEY, value INT)",
		"INSERT INTO entries VALUES (1, 10), (2, 10)",
	} {
		mustQuery(t, owner, statement)
	}
	for _, client := range []*wireClient{worker, observer} {
		mustQuery(t, client, "USE policy_concurrency")
	}

	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM entries WHERE id = 1 FOR UPDATE")
	lockingRead := worker.prepare("SELECT id FROM entries WHERE id = ? FOR UPDATE NOWAIT")
	if lockingRead.err != "" {
		t.Fatalf("prepare locking read = %#v", lockingRead)
	}
	if result := worker.executePreparedValues(lockingRead.id, []preparedParameter{{typ: 0x03, value: []byte{1, 0, 0, 0}}}); result.errCode != 3572 || result.errState != "HY000" {
		t.Fatalf("prepared locking read = %#v", result)
	}
	worker.closePrepared(lockingRead.id)

	mustQuery(t, worker, "BEGIN")
	mustQuery(t, worker, "UPDATE entries SET value = 20 WHERE id = 2")
	mustQuery(t, worker, "SET statement_timeout_ms = 25")
	if result := worker.query("UPDATE entries SET value = 20 WHERE id = 1"); result.errCode != 3024 || result.errState != "HY000" {
		t.Fatalf("statement resource limit = %#v", result)
	}
	mustQuery(t, worker, "SET statement_timeout_ms = 1000")
	mustQuery(t, worker, "COMMIT")
	if result := observer.query("SELECT id, value FROM entries ORDER BY id"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1", "10"}, {"2", "20"}}) {
		t.Fatalf("transaction after resource failure = %#v", result)
	}

	mustQuery(t, worker, "BEGIN")
	mustQuery(t, worker, "UPDATE entries SET value = 30 WHERE id = 2")
	blockedSQL := "/* application update */ UPDATE entries SET value = 30 WHERE id = 1"
	blocked := queryAsync(worker, blockedSQL)
	waitForProcessListQuery(t, observer, worker.connectionID)
	snapshot := liveQueryExplanation(t, observer, worker.connectionID)
	statement := snapshot["statement"].(map[string]any)
	actual := snapshot["plan"].(map[string]any)["actual"].(map[string]any)
	if statement["sql"] != "UPDATE entries SET value = 30 WHERE id = 1" || statement["kind"] != "update" || actual["wait"].(map[string]any)["lock_ms"].(float64) <= 0 {
		t.Fatalf("live Query explanation execution = %#v", snapshot)
	}
	mustQuery(t, observer, "KILL QUERY "+strconv.FormatUint(uint64(worker.connectionID), 10))
	if result := <-blocked; result.errCode != 1317 || result.errState != "70100" {
		t.Fatalf("cancelled statement = %#v", result)
	}
	if result := worker.query("SELECT 1"); result.err != "" {
		t.Fatalf("session after cancellation = %#v", result)
	}
	if result := worker.query("SELECT value FROM entries WHERE id = 2"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"20"}}) {
		t.Fatalf("cancelled transaction state = %#v", result)
	}
	if result := observer.query("SELECT id, value FROM entries ORDER BY id"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1", "10"}, {"2", "20"}}) {
		t.Fatalf("transaction after cancellation = %#v", result)
	}
	mustQuery(t, owner, "COMMIT")
}

func stringPreparedParameter(value string) preparedParameter {
	return preparedParameter{typ: 0xfd, value: []byte(value)}
}

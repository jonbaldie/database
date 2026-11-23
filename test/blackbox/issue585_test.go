package blackbox_test

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMetadataFailureRemovesSortSpills(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	instance := startInstance(t, runner, blackbox.InstanceOptions{
		DataDirectory: directory, Diagnostics: true,
		Arguments: []string{"--max-allowed-packet=1024", "--execution-memory-limit-bytes=30", "--aggregate-execution-memory-limit-bytes=30", "--temporary-storage-limit-bytes=4096", "--aggregate-temporary-storage-limit-bytes=4096"},
	})
	defer func() { _ = instance.Process.Stop(); _ = instance.Process.Wait() }()
	client := newWireClient(t, instance.MySQLAddress, "admin", "lifecycle-secret")
	defer client.close()
	namespace, table, column := strings.Repeat("𐐀", 64), strings.Repeat("𐐁", 64), strings.Repeat("𐐂", 64)
	for _, query := range []string{
		"CREATE DATABASE " + namespace,
		"USE " + namespace,
		"CREATE TABLE " + table + " (" + column + " VARCHAR(32))",
		"INSERT INTO " + table + " VALUES ('Ada'), ('Grace'), ('Linus')",
	} {
		mustQuery(t, client, query)
	}
	// The query fits the inbound limit. Its column definition repeats the long
	// schema, table, and column names and exceeds the outbound packet limit.
	query := "SELECT " + column + " FROM " + table + " ORDER BY " + column
	writeWirePacket(t, client.conn, 0, append([]byte{0x03}, query...))
	if count := readWirePacket(t, client.conn); len(count) != 1 || count[0] != 1 {
		t.Fatalf("column count = %x", count)
	}
	// Once a result has started, a metadata failure closes the connection.
	var header [4]byte
	if n, err := io.ReadFull(client.conn, header[:]); n != 0 || err != io.EOF {
		t.Fatalf("metadata failure = %d bytes, %v; want connection closure", n, err)
	}
	response, err := http.Get("http://" + instance.DiagnosticsAddress + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	metrics, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(metrics), "database_resource_spills_total ") || strings.Contains(string(metrics), "database_resource_spills_total 0\n") {
		t.Fatalf("missing spill evidence: status %d, %s", response.StatusCode, metrics)
	}
	files, err := filepath.Glob(filepath.Join(temporary, "database-sort-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("metadata failure left spill files: %v, %v", files, err)
	}
	next := newWireClient(t, instance.MySQLAddress, "admin", "lifecycle-secret")
	defer next.close()
	if result := next.query("SELECT 1"); result.err != "" || len(result.rows) != 1 || len(result.rows[0]) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("new connection result = %#v", result)
	}
}

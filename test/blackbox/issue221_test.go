package blackbox_test

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue221DoubleTextProtocolUsesDecimalNotation(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	result := client.query("SELECT 1e10, 1e20, 1e-10, -1e10, -1e-10, 1.25")
	if result.err != "" {
		t.Fatalf("SELECT DOUBLE literals: %#v", result)
	}
	want := [][]string{{"10000000000", "100000000000000000000", "0.0000000001", "-10000000000", "-0.0000000001", "1.25"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("wire DOUBLE literals = %#v, want %#v", result.rows, want)
	}
}

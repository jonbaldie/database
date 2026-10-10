package blackbox

import (
	"testing"
	"time"
)

func TestProcessSnapshotObservesStderrWhileWritesContinue(t *testing.T) {
	process := &Process{}
	writer := stderrWriter{output: &process.output}
	done := make(chan [2]string)
	go func() {
		_, _ = writer.Write([]byte("first\n"))
		_, _ = process.Snapshot()
		_, _ = writer.Write([]byte("second\n"))
		process.output.appendStdout("result")
		stdout, stderr := process.Snapshot()
		done <- [2]string{stdout, stderr}
	}()
	select {
	case got := <-done:
		if got[0] != "result\n" || got[1] != "first\nsecond\n" {
			t.Fatalf("snapshot = stdout %q stderr %q", got[0], got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot and stderr writes did not release the output lock")
	}
}

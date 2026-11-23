package mysql

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func spillResultForCleanup(t *testing.T) (*queryResult, *resourceManager, []string, *session) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes: 50, AggregateExecutionMemoryLimitBytes: 50,
		TemporaryStorageLimitBytes: 4096, AggregateTemporaryStorageLimitBytes: 4096,
	}}
	manager := newResourceManager(config)
	resources := newStatementResources(manager, config, nil)
	executor.session.resources = resources
	t.Cleanup(func() { closeStatementResources(resources) })
	result, err := executeStatement(executor, "SELECT name FROM authors ORDER BY name DESC")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(result.close)
	files, err := filepath.Glob(filepath.Join(directory, "database-sort-*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("expected spill files: %v, %v", files, err)
	}
	if usage := manager.usage(); usage.TemporaryStorageBytes == 0 {
		t.Fatalf("expected reserved spill bytes: %#v", usage)
	}
	return result, manager, files, executor.session
}

func assertSpillReleased(t *testing.T, manager *resourceManager, files []string) {
	t.Helper()
	for _, path := range files {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("result closure left spill file %s: %v", path, err)
		}
	}
	if usage := manager.usage(); usage.TemporaryStorageBytes != 0 || usage.ExecutionMemoryBytes != 0 {
		t.Errorf("result closure retained resources: %#v", usage)
	}
}

type failedResultOutput struct{}

func (failedResultOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestResultDeliveryReleasesSpillFiles(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode ResultMode
	}{{"text", ResultModeText}, {"binary", ResultModeBinary}} {
		for _, delivery := range []struct {
			name      string
			output    io.Writer
			maximum   int64
			wantError bool
		}{
			{"metadata-limit", io.Discard, 1, true},
			{"output-failure", failedResultOutput{}, 4096, true},
			{"complete", io.Discard, 4096, false},
		} {
			t.Run(mode.name+"/"+delivery.name, func(t *testing.T) {
				result, manager, files, _ := spillResultForCleanup(t)
				var packets bytes.Buffer
				output := delivery.output
				if !delivery.wantError {
					output = &packets
				}
				err := NewStreamWriter(output, 1, delivery.maximum).WriteResultSet(result, mode.mode)
				if (err != nil) != delivery.wantError {
					t.Fatalf("delivery error = %v", err)
				}
				if !delivery.wantError {
					for _, value := range []string{"Linus", "Grace", "Ada"} {
						if !bytes.Contains(packets.Bytes(), []byte(value)) {
							t.Errorf("result packets omitted %q: %x", value, packets.Bytes())
						}
					}
				}
				assertSpillReleased(t, manager, files)
				result.close()
				assertSpillReleased(t, manager, files)
			})
		}
	}
}

func TestResultCloseReleasesOwnershipOnce(t *testing.T) {
	calls := 0
	result := &queryResult{release: func() { calls++ }}
	result.close()
	result.close()
	if calls != 1 {
		t.Fatalf("release calls = %d, want 1", calls)
	}
}

func TestResultCloseWithoutDeliveryReleasesSpillFiles(t *testing.T) {
	result, manager, files, _ := spillResultForCleanup(t)
	result.close()
	result.close()
	assertSpillReleased(t, manager, files)
}

func TestResultEarlyStopReleasesSpillFiles(t *testing.T) {
	result, manager, files, _ := spillResultForCleanup(t)
	stopped := errors.New("stop delivery")
	if err := result.stream(func([]string, []bool) error { return stopped }); !errors.Is(err, stopped) {
		t.Fatalf("early-stop error = %v", err)
	}
	result.close()
	assertSpillReleased(t, manager, files)
}

func TestStatementRejectionReleasesAllocatedSpillResult(t *testing.T) {
	result, manager, files, session := spillResultForCleanup(t)
	cancel := make(chan struct{})
	session.resources.cancel = cancel
	close(cancel)
	rejection := session.checkStatementResources()
	if rejection == nil {
		t.Fatal("expected cancellation after result allocation")
	}
	// This is the finalization boundary used by statementExecutionPolicy after
	// its post-dispatch resource check. No row delivery has started.
	finished, err := (statementTransaction{}).finish(session, result, rejection)
	if finished != nil || !errors.Is(err, rejection) {
		t.Fatalf("statement finalization = %v, %v", finished, err)
	}
	assertSpillReleased(t, manager, files)
}

func TestInternalResultConsumersReleaseSpillFiles(t *testing.T) {
	for _, consumer := range []string{"discard", "materialize", "scalar-arity", "scalar-rows"} {
		t.Run(consumer, func(t *testing.T) {
			result, manager, files, _ := spillResultForCleanup(t)
			switch consumer {
			case "discard":
				rows, _, err := discardResultRows(result)
				if err != nil || rows != 3 {
					t.Fatalf("discard = %d, %v", rows, err)
				}
			case "materialize":
				materialized, err := materializeQueryResult(result)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(materialized.rows, [][]string{{"Linus"}, {"Grace"}, {"Ada"}}) {
					t.Fatalf("rows = %#v", materialized.rows)
				}
			case "scalar-arity":
				result.columns = append(result.columns, "extra")
				if _, _, err := scalarSubqueryValue(result); !isFailureCode(err, 1241) {
					t.Fatalf("scalar arity error = %v", err)
				}
			case "scalar-rows":
				if _, _, err := scalarSubqueryValue(result); !isFailureCode(err, 1242) {
					t.Fatalf("scalar rows error = %v", err)
				}
			}
			assertSpillReleased(t, manager, files)
		})
	}
}

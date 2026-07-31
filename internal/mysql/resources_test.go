package mysql

import (
	"testing"
	"time"
)

func TestStatementResourcesEnforceStatementMemoryAndReleaseIt(t *testing.T) {
	config := Config{ResourceLimits: ResourceLimits{ExecutionMemoryLimitBytes: 8, AggregateExecutionMemoryLimitBytes: 12}}
	manager := newResourceManager(config)
	resources := newStatementResources(manager, config, nil)
	if err := reserveStatementMemory(resources, 8); err != nil {
		t.Fatalf("reserve memory: %v", err)
	}
	if err := reserveStatementMemory(resources, 1); !isFailureCode(err, 1114) {
		t.Fatalf("per-statement memory error = %v", err)
	}
	closeStatementResources(resources)

	second := newStatementResources(manager, config, nil)
	defer closeStatementResources(second)
	if err := reserveStatementMemory(second, 8); err != nil {
		t.Fatalf("released memory remained reserved: %v", err)
	}
}

func TestStatementResourcesEnforceAggregateMemoryAndTemporaryStorage(t *testing.T) {
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes:           8,
		AggregateExecutionMemoryLimitBytes:  12,
		TemporaryStorageLimitBytes:          8,
		AggregateTemporaryStorageLimitBytes: 12,
	}}
	manager := newResourceManager(config)
	first := newStatementResources(manager, config, nil)
	defer closeStatementResources(first)
	second := newStatementResources(manager, config, nil)
	defer closeStatementResources(second)
	if err := reserveStatementMemory(first, 7); err != nil {
		t.Fatalf("first memory reservation: %v", err)
	}
	if err := reserveStatementMemory(second, 6); !isFailureCode(err, 1114) {
		t.Fatalf("aggregate memory error = %v", err)
	}
	if err := reserveStatementTemporary(first, 7); err != nil {
		t.Fatalf("first temporary reservation: %v", err)
	}
	if err := reserveStatementTemporary(second, 6); !isFailureCode(err, 1114) {
		t.Fatalf("aggregate temporary storage error = %v", err)
	}
}

func TestSpillWriterReservesEncodedTemporaryBytes(t *testing.T) {
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes: 1024, AggregateExecutionMemoryLimitBytes: 1024,
		TemporaryStorageLimitBytes: 1024, AggregateTemporaryStorageLimitBytes: 1024,
	}}
	manager := newResourceManager(config)
	resources := newStatementResources(manager, config, nil)
	defer closeStatementResources(resources)
	sorter := relationalSpillSorter{plan: &relationalSelectPlan{relationalSelectEnvironment: relationalSelectEnvironment{session: &session{resources: resources}}}}
	writer, err := sorter.newRunWriter()
	if err != nil {
		t.Fatalf("create spill writer: %v", err)
	}
	if err := encodeRelationalSpillRow(writer.encoder, relationalResultRow{
		values: []string{"payload"}, nulls: []bool{false},
		source: relationRow{values: []string{"1", "payload"}, lockKeys: []string{"1"}},
	}); err != nil {
		writer.abort()
		t.Fatalf("encode spill row: %v", err)
	}
	if err := writer.file.Sync(); err != nil {
		writer.abort()
		t.Fatalf("sync spill file: %v", err)
	}
	info, err := writer.file.Stat()
	if err != nil {
		writer.abort()
		t.Fatalf("stat spill file: %v", err)
	}
	if got := statementResourceSnapshot(resources).temporary; got != info.Size() {
		writer.abort()
		t.Fatalf("temporary reservation = %d, encoded file size = %d", got, info.Size())
	}
	run, err := writer.close()
	if err != nil {
		writer.abort()
		t.Fatalf("close spill writer: %v", err)
	}
	sorter.releaseRun(run)
	if usage := manager.usage(); usage.SpillBytes != info.Size() || usage.TemporaryStorageBytes != 0 {
		t.Fatalf("final spill usage = %#v, file size = %d", usage, info.Size())
	}
}

func TestStatementResourcesStopForCancellationAndDeadline(t *testing.T) {
	cancelled := make(chan struct{})
	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}, cancelled)
	close(cancelled)
	if err := resources.check(); !isFailureCode(err, 1317) {
		t.Fatalf("cancellation error = %v", err)
	}

	deadline := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}, nil)
	deadline.deadline = time.Now().Add(-time.Nanosecond)
	if err := deadline.check(); !isFailureCode(err, 3024) {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestStatementAdmissionAppliesDeadlineToSettingsAndMutations(t *testing.T) {
	executor := relationalSelectExecutor(t)
	config := Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}

	settings := newStatementResources(newResourceManager(config), config, nil)
	settings.deadline = time.Now().Add(-time.Nanosecond)
	executor.session.resources = settings
	if _, err := executor.execute("SET time_zone = '+01:00'"); !isFailureCode(err, 3024) {
		t.Fatalf("expired setting statement error = %v", err)
	}
	closeStatementResources(settings)

	mutation := newStatementResources(newResourceManager(config), config, nil)
	mutation.deadline = time.Now().Add(-time.Nanosecond)
	executor.session.resources = mutation
	if _, err := executor.execute("INSERT INTO authors VALUES (4, 'Margaret')"); !isFailureCode(err, 3024) {
		t.Fatalf("expired mutation statement error = %v", err)
	}
	closeStatementResources(mutation)
	executor.session.resources = nil

	result, err := executor.execute("SELECT name FROM authors WHERE id = 4")
	if err != nil {
		t.Fatalf("read after rejected mutation: %v", err)
	}
	if len(result.rows) != 0 {
		t.Fatalf("rejected mutation changed rows: %#v", result.rows)
	}
}

func TestExplicitCommitMakesPublicationTheStatementBoundary(t *testing.T) {
	executor := relationalSelectExecutor(t)
	cancelled := make(chan struct{})
	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}, cancelled)
	executor.session.resources = resources
	for _, query := range []string{
		"BEGIN",
		"INSERT INTO authors VALUES (4, 'Margaret')",
		"COMMIT",
	} {
		if _, err := executor.execute(query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	close(cancelled)
	resources.deadline = time.Now().Add(-time.Nanosecond)
	if err := resources.check(); err != nil {
		t.Fatalf("published commit was rejected by a later resource event: %v", err)
	}
	closeStatementResources(resources)
	executor.session.resources = nil
}

func TestImplicitPreDefinitionCommitKeepsResourceChecksActive(t *testing.T) {
	executor := relationalSelectExecutor(t)
	cancelled := make(chan struct{})
	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}, cancelled)
	executor.session.resources = resources
	defer closeStatementResources(resources)
	defer func() { executor.session.resources = nil }()
	for _, query := range []string{
		"BEGIN",
		"INSERT INTO authors VALUES (4, 'Margaret')",
		"CREATE TABLE drafts (id INT)",
	} {
		if _, err := executor.execute(query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if resources.finalized {
		t.Fatal("implicit pre-DDL commit finalized the continuing statement resources")
	}
	close(cancelled)
	if err := resources.check(); !isFailureCode(err, 1317) {
		t.Fatalf("DDL continuation did not observe cancellation: %v", err)
	}
}

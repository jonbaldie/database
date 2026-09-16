package blackbox_test

import (
	"sort"
	"strconv"
	"testing"
)

func FuzzBlackBoxSQLStateful(f *testing.F) {
	for _, operations := range [][]byte{
		{},
		{0, 1, 2, 3, 4, 5, 6, 7},
		{4, 3, 0, 1, 2, 4, 0, 2},
	} {
		f.Add(operations)
	}

	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 32 {
			operations = operations[:32]
		}
		harness := NewStatefulHarness(t)
		defer func() {
			if harness.serveProcess != nil {
				_ = harness.serveProcess.Stop()
				_ = harness.serveProcess.Wait()
			}
		}()

		assertStatefulStepSucceeds(t, harness.Execute(Step{Action: ActInit}), "init")
		assertStatefulStepSucceeds(t, harness.Execute(Step{Action: ActServeStart}), "serve")
		client := newWireClient(t, harness.mysqlAddr, harness.adminUser, harness.password)
		mustQuery(t, client, "CREATE DATABASE cgpt")
		mustQuery(t, client, "CREATE TABLE cgpt.items (id INT PRIMARY KEY, value INT NOT NULL)")
		mustQuery(t, client, "USE cgpt")
		model := map[int]int{}

		for index, operation := range operations {
			applyCGPTSQLAction(t, client, model, index, operation)
			assertCGPTRows(t, client, model, "after action "+strconv.Itoa(index))
		}
		_ = client.close()

		assertStatefulStepSucceeds(t, harness.Execute(Step{Action: ActShutdownSIGTERM}), "restart stop")
		assertStatefulStepSucceeds(t, harness.Execute(Step{Action: ActServeStart}), "restart serve")
		reopened := newWireClient(t, harness.mysqlAddr, harness.adminUser, harness.password)
		defer reopened.close()
		mustQuery(t, reopened, "USE cgpt")
		assertCGPTRows(t, reopened, model, "after restart")
	})
}

func assertStatefulStepSucceeds(t *testing.T, record StepRecord, action string) {
	t.Helper()
	if record.Err != nil || record.Result.ExitCode != 0 {
		t.Fatalf("%s: result=%#v err=%v", action, record.Result, record.Err)
	}
}

func applyCGPTSQLAction(t *testing.T, client *wireClient, model map[int]int, index int, operation byte) {
	t.Helper()
	id := index + 1
	value := int(int8(operation))
	switch operation % 5 {
	case 0:
		result := client.query("INSERT INTO items VALUES (" + strconv.Itoa(id) + ", " + strconv.Itoa(value) + ")")
		if result.err != "" {
			t.Fatalf("insert action %d: %#v", index, result)
		}
		model[id] = value
	case 1:
		keys := sortedCGPTKeys(model)
		if len(keys) == 0 {
			return
		}
		id = keys[int(operation)/len(keys)%len(keys)]
		value = int(int8(operation ^ 0x55))
		result := client.query("UPDATE items SET value = " + strconv.Itoa(value) + " WHERE id = " + strconv.Itoa(id))
		if result.err != "" {
			t.Fatalf("update action %d: %#v", index, result)
		}
		model[id] = value
	case 2:
		keys := sortedCGPTKeys(model)
		if len(keys) == 0 {
			return
		}
		id = keys[int(operation)/len(keys)%len(keys)]
		result := client.query("DELETE FROM items WHERE id = " + strconv.Itoa(id))
		if result.err != "" {
			t.Fatalf("delete action %d: %#v", index, result)
		}
		delete(model, id)
	case 3:
		keys := sortedCGPTKeys(model)
		if len(keys) == 0 {
			return
		}
		id = keys[int(operation)/len(keys)%len(keys)]
		result := client.query("INSERT INTO items VALUES (" + strconv.Itoa(id) + ", " + strconv.Itoa(value) + ")")
		if result.errCode != 1062 {
			t.Fatalf("duplicate action %d: %#v", index, result)
		}
	case 4:
		result := client.query("INSERT INTO items VALUES (" + strconv.Itoa(id+1000) + ", NULL)")
		if result.errCode != 1048 {
			t.Fatalf("null action %d: %#v", index, result)
		}
	}
}

func sortedCGPTKeys(model map[int]int) []int {
	keys := make([]int, 0, len(model))
	for key := range model {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func assertCGPTRows(t *testing.T, client *wireClient, model map[int]int, phase string) {
	t.Helper()
	result := client.query("SELECT id, value FROM items ORDER BY id")
	if result.err != "" {
		t.Fatalf("%s: select: %#v", phase, result)
	}
	keys := sortedCGPTKeys(model)
	if len(result.rows) != len(keys) {
		t.Fatalf("%s: row count=%d, want=%d, rows=%#v", phase, len(result.rows), len(keys), result.rows)
	}
	for index, key := range keys {
		if len(result.rows[index]) != 2 || result.rows[index][0] != strconv.Itoa(key) || result.rows[index][1] != strconv.Itoa(model[key]) {
			t.Fatalf("%s: row=%#v, want=[%d %d], all rows=%#v", phase, result.rows[index], key, model[key], result.rows)
		}
	}
}

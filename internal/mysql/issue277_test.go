package mysql

import (
	"reflect"
	"testing"
)

func TestIssue277WindowRowsPrecedingEndBoundEmptyFrame(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, val INT)",
		"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30), (4, 40), (5, 50), (6, 60)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	t.Run("preceding end bound empty frames", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT id, SUM(val) OVER (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 2 PRECEDING) AS s FROM t ORDER BY id")
		if err != nil {
			t.Fatalf("execute window query: %v", err)
		}

		want := [][]string{
			{"1", "NULL"},
			{"2", "NULL"},
			{"3", "10"},
			{"4", "30"},
			{"5", "60"},
			{"6", "100"},
		}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("preceding end bound empty frames all aggregates", func(t *testing.T) {
		query := `SELECT id,
			SUM(val) OVER w AS s,
			COUNT(val) OVER w AS c,
			MIN(val) OVER w AS mn,
			MAX(val) OVER w AS mx,
			AVG(val) OVER w AS a
		FROM t
		WINDOW w AS (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 2 PRECEDING)
		ORDER BY id`
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute window query: %v", err)
		}

		want := [][]string{
			{"1", "NULL", "0", "NULL", "NULL", "NULL"},
			{"2", "NULL", "0", "NULL", "NULL", "NULL"},
			{"3", "10", "1", "10", "10", "10.0000"},
			{"4", "30", "2", "10", "20", "15.0000"},
			{"5", "60", "3", "10", "30", "20.0000"},
			{"6", "100", "4", "10", "40", "25.0000"},
		}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("preceding end bound empty frames positional functions", func(t *testing.T) {
		query := `SELECT id,
			FIRST_VALUE(val) OVER w AS f,
			LAST_VALUE(val) OVER w AS l,
			NTH_VALUE(val, 1) OVER w AS n
		FROM t
		WINDOW w AS (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 2 PRECEDING)
		ORDER BY id`
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute window query: %v", err)
		}

		want := [][]string{
			{"1", "NULL", "NULL", "NULL"},
			{"2", "NULL", "NULL", "NULL"},
			{"3", "10", "10", "10"},
			{"4", "10", "20", "10"},
			{"5", "10", "30", "10"},
			{"6", "10", "40", "10"},
		}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("following start bound empty frames at partition end all aggregates", func(t *testing.T) {
		query := `SELECT id,
			SUM(val) OVER w AS s,
			COUNT(val) OVER w AS c,
			MIN(val) OVER w AS mn,
			MAX(val) OVER w AS mx
		FROM t
		WINDOW w AS (ORDER BY id ROWS BETWEEN 2 FOLLOWING AND 3 FOLLOWING)
		ORDER BY id`
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute window query: %v", err)
		}

		want := [][]string{
			{"1", "70", "2", "30", "40"},
			{"2", "90", "2", "40", "50"},
			{"3", "110", "2", "50", "60"},
			{"4", "60", "1", "60", "60"},
			{"5", "NULL", "0", "NULL", "NULL"},
			{"6", "NULL", "0", "NULL", "NULL"},
		}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("single row table empty frames", func(t *testing.T) {
		if _, err := executeStatement(executor, "CREATE TABLE single_t (id INT PRIMARY KEY, val INT)"); err != nil {
			t.Fatalf("create single_t: %v", err)
		}
		if _, err := executeStatement(executor, "INSERT INTO single_t VALUES (1, 42)"); err != nil {
			t.Fatalf("insert single_t: %v", err)
		}

		query := `SELECT id,
			SUM(val) OVER (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 2 PRECEDING) AS p_sum,
			COUNT(val) OVER (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 2 PRECEDING) AS p_cnt,
			SUM(val) OVER (ORDER BY id ROWS BETWEEN 2 FOLLOWING AND 5 FOLLOWING) AS f_sum,
			COUNT(val) OVER (ORDER BY id ROWS BETWEEN 2 FOLLOWING AND 5 FOLLOWING) AS f_cnt
		FROM single_t`
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute single row query: %v", err)
		}

		want := [][]string{
			{"1", "NULL", "0", "NULL", "0"},
		}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("genuine backwards movement returns error 1105", func(t *testing.T) {
		state := &rollingWindowAggregate{
			aggregate: relationalAggregate{name: "SUM"},
			kind:      rollingSum,
			inputs: []windowAggregateInput{
				{value: intValue(10), present: true},
				{value: intValue(20), present: true},
				{value: intValue(30), present: true},
			},
		}
		activeStart, activeEnd := 0, -1
		if err := state.advance(windowFrameRange{start: 1, end: 2}, &activeStart, &activeEnd); err != nil {
			t.Fatalf("first advance: %v", err)
		}
		// Try moving start backwards (from 1 to 0):
		errStart := state.advance(windowFrameRange{start: 0, end: 2}, &activeStart, &activeEnd)
		if errStart == nil {
			t.Fatalf("expected error for backwards start, got nil")
		}
		if failure, ok := errStart.(sqlFailure); !ok || failure.code != 1105 {
			t.Fatalf("expected error 1105, got: %v", errStart)
		}

		// Reset state and advance to {1, 2} again:
		state = &rollingWindowAggregate{
			aggregate: relationalAggregate{name: "SUM"},
			kind:      rollingSum,
			inputs: []windowAggregateInput{
				{value: intValue(10), present: true},
				{value: intValue(20), present: true},
				{value: intValue(30), present: true},
			},
		}
		activeStart, activeEnd = 0, -1
		if err := state.advance(windowFrameRange{start: 1, end: 2}, &activeStart, &activeEnd); err != nil {
			t.Fatalf("first advance: %v", err)
		}
		// Try moving end backwards (from 2 to 1):
		errEnd := state.advance(windowFrameRange{start: 1, end: 1}, &activeStart, &activeEnd)
		if errEnd == nil {
			t.Fatalf("expected error for backwards end, got nil")
		}
		if failure, ok := errEnd.(sqlFailure); !ok || failure.code != 1105 {
			t.Fatalf("expected error 1105, got: %v", errEnd)
		}
	})
}

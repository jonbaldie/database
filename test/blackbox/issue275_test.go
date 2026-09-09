package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue275CastSupportsTemporalBinaryAndApproximateTargets(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE issue275",
		"USE issue275",
		"CREATE TABLE events (id INT PRIMARY KEY, moment DATETIME)",
		"INSERT INTO events VALUES (1, '2024-01-15 10:30:00')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	cases := map[string]string{
		"SELECT CAST('2024-01-15' AS DATE)":                    "2024-01-15",
		"SELECT CAST('2024-01-15 10:30:00' AS DATETIME)":       "2024-01-15 10:30:00",
		"SELECT CAST('10:30:00' AS TIME)":                      "10:30:00",
		"SELECT CONVERT('2024-01-15', DATE)":                   "2024-01-15",
		"SELECT CAST(3 AS DOUBLE)":                             "3",
		"SELECT CAST(3 AS REAL)":                               "3",
		"SELECT CAST(3 AS FLOAT)":                              "3",
		"SELECT CAST('abc' AS BINARY)":                         "abc",
		"SELECT CAST(moment AS DATE) FROM events WHERE id = 1": "2024-01-15",
		"SELECT CAST('2024' AS YEAR)":                          "2024",
	}
	for query, want := range cases {
		result := client.query(query)
		if result.err != "" || len(result.rows) != 1 || result.rows[0][0] != want {
			t.Errorf("%s = %#v, want single row %q", query, result.rows, want)
		}
	}

	for _, query := range []string{
		"SELECT CAST('2024-02-30' AS DATE)",
		"SELECT CAST('abc' AS DOUBLE)",
		"SELECT CAST('abcd' AS BINARY(3))",
	} {
		if result := client.query(query); result.errCode == 0 {
			t.Errorf("%s succeeded, want a cast error", query)
		}
	}
}

# Reproduce the indexed range comparison

Run these commands from the repository root. Use Python 3 and the Go version in `go.mod`.

1. Create a temporary baseline worktree.

```sh
workspace="$(mktemp -d)"
git worktree add --detach "$workspace/before" 8039491
cp internal/mysql/indexed_range_benchmark_test.go "$workspace/before/internal/mysql/"
export GOMAXPROCS=2
export GOFLAGS='-p=2'
```

2. Compile both versions with the same client benchmark.

```sh
(cd "$workspace/before" && go test -c -o "$workspace/before.test" ./internal/mysql)
go test -c -o "$workspace/after.test" ./internal/mysql
```

3. Run five alternating measurements per version.

```sh
python3 scripts/benchmark-indexed-range.py \
	"$workspace/before.test" "$workspace/after.test" \
	--output "$workspace/comparison.json"
```

The command checks all returned rows. It exits with an error if a run fails, the timing ranges overlap, or throughput does not double.

4. Copy the comparison file to a location that you own.

5. To capture separate profiles, run each version with profiling enabled.

```sh
DATABASE_RANGE_CPU_PROFILE="$workspace/before.cpu" "$workspace/before.test" \
	-test.run='^$' -test.bench='^BenchmarkMySQLIndexedRangePage$' -test.benchtime=10000x
DATABASE_RANGE_CPU_PROFILE="$workspace/after.cpu" "$workspace/after.test" \
	-test.run='^$' -test.bench='^BenchmarkMySQLIndexedRangePage$' -test.benchtime=10000x
go tool pprof -top "$workspace/before.test" "$workspace/before.cpu"
go tool pprof -top "$workspace/after.test" "$workspace/after.cpu"
```

6. Remove the temporary baseline worktree and its files.

```sh
rm "$workspace/before/internal/mysql/indexed_range_benchmark_test.go"
git worktree remove "$workspace/before"
rm -rf "$workspace"
```

The measured run excludes setup and profile work. The benchmark uses one authenticated session and a warm ordered-index cache. It does not run the full release acceptance scenario.

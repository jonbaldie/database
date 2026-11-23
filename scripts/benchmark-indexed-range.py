#!/usr/bin/env python3
"""Compare identical wire benchmarks from two compiled Go test binaries."""

import argparse
import json
import os
from pathlib import Path
import platform
import re
import statistics
import subprocess
import sys


def measure(executable):
    env = dict(os.environ, GOMAXPROCS="2")
    env.pop("DATABASE_RANGE_CPU_PROFILE", None)
    result = subprocess.run(
        [str(executable), "-test.run=^$", "-test.bench=^BenchmarkMySQLIndexedRangePage$",
         "-test.benchtime=10000x", "-test.count=1"],
        env=env, capture_output=True, text=True, check=True, timeout=120,
    )
    match = re.search(
        r"^BenchmarkMySQLIndexedRangePage-2\s+(\d+)\s+([\d.]+) ns/op\s+([\d.]+) rows/op",
        result.stdout, re.MULTILINE,
    )
    if match is None or "PASS" not in result.stdout:
        raise RuntimeError(result.stdout + result.stderr)
    operations, nanoseconds, rows = map(float, match.groups())
    if operations != 10000 or rows != 20:
        raise RuntimeError("The benchmark did not complete the required work.")
    return {"operations": int(operations), "verified_rows": int(operations * rows),
            "ns_per_op": nanoseconds, "errors": 0, "output": result.stdout}


def summary(runs):
    values = [run["ns_per_op"] for run in runs]
    median = statistics.median(values)
    return {"median_ns_per_op": median, "min_ns_per_op": min(values),
            "max_ns_per_op": max(values), "requests_per_second": 1e9 / median}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    paths = {"before": args.before.resolve(strict=True), "after": args.after.resolve(strict=True)}
    runs = {"before": [], "after": []}
    for repetition in range(5):
        for side in ("before", "after"):
            print(f"Run {repetition + 1}/5 for {side}.", file=sys.stderr)
            runs[side].append(measure(paths[side]))
    summaries = {side: summary(values) for side, values in runs.items()}
    ratio = summaries["before"]["median_ns_per_op"] / summaries["after"]["median_ns_per_op"]
    separated = summaries["after"]["max_ns_per_op"] < summaries["before"]["min_ns_per_op"]
    report = {
        "schema": "database.indexed-range-comparison/v1", "environment": platform.platform(),
        "runtime_processors": 2, "sessions": 1, "table_rows": 100000, "rows_per_request": 20,
        "profiled": False, "runs": runs, "summary": summaries,
        "throughput_ratio": ratio, "throughput_increase_percent": (ratio - 1) * 100,
        "latency_reduction_percent": (1 - 1 / ratio) * 100,
        "ranges_separated": separated, "target_met": ratio >= 2 and separated,
    }
    encoded = json.dumps(report, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
    else:
        print(encoded, end="")
    if not report["target_met"]:
        raise SystemExit("The measured result does not meet the twofold target.")


if __name__ == "__main__":
    main()

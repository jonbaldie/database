#!/bin/sh
set -eu

npx --yes --package=ajv-cli@5.0.0 --package=ajv-formats@3.0.1 -- \
  ajv validate --spec=draft2020 -c ajv-formats \
  -s docs/query-explanation/explain-v1.schema.json \
  -d docs/query-explanation/examples/plan.json \
  -d docs/query-explanation/examples/analyze.json \
  -d docs/query-explanation/examples/snapshot.json \
  -d docs/query-explanation/examples/write.json

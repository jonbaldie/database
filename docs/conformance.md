# v0.1 Conformance Evidence

The repository-wide verification seam is the built `database` executable and its
black-box tests. Run `go test ./...` from the repository root for the complete
currently implemented evidence set. Run `scripts/build-release.sh` to produce
the supported native release binaries and `scripts/performance.sh` for the
repeatable verification record.

[The draft v0.1 conformance evidence map](conformance-evidence.md) links each
Issue #1 story to its contract and current evidence. It states the remaining
contract inventory and release evidence work.

The public seams covered by the current evidence are version reporting,
initialization, lifecycle and diagnostics, MySQL handshake and text/prepared
query commands, transaction controls, scalar expressions, DDL/mutation command
outcomes, catalog probes, operator result records, and release build entry
points. Unsupported behavior returns explicit protocol or operator errors.

The normative [operator automation contract](operator-automation.md) defines
the complete command, stream, progress, result, diagnostic, and exit-class
surface. Implementations must preserve that contract even while individual
workflow coverage is delivered by separate tickets.

The normative [performance acceptance scenario](performance-acceptance.md)
defines the fixed corpus, application-visible operations, load boundary,
measurement and repetition rules, clean-start gate, and published release
evidence. `make performance` runs the real SQL-driver harness and writes
`dist/performance-evidence.json`. Its default settings implement the full
acceptance scenario. Reduced flags are useful for local diagnostics only and
cannot establish the release judgment.

The normative [catalog metadata contract](catalog-metadata.md) defines the
closed visibility boundary, supported `SHOW` statements,
`information_schema` views, MySQL-shaped metadata, canonical definitions,
snapshot consistency, and explicit-failure behavior. Implementations and
conformance evidence must preserve that contract as catalog coverage expands.

The [distribution evidence](distribution.md) records the finite native and OCI
runtime contract, the reproducible release build, and the tested examples. It
keeps tested products and machines separate from the supported runtime
boundaries.

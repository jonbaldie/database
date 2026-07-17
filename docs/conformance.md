# v0.1 Conformance Evidence

The repository-wide verification seam is the built `database` executable and its
black-box tests. Run `go test ./...` from the repository root for the complete
currently implemented evidence set. Run `scripts/build-release.sh` to produce
the supported native release binaries and `scripts/performance.sh` for the
repeatable verification record.

The public seams covered by the current evidence are version reporting,
initialization, lifecycle and diagnostics, MySQL handshake and text/prepared
query commands, transaction controls, scalar expressions, DDL/mutation command
outcomes, catalog probes, operator result records, and release build entry
points. Unsupported behavior returns explicit protocol or operator errors.

The normative [operator automation contract](operator-automation.md) defines
the complete command, stream, progress, result, diagnostic, and exit-class
surface. Implementations must preserve that contract even while individual
workflow coverage is delivered by separate tickets.

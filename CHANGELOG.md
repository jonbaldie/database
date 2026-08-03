# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-03

### Added

- First public release of the experimental single-node database server in Go.
- MySQL 8.4 classic-protocol application profile with `caching_sha2_password`
  authentication, text and prepared execution, and explicit failure for
  unsupported protocol features.
- Documented MySQL 8.4.11 SQL subset for namespaces, tables, B-tree indexes,
  constraints, CRUD, joins, subqueries, CTEs, set operations, aggregates,
  windows, transactions, catalog metadata, and account administration.
- Versioned query explanation contract (JSON format `1` and stable tabular
  projection), including plan-only, analyzed, and live snapshot modes.
- Operator command family: `init`, `serve`, `shutdown`, `backup`, `restore`,
  `upgrade`, `config validate`, `data validate`, `data inspect`, and `version`.
- Closed server configuration and session settings registries with bounded
  defaults.
- Diagnostics listener for `/live`, `/ready`, and `/metrics`, plus structured
  lifecycle events.
- Native release artifacts for `darwin/arm64`, `linux/amd64`, and `linux/arm64`,
  plus a multi-architecture OCI image index.
- Public conformance, compatibility, distribution, and performance acceptance
  evidence under `docs/`.

### Notes

- v0.1.0 is experimental. It does not claim production readiness or complete
  MySQL compatibility beyond the documented contracts.
- Parent delivery map: https://github.com/jonbaldie/database/issues/1

[0.1.0]: https://github.com/jonbaldie/database/releases/tag/v0.1.0

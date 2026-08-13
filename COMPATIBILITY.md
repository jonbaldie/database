# Release and compatibility policy

## Releases

database releases follow [Semantic Versioning](https://semver.org/). The
project changelog follows [Keep a Changelog](https://keepachangelog.com/).

## 0.x compatibility

During `0.x`, patch releases preserve documented public compatibility. Minor
releases may make breaking public changes. A breaking `0.x` change requires a
prominent changelog entry and migration notes; it does not require a prior
deprecation release.

Compatibility commitments cover documented public behaviour and formats,
including the [MySQL SQL behaviour contract](docs/mysql-sql-behaviour.md) and
versioned query-explanation contract. The downstream operational contract
defines supported upgrade behaviour.

Internal Go packages, component interfaces, and physical storage formats are
not public compatibility surfaces.

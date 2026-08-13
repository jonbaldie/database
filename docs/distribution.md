# v0.2.0 distribution evidence

This document separates the v0.2.0 runtime contract from the environments used
to test the release artifacts. A test on one machine is evidence for that
artifact; it does not widen or replace the support contract.

## Supported runtime contract

The release supports these finite environments:

| Artifact | Contract |
|---|---|
| Native macOS | Apple Silicon (`arm64`) on macOS 14 Sonoma or a later generally available macOS release |
| Native Linux amd64 | Linux kernel 5.15 or later, glibc 2.35 or later, and x86-64-v1 |
| Native Linux arm64 | Linux kernel 5.15 or later, glibc 2.35 or later, and ARMv8.0-A/AArch64 |
| OCI image | OCI Image and Image Index 1.1.1, `linux/amd64` and `linux/arm64/v8`, on an OCI Runtime 1.2.1 or later host with Linux kernel 5.15 or later |

Native Linux support is for GNU libc environments. musl, uClibc, other libc
implementations, emulated or translated execution, and optional CPU
extensions are outside the native contract. Intel macOS, macOS 13 and older,
and prerelease macOS versions are outside the contract. The OCI image supplies
its userspace, so host userspace is not part of the OCI contract. Docker,
Podman, containerd, Kubernetes, and other named products are test examples,
not separate compatibility promises.

These boundaries remain stable within `0.2.x`. A later release may widen them
but cannot withdraw an established supported environment.

## Reproducible artifacts

The release build uses Go `1.26.5`, `CGO_ENABLED=0`, `-trimpath`,
`-buildvcs=false`, fixed product and build identity linker values, and a
recorded `SOURCE_DATE_EPOCH`. It writes three versioned native binaries, an
OCI layout archive, `SHA256SUMS`, and `release-manifest.json` under `dist/`.
The manifest is `database.release/v1` and records each target, baseline, and
digest.

```sh
VERSION=0.2.0 BUILD_IDENTITY=release ./scripts/build-release.sh
./scripts/verify-release.sh
```

The build produces:

- `database-0.2.0-darwin-arm64`;
- `database-0.2.0-linux-amd64`;
- `database-0.2.0-linux-arm64`; and
- `database-0.2.0-oci.tar`, an OCI image index with both Linux platforms.

The verification script checks the manifest and all digests, inspects the OCI
index, runs the macOS binary on a matching host, runs both Linux binaries in
glibc containers, and builds and initializes an instance with both OCI
variants. It needs Docker for Linux and OCI checks. When the host is not
Apple Silicon macOS, it records the macOS artifact as checksum-verified but
does not claim to have executed that binary.

## Tested examples

The release evidence for this repository was collected on an Apple Silicon
iMac running macOS 26.5.2 with Docker CLI 29.4.0 and OrbStack Buildx/BuildKit.
The native macOS binary
ran directly on that host. The native Linux binaries ran in
`debian:bookworm-slim` (glibc 2.36) containers for `linux/amd64` and
`linux/arm64/v8`. The generated OCI archive's index listed the same two
platforms. Matching OCI variants were built and each initialized a stopped
database instance.

These examples exercise process startup, version reporting, password input,
and durable instance initialization. They do not claim that every GNU/Linux
distribution, container product, kernel, or CPU has been tested. The broader
server and protocol behavior remains covered by the repository's black-box and
driver compatibility tests.

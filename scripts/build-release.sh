#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR=${DIST_DIR:-"$ROOT/dist"}
VERSION=${VERSION:-0.1.0}
BUILD_IDENTITY=${BUILD_IDENTITY:-release}
OCI_IMAGE=${OCI_IMAGE:-database}
OCI_TAG=${OCI_TAG:-$VERSION}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" show -s --format=%ct HEAD 2>/dev/null || date +%s)}
GO=${GO:-go}
DOCKER=${DOCKER:-docker}
BUILDX_BUILDER=${BUILDX_BUILDER:-database-release}

case "$VERSION" in
  ''|*[!A-Za-z0-9._+-]*) echo "VERSION contains unsupported characters" >&2; exit 2 ;;
esac
case "$BUILD_IDENTITY" in
  ''|*[!A-Za-z0-9._+-]*) echo "BUILD_IDENTITY contains unsupported characters" >&2; exit 2 ;;
esac
case "$OCI_IMAGE:$OCI_TAG" in
  *[!A-Za-z0-9._/:+-]*) echo "OCI_IMAGE or OCI_TAG contains unsupported characters" >&2; exit 2 ;;
esac
case "$SOURCE_DATE_EPOCH" in
  ''|*[!0-9]*) echo "SOURCE_DATE_EPOCH must be a non-negative integer" >&2; exit 2 ;;
esac

mkdir -p "$DIST_DIR"

LDFLAGS="-s -w -X github.com/jonbaldie/database/internal/buildinfo.ProductVersion=$VERSION -X github.com/jonbaldie/database/internal/buildinfo.BuildIdentity=$BUILD_IDENTITY"

build_native() {
  os=$1
  arch=$2
  artifact="$DIST_DIR/database-$VERSION-$os-$arch"
  echo "building $artifact"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$GO" build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$artifact" "$ROOT/cmd/database"
  chmod 0755 "$artifact"
}

sha256_file() {
  file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

build_native darwin arm64
build_native linux amd64
build_native linux arm64

if ! command -v "$DOCKER" >/dev/null 2>&1; then
  echo "Docker is required for the multi-architecture OCI artifact" >&2
  exit 1
fi
if ! "$DOCKER" buildx inspect "$BUILDX_BUILDER" >/dev/null 2>&1; then
  "$DOCKER" buildx create --name "$BUILDX_BUILDER" --driver docker-container --use >/dev/null
else
  "$DOCKER" buildx use "$BUILDX_BUILDER"
fi

OCI_ARCHIVE="$DIST_DIR/database-$VERSION-oci.tar"
echo "building $OCI_IMAGE:$OCI_TAG -> $OCI_ARCHIVE"
"$DOCKER" buildx build \
  --builder "$BUILDX_BUILDER" \
  --platform linux/amd64,linux/arm64/v8 \
  --provenance=false \
  --sbom=false \
  --build-arg VERSION="$VERSION" \
  --build-arg BUILD_IDENTITY="$BUILD_IDENTITY" \
  --build-arg SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
  --tag "$OCI_IMAGE:$OCI_TAG" \
  --output "type=oci,dest=$OCI_ARCHIVE,oci-mediatypes=true" \
  "$ROOT"

# Buildx can omit the default ARM64 variant from a nested OCI index. Add it so
# the published index states the supported linux/arm64/v8 contract literally.
python3 - "$OCI_ARCHIVE" <<'PY'
import copy
import hashlib
import io
import json
import os
import pathlib
import sys
import tarfile

archive_path = pathlib.Path(sys.argv[1])
with tarfile.open(archive_path, "r") as archive:
    members = {}
    for member in archive.getmembers():
        content = archive.extractfile(member).read() if member.isfile() else None
        members[member.name] = (copy.copy(member), content)

root_name = "index.json"
root_member, root_content = members[root_name]
root_index = json.loads(root_content)
changed = False
for descriptor in root_index.get("manifests", []):
    media_type = descriptor.get("mediaType", "")
    if not media_type.endswith("image.index.v1+json") or "platform" in descriptor:
        continue
    old_digest = descriptor["digest"]
    old_name = "blobs/sha256/" + old_digest.split(":", 1)[1]
    nested_member, nested_content = members[old_name]
    nested_index = json.loads(nested_content)
    nested_changed = False
    for manifest in nested_index.get("manifests", []):
        platform = manifest.get("platform", {})
        if (
            platform.get("os") == "linux"
            and platform.get("architecture") == "arm64"
            and "variant" not in platform
        ):
            platform["variant"] = "v8"
            nested_changed = True
    if not nested_changed:
        continue
    new_content = (json.dumps(nested_index, sort_keys=True, separators=(",", ":")) + "\n").encode()
    new_digest = "sha256:" + hashlib.sha256(new_content).hexdigest()
    new_name = "blobs/sha256/" + new_digest.split(":", 1)[1]
    del members[old_name]
    nested_member.name = new_name
    nested_member.size = len(new_content)
    members[new_name] = (nested_member, new_content)
    descriptor["digest"] = new_digest
    descriptor["size"] = len(new_content)
    changed = True

if changed:
    root_content = (json.dumps(root_index, sort_keys=True, separators=(",", ":")) + "\n").encode()
    root_member.size = len(root_content)
    members[root_name] = (root_member, root_content)

if changed:
    temporary = archive_path.with_suffix(archive_path.suffix + ".tmp")
    with tarfile.open(temporary, "w", format=tarfile.GNU_FORMAT) as output:
        for name in sorted(members):
            member, content = members[name]
            member = copy.copy(member)
            member.mtime = 0
            member.uid = 0
            member.gid = 0
            member.uname = ""
            member.gname = ""
            member.pax_headers = {}
            if content is None:
                output.addfile(member)
            else:
                member.size = len(content)
                output.addfile(member, io.BytesIO(content))
    os.replace(temporary, archive_path)
PY

CHECKSUMS="$DIST_DIR/SHA256SUMS"
: > "$CHECKSUMS"
for artifact in \
  "$DIST_DIR/database-$VERSION-darwin-arm64" \
  "$DIST_DIR/database-$VERSION-linux-amd64" \
  "$DIST_DIR/database-$VERSION-linux-arm64" \
  "$OCI_ARCHIVE"; do
  printf '%s  %s\n' "$(sha256_file "$artifact")" "$(basename "$artifact")" >> "$CHECKSUMS"
done

oci_sha256=$(sha256_file "$OCI_ARCHIVE")
darwin_sha256=$(sha256_file "$DIST_DIR/database-$VERSION-darwin-arm64")
linux_amd64_sha256=$(sha256_file "$DIST_DIR/database-$VERSION-linux-amd64")
linux_arm64_sha256=$(sha256_file "$DIST_DIR/database-$VERSION-linux-arm64")
cat > "$DIST_DIR/release-manifest.json" <<EOF
{
  "schema": "database.release/v1",
  "version": "$VERSION",
  "build_identity": "$BUILD_IDENTITY",
  "source_date_epoch": $SOURCE_DATE_EPOCH,
  "native": [
    {"name":"database-$VERSION-darwin-arm64","sha256":"$darwin_sha256","goos":"darwin","goarch":"arm64","baseline":"macOS 14+ on Apple Silicon"},
    {"name":"database-$VERSION-linux-amd64","sha256":"$linux_amd64_sha256","goos":"linux","goarch":"amd64","baseline":"Linux 5.15+; glibc 2.35+; x86-64-v1"},
    {"name":"database-$VERSION-linux-arm64","sha256":"$linux_arm64_sha256","goos":"linux","goarch":"arm64","baseline":"Linux 5.15+; glibc 2.35+; ARMv8.0-A/AArch64"}
  ],
  "oci": {
    "name": "$(basename "$OCI_ARCHIVE")",
    "sha256": "$oci_sha256",
    "image": "$OCI_IMAGE:$OCI_TAG",
    "format": "OCI Image Index 1.1.1",
    "runtime_baseline": "OCI Runtime 1.2.1+ on Linux 5.15+",
    "platforms": ["linux/amd64", "linux/arm64/v8"]
  }
}
EOF

echo "release artifacts written to $DIST_DIR"

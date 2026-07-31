#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR=${DIST_DIR:-"$ROOT/dist"}
VERSION=${VERSION:-0.1.0}
BUILD_IDENTITY=${BUILD_IDENTITY:-release}
DOCKER=${DOCKER:-docker}
BUILDX_BUILDER=${BUILDX_BUILDER:-database-release}

manifest="$DIST_DIR/release-manifest.json"
oci_archive="$DIST_DIR/database-$VERSION-oci.tar"

if [ ! -f "$manifest" ]; then
  echo "release manifest is missing: $manifest" >&2
  exit 1
fi
if [ ! -f "$oci_archive" ]; then
  echo "OCI archive is missing: $oci_archive" >&2
  exit 1
fi

python3 - "$manifest" "$DIST_DIR" "$VERSION" <<'PY'
import hashlib
import json
import pathlib
import sys
import tarfile

manifest_path = pathlib.Path(sys.argv[1])
dist = pathlib.Path(sys.argv[2])
version = sys.argv[3]
data = json.loads(manifest_path.read_text())
if data.get("schema") != "database.release/v1":
    raise SystemExit("unexpected release manifest schema")
if data.get("version") != version:
    raise SystemExit("release manifest version does not match VERSION")
expected_native = {"darwin/arm64", "linux/amd64", "linux/arm64"}
actual_native = {f"{item['goos']}/{item['goarch']}" for item in data.get("native", [])}
if actual_native != expected_native:
    raise SystemExit(f"native targets = {sorted(actual_native)!r}")
if data.get("oci", {}).get("platforms") != ["linux/amd64", "linux/arm64/v8"]:
    raise SystemExit("release manifest OCI platforms are not linux/amd64 and linux/arm64/v8")
if data.get("oci", {}).get("format") != "OCI Image Index 1.1.1":
    raise SystemExit("release manifest does not declare OCI Image Index 1.1.1")
for item in data["native"]:
    path = dist / item["name"]
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if digest != item["sha256"]:
        raise SystemExit(f"checksum mismatch: {path}")
oci_path = dist / data["oci"]["name"]
if hashlib.sha256(oci_path.read_bytes()).hexdigest() != data["oci"]["sha256"]:
    raise SystemExit("checksum mismatch: OCI archive")
with tarfile.open(oci_path) as archive:
    layout = json.loads(archive.extractfile("oci-layout").read())
    if layout.get("imageLayoutVersion") != "1.0.0":
        raise SystemExit("unexpected OCI layout version")
    index = json.loads(archive.extractfile("index.json").read())

    def blob_json(digest):
        return json.loads(archive.extractfile("blobs/sha256/" + digest.split(":", 1)[1]).read())

    nested_indexes = [item for item in index.get("manifests", []) if item.get("mediaType", "").endswith("image.index.v1+json")]
    if len(nested_indexes) == 1 and "platform" not in nested_indexes[0]:
        index = blob_json(nested_indexes[0]["digest"])
    platforms = {
        f"{item['platform']['os']}/{item['platform']['architecture']}"
        + (f"/{item['platform']['variant']}" if item["platform"].get("variant") else "")
        for item in index.get("manifests", [])
        if "platform" in item
    }
    if platforms != {"linux/amd64", "linux/arm64/v8"}:
        raise SystemExit(f"OCI platforms = {sorted(platforms)!r}")
print("manifest, checksums, and OCI index verified")
PY

SOURCE_DATE_EPOCH=$(python3 - "$manifest" <<'PY'
import json
import sys
print(json.load(open(sys.argv[1]))["source_date_epoch"])
PY
)

run_native() {
  os=$1
  arch=$2
  platform=$3
  artifact="$DIST_DIR/database-$VERSION-$os-$arch"
  if [ "$os" = "darwin" ]; then
    if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
      echo "skipping $os/$arch on this host; the artifact remains checksum-verified"
      return
    fi
  fi
  data_directory=$(mktemp -d "${TMPDIR:-/tmp}/database-release.XXXXXX")
  trap 'rm -rf "$data_directory"' EXIT HUP INT TERM
  if [ "$os" = "darwin" ]; then
    "$artifact" version --format=json | grep -F '"platform":"darwin/arm64"' >/dev/null
    printf '%s\n' release-password | "$artifact" init "$data_directory" --password-stdin >/dev/null
  else
    "$DOCKER" run --rm --platform "$platform" \
      -v "$artifact:/database:ro" -v "$data_directory:/data" \
      debian:bookworm-slim /database version --format=json | grep -F '"platform":"'$os'/'$arch'"' >/dev/null
    printf '%s\n' release-password | "$DOCKER" run --rm --platform "$platform" -i \
      -v "$artifact:/database:ro" -v "$data_directory:/data" \
      debian:bookworm-slim /database init /data --password-stdin >/dev/null
  fi
  rm -rf "$data_directory"
  trap - EXIT HUP INT TERM
  echo "$os/$arch native artifact started and initialized an instance"
}

if ! command -v "$DOCKER" >/dev/null 2>&1; then
  echo "Docker is required to verify Linux and OCI artifacts" >&2
  exit 1
fi

run_native darwin arm64 darwin/arm64
run_native linux amd64 linux/amd64
run_native linux arm64 linux/arm64/v8

if ! "$DOCKER" buildx inspect "$BUILDX_BUILDER" >/dev/null 2>&1; then
  "$DOCKER" buildx create --name "$BUILDX_BUILDER" --driver docker-container --use >/dev/null
else
  "$DOCKER" buildx use "$BUILDX_BUILDER"
fi

for platform in linux/amd64 linux/arm64/v8; do
  image="database-release-smoke:${VERSION}-$(printf '%s' "$platform" | tr '/:' '-')"
  "$DOCKER" buildx build \
    --builder "$BUILDX_BUILDER" \
    --platform "$platform" \
    --provenance=false \
    --sbom=false \
    --build-arg VERSION="$VERSION" \
    --build-arg BUILD_IDENTITY="$BUILD_IDENTITY" \
    --build-arg SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
    --tag "$image" \
    --load \
    "$ROOT" >/dev/null
  data_directory=$(mktemp -d "${TMPDIR:-/tmp}/database-oci-release.XXXXXX")
  trap 'rm -rf "$data_directory"' EXIT HUP INT TERM
  printf '%s\n' release-password | "$DOCKER" run --rm --platform "$platform" -i \
    -v "$data_directory:/data" "$image" init /data --password-stdin >/dev/null
  "$DOCKER" run --rm --platform "$platform" "$image" version --format=json \
    | grep -F '"product_version":"'$VERSION'"' >/dev/null
  rm -rf "$data_directory"
  trap - EXIT HUP INT TERM
  echo "$platform OCI variant built and initialized an instance"
done

echo "release smoke verification passed"

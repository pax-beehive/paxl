#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_paxl.sh [patch|minor|major|<version>] [tag[,tag...]]

Build paxl for supported platforms, smoke-test the native binary, upload
artifacts and release metadata to GCS, and tag the uploaded semantic version.

Defaults:
  version bump: patch
  tags: stable
  bucket: pax-tech-bucket
  object prefix: paxl/releases/<version>/
  platforms: darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

Environment overrides:
  PAX_RELEASE_BUCKET      GCS bucket name.
  PAX_RELEASE_PREFIX      GCS object prefix parent.
  PAX_RELEASE_TAGS        Comma-separated tags. Overrides the second argument.
  PAX_RELEASE_PLATFORMS   Space-separated GOOS/GOARCH platforms.
  PAX_RELEASE_BUILD_ID    Build id stored in metadata. Defaults to git short SHA.
  PAX_RELEASE_DIST_DIR    Local output directory. Defaults to dist.
  PAX_RELEASE_INSTALLER_OBJECT GCS object for installer. Defaults to paxl/install.sh.
  PAX_RELEASE_MANAGER_URL Public manager API base URL. Defaults to https://api.paxtech.net.
  PAX_RELEASE_ID_TOKEN    Bearer token for artifact metadata publish. Defaults to gcloud.
  PAX_RELEASE_DRY_RUN=1   Build and smoke-test without upload or git tag.
  PAX_RELEASE_SKIP_UPLOAD=1 Build only; also skips git tag.
  PAX_RELEASE_SKIP_VERIFY=1
  PAX_RELEASE_SKIP_INSTALLER=1
  PAX_RELEASE_SKIP_METADATA=1 Skip manager metadata publish and resolver verification.
  PAX_RELEASE_SKIP_TAG=1
  PAX_RELEASE_PUSH_TAG=1
  PAX_RELEASE_ALLOW_DIRTY=1

Examples:
  scripts/release_paxl.sh
  scripts/release_paxl.sh minor beta
  scripts/release_paxl.sh 0.2.0 stable
  PAX_RELEASE_DRY_RUN=1 scripts/release_paxl.sh patch stable
EOF
}

log() {
  printf '==> %s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

semver_re='^[0-9]+\.[0-9]+\.[0-9]+$'

source_version() {
  python3 - <<'PY'
import re
from pathlib import Path

text = Path("cmd/paxl/main.go").read_text(encoding="utf-8")
match = re.search(r'var\s+version\s*=\s*"([^"]+)"', text)
if not match:
    raise SystemExit("missing cmd/paxl/main.go version")
print(match.group(1))
PY
}

latest_release_version() {
  local tag version

  tag="$(git tag --list 'paxl/v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n 1)"
  if [[ -n "$tag" ]]; then
    version="${tag#paxl/v}"
    printf '%s' "$version"
    return
  fi
  source_version
}

next_semver() {
  local current="$1"
  local bump="$2"
  local major minor patch

  [[ "$current" =~ $semver_re ]] || fail "current version is not semantic: $current"
  IFS=. read -r major minor patch <<<"$current"
  case "$bump" in
    major) printf '%s.0.0' "$((major + 1))" ;;
    minor) printf '%s.%s.0' "$major" "$((minor + 1))" ;;
    patch) printf '%s.%s.%s' "$major" "$minor" "$((patch + 1))" ;;
    *) fail "unsupported version bump: $bump" ;;
  esac
}

resolve_version() {
  local requested="${1:-patch}"
  local current

  case "$requested" in
    major | minor | patch)
      current="$(latest_release_version)"
      next_semver "$current" "$requested"
      ;;
    *)
      [[ "$requested" =~ $semver_re ]] || fail "version must be major, minor, patch, or X.Y.Z"
      printf '%s' "$requested"
      ;;
  esac
}

ensure_clean_tree() {
  if [[ "${PAX_RELEASE_ALLOW_DIRTY:-0}" == "1" || "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    return
  fi
  git diff --quiet || fail "working tree has unstaged changes; commit or set PAX_RELEASE_ALLOW_DIRTY=1"
  git diff --cached --quiet || fail "working tree has staged changes; commit or set PAX_RELEASE_ALLOW_DIRTY=1"
}

sha256_file() {
  local path="$1"

  if command -v shasum >/dev/null 2>&1; then
    LC_ALL=C LANG=C shasum -a 256 "$path" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    fail "missing shasum or sha256sum"
  fi
}

file_size() {
  python3 -c 'import os, sys; print(os.path.getsize(sys.argv[1]))' "$1"
}

artifact_name_for() {
  local version="$1"
  local platform="$2"
  local os="${platform%/*}"
  local arch="${platform#*/}"

  printf 'paxl_%s_%s_%s' "$version" "$os" "$arch"
}

tag_json_array() {
  python3 - "$1" <<'PY'
import json
import sys

tags = [tag.strip() for tag in sys.argv[1].split(",") if tag.strip()]
print(json.dumps(tags, separators=(",", ":")))
PY
}

content_type_for() {
  printf '%s' "application/octet-stream"
}

smoke_test_binary() {
  local platform="$1"
  local output="$2"
  local expected_version="$3"
  local expected_commit="$4"
  local os="${platform%/*}"
  local arch="${platform#*/}"
  local host_os host_arch actual_version actual_commit actual_dirty

  host_os="$(go env GOOS)"
  host_arch="$(go env GOARCH)"
  if [[ "$os" != "$host_os" || "$arch" != "$host_arch" ]]; then
    log "skipping smoke test for ${platform}; host is ${host_os}/${host_arch}"
    return
  fi

  log "smoke-testing ${output}"
  actual_version="$("$output" version | awk 'NR == 1 {print $2}')"
  actual_commit="$("$output" version | awk 'NR == 2 {print $2}')"
  actual_dirty="$("$output" version | awk 'NR == 3 {print $2}')"
  [[ "$actual_version" == "$expected_version" ]] ||
    fail "smoke test version mismatch for ${platform}: ${actual_version}, expected ${expected_version}"
  [[ "$actual_commit" == "$expected_commit" ]] ||
    fail "smoke test commit mismatch for ${platform}: ${actual_commit}, expected ${expected_commit}"
  if [[ "${PAX_RELEASE_DRY_RUN:-0}" != "1" && "${PAX_RELEASE_ALLOW_DIRTY:-0}" != "1" ]]; then
    [[ "$actual_dirty" == "false" || -z "$actual_dirty" ]] ||
      fail "smoke test dirty state mismatch for ${platform}: ${actual_dirty}, expected false"
  fi
}

write_manifest() {
  local path="$1"
  local version="$2"
  local build_id="$3"
  local tags_json="$4"
  local created_at="$5"
  local artifacts_jsonl="$6"

  python3 - "$path" "$version" "$build_id" "$tags_json" "$created_at" "$artifacts_jsonl" <<'PY'
import json
import sys
from pathlib import Path

path, version, build_id, tags_json, created_at, artifacts_path = sys.argv[1:]
artifacts = []
if Path(artifacts_path).exists():
    for line in Path(artifacts_path).read_text(encoding="utf-8").splitlines():
        if line.strip():
            artifacts.append(json.loads(line))
manifest = {
    "product": "paxl",
    "version": version,
    "build_id": build_id,
    "tags": json.loads(tags_json),
    "created_at": created_at,
    "artifacts": artifacts,
}
Path(path).write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

append_artifact_metadata() {
  local path="$1"
  local platform="$2"
  local file="$3"
  local sha="$4"
  local size="$5"
  local storage_url="$6"
  local bucket="$7"
  local object="$8"
  local generation="$9"
  local content_type="${10}"

  python3 - "$path" "$platform" "$file" "$sha" "$size" "$storage_url" "$bucket" "$object" "$generation" "$content_type" <<'PY'
import json
import sys

path, platform, file_name, sha, size, storage_url, bucket, object_name, generation, content_type = sys.argv[1:]
record = {
    "platform": platform,
    "file": file_name,
    "sha256": sha,
    "size": int(size),
    "storage_url": storage_url,
    "bucket": bucket,
    "object": object_name,
    "generation": int(generation),
    "content_type": content_type,
}
with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps(record, separators=(",", ":"), sort_keys=True) + "\n")
PY
}

upload_file() {
  local src="$1"
  local dst="$2"
  local content_type="$3"

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" || "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    log "skipping upload ${src} -> ${dst}"
    return
  fi
  log "uploading ${src} -> ${dst}"
  gcloud storage cp --content-type="$content_type" "$src" "$dst" >/dev/null
}

verify_gcs_object() {
  local dst="$1"
  local expected_size="$2"
  local actual_size

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" ||
    "${PAX_RELEASE_SKIP_VERIFY:-0}" == "1" ||
    "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    return
  fi
  actual_size="$(gcloud storage objects describe "$dst" --format='value(size)')"
  [[ "$actual_size" == "$expected_size" ]] ||
    fail "GCS object size mismatch for ${dst}: ${actual_size}, expected ${expected_size}"
}

gcs_object_generation() {
  local dst="$1"

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" || "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    printf '0'
    return
  fi
  gcloud storage objects describe "$dst" --format='value(generation)'
}

json_field() {
  local path="$1"
  local source="${2:-stdin}"

  python3 -c '
import json
import sys

path = sys.argv[1].split(".")
source = sys.argv[2]
raw = sys.stdin.read()
try:
    doc = json.loads(raw)
except json.JSONDecodeError as exc:
    snippet = raw[:500].replace("\n", "\\n")
    raise SystemExit(f"non-JSON input while reading {'\''.'\''.join(path)} from {source}: {exc}; body={snippet!r}")
try:
    value = doc
    for part in path:
        value = value[part]
except (KeyError, TypeError) as exc:
    raise SystemExit(f"missing JSON field {'\''.'\''.join(path)} in {source}: {exc}")
print(value)
' "$path" "$source"
}

json_field_checked() {
  local path="$1"
  local source="$2"

  json_field "$path" "$source"
}

urlencode() {
  python3 - "$1" <<'PY'
import sys
import urllib.parse

print(urllib.parse.quote(sys.argv[1], safe=""))
PY
}

artifact_publish_token() {
  if [[ -n "${PAX_RELEASE_ID_TOKEN:-}" ]]; then
    printf '%s' "$PAX_RELEASE_ID_TOKEN"
    return
  fi
  gcloud auth print-identity-token
}

should_publish_metadata() {
  [[ "${PAX_RELEASE_SKIP_METADATA:-0}" != "1" &&
    "${PAX_RELEASE_SKIP_UPLOAD:-0}" != "1" &&
    "${PAX_RELEASE_DRY_RUN:-0}" != "1" ]]
}

publish_artifact_metadata() {
  local artifacts_jsonl="$1"
  local version="$2"
  local build_id="$3"
  local tags_json="$4"
  local manager_url="$5"
  local token line payload endpoint platform response code

  if ! should_publish_metadata; then
    log "skipping manager artifact metadata publish"
    return
  fi

  require_cmd curl
  token="$(artifact_publish_token)"
  endpoint="${manager_url%/}/api/v1/admin/artifacts"
  log "publishing artifact metadata -> ${endpoint}"

  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    platform="$(printf '%s' "$line" | json_field platform "release artifact metadata")"
    payload="$(python3 - "$line" "$version" "$build_id" "$tags_json" <<'PY'
import json
import sys

record = json.loads(sys.argv[1])
version = sys.argv[2]
build_id = sys.argv[3]
tags = json.loads(sys.argv[4])
payload = {
    "product": "paxl",
    "platform": record["platform"],
    "tags": tags,
    "version": version,
    "build_id": build_id,
    "bucket": record["bucket"],
    "object": record["object"],
    "generation": record["generation"],
    "sha256": record["sha256"],
    "size_bytes": record["size"],
    "content_type": record["content_type"],
}
if record["platform"] == "script":
    payload["tags"] = sorted(set(tags + ["installer"]))
print(json.dumps(payload, separators=(",", ":"), sort_keys=True))
PY
)"
    if ! response="$(curl -sS \
      -H "Authorization: Bearer ${token}" \
      -H "Content-Type: application/json" \
      -X POST \
      --data "$payload" \
      "$endpoint")"; then
      fail "artifact metadata publish request failed for ${platform}: ${endpoint}"
    fi
    code="$(printf '%s' "$response" | json_field code "artifact publish response for ${platform}")"
    [[ "$code" == "200" ]] ||
      fail "artifact metadata publish failed for ${platform}: code ${code}; response ${response}"
  done <"$artifacts_jsonl"
}

verify_resolver_artifacts() {
  local artifacts_jsonl="$1"
  local version="$2"
  local tags="$3"
  local manager_url="$4"
  local verify_tag line platform encoded_platform encoded_tag resolver_url response
  local actual_version actual_sha actual_size expected_sha expected_size

  if ! should_publish_metadata || [[ "${PAX_RELEASE_SKIP_VERIFY:-0}" == "1" ]]; then
    log "skipping public resolver verification"
    return
  fi

  require_cmd curl
  verify_tag="${tags%%,*}"
  encoded_tag="$(urlencode "$verify_tag")"
  log "verifying public resolver metadata"

  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    platform="$(printf '%s' "$line" | json_field platform "release artifact metadata")"
    expected_sha="$(printf '%s' "$line" | json_field sha256 "release artifact metadata for ${platform}")"
    expected_size="$(printf '%s' "$line" | json_field size "release artifact metadata for ${platform}")"
    encoded_platform="$(urlencode "$platform")"
    resolver_url="${manager_url%/}/api/v1/public/artifacts/download?product=paxl&platform=${encoded_platform}&tags=${encoded_tag}"
    if ! response="$(curl -sS "$resolver_url")"; then
      fail "resolver request failed for ${platform}: ${resolver_url}"
    fi
    actual_version="$(printf '%s' "$response" | json_field_checked data.version "$resolver_url")"
    actual_sha="$(printf '%s' "$response" | json_field_checked data.sha256 "$resolver_url")"
    actual_size="$(printf '%s' "$response" | json_field_checked data.size_bytes "$resolver_url")"
    [[ "$actual_version" == "$version" ]] ||
      fail "resolver version mismatch for ${platform}: ${actual_version}, expected ${version}"
    [[ "$actual_sha" == "$expected_sha" ]] ||
      fail "resolver sha256 mismatch for ${platform}"
    [[ "$actual_size" == "$expected_size" ]] ||
      fail "resolver size mismatch for ${platform}: ${actual_size}, expected ${expected_size}"
  done <"$artifacts_jsonl"
}

create_release_tag() {
  local version="$1"
  local tag="paxl/v${version}"

  if [[ "${PAX_RELEASE_SKIP_TAG:-0}" == "1" ||
    "${PAX_RELEASE_DRY_RUN:-0}" == "1" ||
    "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" ]]; then
    log "skipping git tag ${tag}"
    return
  fi
  git rev-parse -q --verify "refs/tags/${tag}" >/dev/null &&
    fail "release tag already exists: ${tag}"
  log "creating git tag ${tag}"
  git tag -a "$tag" -m "Release paxl ${version}"
  if [[ "${PAX_RELEASE_PUSH_TAG:-0}" == "1" ]]; then
    log "pushing git tag ${tag}"
    git push origin "$tag"
  fi
}

main() {
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    usage
    exit 0
  fi

  local version_arg="${1:-patch}"
  local version tags bucket prefix_parent platforms dist_dir build_id tags_json created_at
  local artifacts_jsonl manifest manifest_dst installer_object manager_url

  require_cmd go
  require_cmd git
  require_cmd python3
  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" != "1" && "${PAX_RELEASE_DRY_RUN:-0}" != "1" ]]; then
    require_cmd gcloud
  fi

  ensure_clean_tree

  version="$(resolve_version "$version_arg")"
  tags="${PAX_RELEASE_TAGS:-${2:-stable}}"
  bucket="${PAX_RELEASE_BUCKET:-pax-tech-bucket}"
  prefix_parent="${PAX_RELEASE_PREFIX:-paxl/releases}"
  platforms="${PAX_RELEASE_PLATFORMS:-darwin/amd64 darwin/arm64 linux/amd64 linux/arm64}"
  dist_dir="${PAX_RELEASE_DIST_DIR:-dist}"
  installer_object="${PAX_RELEASE_INSTALLER_OBJECT:-paxl/install.sh}"
  manager_url="${PAX_RELEASE_MANAGER_URL:-https://api.paxtech.net}"
  build_id="${PAX_RELEASE_BUILD_ID:-$(git rev-parse --short HEAD)}"
  tags_json="$(tag_json_array "$tags")"
  created_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  artifacts_jsonl="$(mktemp)"

  mkdir -p "$dist_dir"

  log "release version: ${version}"
  log "build id: ${build_id}"
  log "tags: ${tags}"
  log "bucket: gs://${bucket}/${prefix_parent}/${version}/"
  log "platforms: ${platforms}"

  local platform os arch name output sha size object dst content_type sha_file generation
  for platform in $platforms; do
    os="${platform%/*}"
    arch="${platform#*/}"
    name="$(artifact_name_for "$version" "$platform")"
    output="${dist_dir}/${name}"

    log "building ${platform} -> ${output}"
    GOCACHE="${GOCACHE:-/tmp/paxl-go-cache-release-${version//./-}}" \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath \
      -ldflags="-s -w -X main.version=${version} -X main.buildCommit=${build_id}" \
      -o "$output" ./cmd/paxl

    smoke_test_binary "$platform" "$output" "$version" "$build_id"

    sha="$(sha256_file "$output")"
    size="$(file_size "$output")"
    object="${prefix_parent}/${version}/${name}"
    dst="gs://${bucket}/${object}"
    content_type="$(content_type_for "$platform")"
    sha_file="${output}.sha256"

    printf '%s  %s\n' "$sha" "$name" >"$sha_file"
    upload_file "$output" "$dst" "$content_type"
    upload_file "$sha_file" "${dst}.sha256" "text/plain"
    verify_gcs_object "$dst" "$size"
    generation="$(gcs_object_generation "$dst")"
    append_artifact_metadata "$artifacts_jsonl" "$platform" "$name" "$sha" "$size" "$dst" "$bucket" "$object" "$generation" "$content_type"
  done

  manifest="${dist_dir}/paxl_${version}_manifest.json"
  manifest_dst="gs://${bucket}/${prefix_parent}/${version}/manifest.json"
  write_manifest "$manifest" "$version" "$build_id" "$tags_json" "$created_at" "$artifacts_jsonl"
  upload_file "$manifest" "$manifest_dst" "application/json"
  verify_gcs_object "$manifest_dst" "$(file_size "$manifest")"
  local release_tag latest_manifest_dst
  IFS=, read -r -a release_tags <<<"$tags"
  for release_tag in "${release_tags[@]}"; do
    [[ -n "$release_tag" ]] || continue
    latest_manifest_dst="gs://${bucket}/${prefix_parent}/latest/${release_tag}/manifest.json"
    upload_file "$manifest" "$latest_manifest_dst" "application/json"
    verify_gcs_object "$latest_manifest_dst" "$(file_size "$manifest")"
  done
  if [[ "${PAX_RELEASE_SKIP_INSTALLER:-0}" != "1" ]]; then
    upload_file "scripts/installer.sh" "gs://${bucket}/${installer_object}" "text/x-shellscript"
    verify_gcs_object "gs://${bucket}/${installer_object}" "$(file_size scripts/installer.sh)"
    generation="$(gcs_object_generation "gs://${bucket}/${installer_object}")"
    append_artifact_metadata \
      "$artifacts_jsonl" \
      "script" \
      "install.sh" \
      "$(sha256_file scripts/installer.sh)" \
      "$(file_size scripts/installer.sh)" \
      "gs://${bucket}/${installer_object}" \
      "$bucket" \
      "$installer_object" \
      "$generation" \
      "text/x-shellscript"
  else
    log "skipping installer upload"
  fi
  publish_artifact_metadata "$artifacts_jsonl" "$version" "$build_id" "$tags_json" "$manager_url"
  verify_resolver_artifacts "$artifacts_jsonl" "$version" "$tags" "$manager_url"
  rm -f "$artifacts_jsonl"
  create_release_tag "$version"

  log "release ${version} complete"
}

main "$@"

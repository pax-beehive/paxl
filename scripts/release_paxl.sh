#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_paxl.sh [patch|minor|major|<version>] [tag[,tag...]]

Build paxl for supported platforms, smoke-test the native binary, upload
artifacts to S3-compatible object storage, publish release metadata, and tag
the uploaded semantic version. S3 uploads are write-once. A retry accepts an
existing object only after its size, content type, and checksums match.

Defaults:
  version bump: patch
  tags: stable
  object prefix: paxl/releases/<version>/
  platforms: darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

Environment overrides:
  PAX_RELEASE_BUCKET      Required S3 bucket name, except for dry-run/build-only runs.
  PAX_RELEASE_PREFIX      S3 object prefix parent.
  PAX_RELEASE_S3_ENDPOINT Optional S3-compatible endpoint URL (for example MinIO).
  PAX_RELEASE_PUBLIC_BASE_URL Optional stable public base URL used in manifest storage_url fields.
  PAX_RELEASE_TAGS        Comma-separated tags. Overrides the second argument.
  PAX_RELEASE_PLATFORMS   Space-separated GOOS/GOARCH platforms.
  PAX_RELEASE_BUILD_ID    Build id stored in metadata. Defaults to git short SHA.
  PAX_RELEASE_DIST_DIR    Local output directory. Defaults to dist.
  PAX_RELEASE_INSTALLER_OBJECT S3 object for installer. Defaults to <prefix>/<version>/install.sh.
  PAX_RELEASE_MANAGER_URL Public manager API base URL, also baked into the installer as its default resolver. Defaults to https://api.paxworkspace.net.
  PAX_RELEASE_TOKEN       Required bearer token for artifact metadata publish.
  PAX_RELEASE_ID_TOKEN    Deprecated alias for PAX_RELEASE_TOKEN.
  PAX_CLOUD_CF_CLIENT_ID  Optional Cloudflare Access service-token client ID for admin metadata publish only.
  PAX_CLOUD_CF_CLIENT_SECRET Matching service-token secret; both CF values must be set together.
  AWS_REGION              AWS region used by the AWS CLI. Defaults to us-east-1.
  AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
                           Standard AWS credential environment variables.
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
  AWS_REGION=us-west-2 PAX_RELEASE_BUCKET=my-releases PAX_RELEASE_TOKEN=... scripts/release_paxl.sh 0.2.0 stable
  AWS_REGION=us-east-1 PAX_RELEASE_S3_ENDPOINT=http://127.0.0.1:9000 PAX_RELEASE_BUCKET=pax-releases PAX_RELEASE_TOKEN=... scripts/release_paxl.sh 0.2.0 stable
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

validate_cloudflare_access_credentials() {
  local client_id="${PAX_CLOUD_CF_CLIENT_ID:-}"
  local client_secret="${PAX_CLOUD_CF_CLIENT_SECRET:-}"

  if [[ -n "$client_id" && -z "$client_secret" ]] ||
    [[ -z "$client_id" && -n "$client_secret" ]]; then
    fail "PAX_CLOUD_CF_CLIENT_ID and PAX_CLOUD_CF_CLIENT_SECRET must be set together"
  fi
}

manager_admin_curl() {
  validate_cloudflare_access_credentials
  if [[ -n "${PAX_CLOUD_CF_CLIENT_ID:-}" ]]; then
    curl --disable \
      -H "CF-Access-Client-Id: ${PAX_CLOUD_CF_CLIENT_ID}" \
      -H "CF-Access-Client-Secret: ${PAX_CLOUD_CF_CLIENT_SECRET}" \
      "$@"
    return
  fi
  curl --disable "$@"
}

anonymous_manager_curl() {
  # Public release smoke tests must exercise the same anonymous contract as an
  # installer or client. In particular, never reuse Cloudflare service-token
  # headers that are valid only for the admin metadata endpoint.
  curl --disable --no-location --max-redirs 0 "$@"
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

normalize_public_manager_url() {
  local manager_url="$1"

  python3 - "$manager_url" <<'PY'
import sys
from urllib.parse import urlsplit

raw_url = sys.argv[1]
if not raw_url or any(character.isspace() for character in raw_url):
    raise SystemExit("manager URL must be a public HTTP(S) base URL without credentials, query, or fragment")
manager_url = raw_url.rstrip("/")
try:
    parsed = urlsplit(manager_url)
    _ = parsed.port
except ValueError:
    raise SystemExit("manager URL is not a valid public HTTP(S) base URL")
if (
    parsed.scheme.lower() not in {"http", "https"}
    or not parsed.hostname
    or parsed.username is not None
    or parsed.password is not None
    or parsed.query
    or parsed.fragment
):
    raise SystemExit("manager URL must be a public HTTP(S) base URL without credentials, query, or fragment")
sys.stdout.write(manager_url)
PY
}

bake_installer_manager_url() {
  local source_path="$1"
  local output_path="$2"
  local manager_url

  manager_url="$(normalize_public_manager_url "$3")" || return 1

  python3 - "$source_path" "$output_path" "$manager_url" <<'PY'
import os
import shlex
import stat
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
manager_url = sys.argv[3]

marker = 'PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-https://api.paxworkspace.net}"'
text = source.read_text(encoding="utf-8")
if text.count(marker) != 1:
    raise SystemExit("paxl installer download URL marker is missing or ambiguous")
replacement = "\n".join(
    (
        "pax_release_default_download_url=" + shlex.quote(manager_url),
        'PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-$pax_release_default_download_url}"',
        "unset pax_release_default_download_url",
    )
)
destination.write_text(text.replace(marker, replacement), encoding="utf-8")
os.chmod(destination, stat.S_IMODE(source.stat().st_mode))
PY
}

artifact_name_for() {
  local version="$1"
  local platform="$2"
  local os="${platform%/*}"
  local arch="${platform#*/}"

  printf 'paxl_%s_%s_%s' "$version" "$os" "$arch"
}

release_installer_object() {
  local prefix_parent="$1"
  local version="$2"

  printf '%s' "${PAX_RELEASE_INSTALLER_OBJECT:-${prefix_parent%/}/${version}/install.sh}"
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

aws_s3api() {
  local args=(--region "${AWS_REGION:-us-east-1}")

  if [[ -n "${PAX_RELEASE_S3_ENDPOINT:-}" ]]; then
    args+=(--endpoint-url "$PAX_RELEASE_S3_ENDPOINT")
  fi
  aws "${args[@]}" s3api "$@"
}

sha256_hex_to_base64() {
  python3 - "$1" <<'PY'
import base64
import sys

try:
    digest = bytes.fromhex(sys.argv[1])
except ValueError as exc:
    raise SystemExit(f"invalid hex sha256: {exc}")
if len(digest) != 32:
    raise SystemExit("invalid hex sha256 length")
sys.stdout.write(base64.b64encode(digest).decode("ascii"))
PY
}

describe_s3_object() {
  local bucket="$1"
  local object="$2"
  local description

  # AWS requires checksum mode to return ChecksumSHA256. Some S3-compatible
  # services do not implement that option, so retry without it and rely on the
  # independently verified sha256 metadata in that compatibility path.
  if description="$(aws_s3api head-object \
    --bucket "$bucket" \
    --key "$object" \
    --checksum-mode ENABLED \
    --output json 2>/dev/null)"; then
    printf '%s' "$description"
    return
  fi
  aws_s3api head-object --bucket "$bucket" --key "$object" --output json
}

upload_file() {
  local src="$1"
  local bucket="$2"
  local object="$3"
  local content_type="$4"
  local sha="$5"
  local checksum_sha256 put_output put_status

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" || "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    if [[ -n "$bucket" ]]; then
      log "skipping upload ${src} -> s3://${bucket}/${object}"
    else
      log "skipping upload ${src} (bucket not configured)"
    fi
    return
  fi
  log "uploading ${src} -> s3://${bucket}/${object}"
  checksum_sha256="$(sha256_hex_to_base64 "$sha")"
  if put_output="$(aws_s3api put-object \
    --bucket "$bucket" \
    --key "$object" \
    --body "$src" \
    --content-type "$content_type" \
    --metadata "sha256=${sha}" \
    --checksum-sha256 "$checksum_sha256" \
    --if-none-match '*' 2>&1)"; then
    return
  else
    put_status=$?
  fi

  # A successful write can still look failed when the client loses the
  # response. Never echo AWS stderr here because it may contain sensitive
  # endpoint context. Treat any failed PUT as idempotent only when HEAD proves
  # that the immutable object is exactly the artifact we intended to upload.
  put_output=""
  log "S3 PUT exited ${put_status}; verifying an existing immutable object"
  verify_s3_object \
    "$bucket" \
    "$object" \
    "$(file_size "$src")" \
    "$content_type" \
    "$sha" \
    "1"
  log "existing S3 object matches; continuing the release"
}

verify_s3_object() {
  local bucket="$1"
  local object="$2"
  local expected_size="$3"
  local expected_content_type="$4"
  local expected_sha="$5"
  local force="${6:-0}"
  local description actual_size actual_content_type actual_sha actual_checksum expected_checksum

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" == "1" ||
    "${PAX_RELEASE_DRY_RUN:-0}" == "1" ]]; then
    return
  fi
  if [[ "$force" != "1" && "${PAX_RELEASE_SKIP_VERIFY:-0}" == "1" ]]; then
    return
  fi
  description="$(describe_s3_object "$bucket" "$object")"
  read -r actual_size actual_content_type actual_sha actual_checksum < <(printf '%s' "$description" | python3 -c '
import json
import sys

doc = json.load(sys.stdin)
metadata = doc.get("Metadata") or {}
print(
    doc.get("ContentLength", ""),
    doc.get("ContentType", ""),
    metadata.get("sha256", ""),
    doc.get("ChecksumSHA256", ""),
)
')
  [[ "$actual_size" == "$expected_size" ]] ||
    fail "S3 object size mismatch for s3://${bucket}/${object}: ${actual_size}, expected ${expected_size}"
  [[ "$actual_content_type" == "$expected_content_type" ]] ||
    fail "S3 object content type mismatch for s3://${bucket}/${object}: ${actual_content_type}, expected ${expected_content_type}"
  [[ "$actual_sha" == "$expected_sha" ]] ||
    fail "S3 object sha256 metadata mismatch for s3://${bucket}/${object}"
  if [[ -n "$actual_checksum" ]]; then
    expected_checksum="$(sha256_hex_to_base64 "$expected_sha")"
    [[ "$actual_checksum" == "$expected_checksum" ]] ||
      fail "S3 object checksum mismatch for s3://${bucket}/${object}"
  fi
}

json_field() {
  local path="$1"
  : "${2:-stdin}"

  python3 -c '
import json
import sys

path = sys.argv[1].split(".")
raw = sys.stdin.read()
try:
    doc = json.loads(raw)
except json.JSONDecodeError:
    raise SystemExit(f"invalid JSON response while reading field {'\''.'\''.join(path)}")
try:
    value = doc
    for part in path:
        value = value[part]
except (KeyError, TypeError):
    raise SystemExit(f"missing JSON field {'\''.'\''.join(path)} in response")
print(value)
' "$path"
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

same_url_target() {
  local first="$1"
  local second="$2"

  printf '%s\0%s\0' "$first" "$second" | python3 -c '
import sys
from urllib.parse import urlsplit

def target(raw):
    try:
        parsed = urlsplit(raw)
        _ = parsed.port
    except ValueError:
        return None
    if parsed.scheme.lower() not in {"http", "https"} or not parsed.hostname:
        return None
    if parsed.username is not None or parsed.password is not None:
        return None
    return (parsed.scheme.lower(), parsed.netloc.lower(), parsed.path or "/")

values = sys.stdin.buffer.read().split(b"\0")
if len(values) != 3 or values[-1] != b"":
    raise SystemExit(1)
try:
    left = target(values[0].decode("utf-8"))
    right = target(values[1].decode("utf-8"))
except UnicodeDecodeError:
    raise SystemExit(1)
raise SystemExit(0 if left is not None and left == right else 1)
'
}

artifact_publish_token() {
  if [[ -n "${PAX_RELEASE_TOKEN:-}" ]]; then
    printf '%s' "$PAX_RELEASE_TOKEN"
    return
  fi
  if [[ -n "${PAX_RELEASE_ID_TOKEN:-}" ]]; then
    printf '%s' "$PAX_RELEASE_ID_TOKEN"
    return
  fi
  fail "PAX_RELEASE_TOKEN is required to publish artifact metadata"
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
    if ! response="$(manager_admin_curl -sS \
      -H "Authorization: Bearer ${token}" \
      -H "Content-Type: application/json" \
      -X POST \
      --data "$payload" \
      "$endpoint")"; then
      fail "artifact metadata publish request failed for ${platform}: ${endpoint}"
    fi
    code="$(printf '%s' "$response" | json_field code "artifact publish response for ${platform}")"
    [[ "$code" == "200" ]] ||
      fail "artifact metadata publish failed for ${platform}"
  done <"$artifacts_jsonl"
}

verify_resolver_artifacts() {
  local artifacts_jsonl="$1"
  local version="$2"
  local tags="$3"
  local manager_url="$4"
  local verify_tag line platform encoded_platform encoded_tag resolver_url response
  local response_file response_status actual_version actual_sha actual_size expected_sha expected_size

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
    response_file="$(mktemp)"
    if ! response_status="$(anonymous_manager_curl \
      -sS \
      -o "$response_file" \
      -w '%{http_code}' \
      "$resolver_url")"; then
      rm -f "$response_file"
      fail "resolver request failed for ${platform}"
    fi
    if [[ "$response_status" != "200" ]]; then
      rm -f "$response_file"
      fail "resolver did not return HTTP 200 for ${platform}"
    fi
    response="$(<"$response_file")"
    rm -f "$response_file"
    actual_version="$(printf '%s' "$response" | json_field_checked data.version "$resolver_url")"
    actual_sha="$(printf '%s' "$response" | json_field_checked data.sha256 "$resolver_url")"
    actual_size="$(printf '%s' "$response" | json_field_checked data.size_bytes "$resolver_url")"
    [[ "$actual_version" == "$version" ]] ||
      fail "resolver version mismatch for ${platform}"
    [[ "$actual_sha" == "$expected_sha" ]] ||
      fail "resolver sha256 mismatch for ${platform}"
    [[ "$actual_size" == "$expected_size" ]] ||
      fail "resolver size mismatch for ${platform}"
  done <"$artifacts_jsonl"
}

verify_public_installer_redirect() {
  local manager_url="$1"
  local resolver_url resolver_response resolver_signed_url installer_url
  local resolver_body_file resolver_status headers_file redirect_status redirect_location

  if ! should_publish_metadata ||
    [[ "${PAX_RELEASE_SKIP_VERIFY:-0}" == "1" ]] ||
    [[ "${PAX_RELEASE_SKIP_INSTALLER:-0}" == "1" ]]; then
    log "skipping public installer redirect verification"
    return
  fi

  require_cmd curl
  resolver_url="${manager_url%/}/api/v1/public/artifacts/download?product=paxl&platform=script&tags=$(urlencode 'stable,installer')"
  resolver_body_file="$(mktemp)"
  if ! resolver_status="$(anonymous_manager_curl \
    -sS \
    --no-location \
    --max-redirs 0 \
    -o "$resolver_body_file" \
    -w '%{http_code}' \
    "$resolver_url")"; then
    rm -f "$resolver_body_file"
    fail "public installer resolver request failed"
  fi
  if [[ "$resolver_status" != "200" ]]; then
    rm -f "$resolver_body_file"
    fail "public installer resolver did not return HTTP 200"
  fi
  resolver_response="$(<"$resolver_body_file")"
  rm -f "$resolver_body_file"
  resolver_signed_url="$(printf '%s' "$resolver_response" |
    json_field_checked data.url "public installer resolver response")"

  installer_url="${manager_url%/}/api/v1/public/paxl/install.sh"
  headers_file="$(mktemp)"
  if ! redirect_status="$(anonymous_manager_curl \
    -sS \
    --no-location \
    --max-redirs 0 \
    -D "$headers_file" \
    -o /dev/null \
    -w '%{http_code}' \
    "$installer_url")"; then
    rm -f "$headers_file"
    fail "public installer request failed"
  fi
  redirect_location="$(awk '
    tolower($0) ~ /^location:[[:space:]]*/ {
      sub(/^[^:]*:[[:space:]]*/, "")
      sub(/\r$/, "")
      print
      exit
    }
  ' "$headers_file")"
  rm -f "$headers_file"

  [[ "$redirect_status" == "302" ]] ||
    fail "public installer endpoint did not return HTTP 302"
  [[ -n "$redirect_location" ]] ||
    fail "public installer endpoint returned HTTP 302 without Location"
  same_url_target "$resolver_signed_url" "$redirect_location" ||
    fail "public installer redirect does not match the stable installer resolver target"
  log "public installer redirect verified"
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
  local artifacts_jsonl manifest manifest_dst installer_object manager_url public_base_url

  require_cmd python3
  manager_url="${PAX_RELEASE_MANAGER_URL:-https://api.paxworkspace.net}"
  manager_url="$(normalize_public_manager_url "$manager_url")" || return 1
  require_cmd go
  require_cmd git
  validate_cloudflare_access_credentials
  ensure_clean_tree

  version="$(resolve_version "$version_arg")"
  tags="${PAX_RELEASE_TAGS:-${2:-stable}}"
  bucket="${PAX_RELEASE_BUCKET:-}"
  prefix_parent="${PAX_RELEASE_PREFIX:-paxl/releases}"
  platforms="${PAX_RELEASE_PLATFORMS:-darwin/amd64 darwin/arm64 linux/amd64 linux/arm64}"
  dist_dir="${PAX_RELEASE_DIST_DIR:-dist}"
  installer_object="$(release_installer_object "$prefix_parent" "$version")"
  public_base_url="${PAX_RELEASE_PUBLIC_BASE_URL:-}"
  build_id="${PAX_RELEASE_BUILD_ID:-$(git rev-parse --short HEAD)}"
  tags_json="$(tag_json_array "$tags")"
  created_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  artifacts_jsonl="$(mktemp)"

  if [[ "${PAX_RELEASE_SKIP_UPLOAD:-0}" != "1" && "${PAX_RELEASE_DRY_RUN:-0}" != "1" ]]; then
    [[ -n "$bucket" ]] || fail "PAX_RELEASE_BUCKET is required for upload"
    require_cmd aws
  fi

  mkdir -p "$dist_dir"

  log "release version: ${version}"
  log "build id: ${build_id}"
  log "tags: ${tags}"
  if [[ -n "$bucket" ]]; then
    log "bucket: s3://${bucket}/${prefix_parent}/${version}/"
  else
    log "bucket: not configured (dry-run/build-only)"
  fi
  if [[ -n "${PAX_RELEASE_S3_ENDPOINT:-}" ]]; then
    log "S3 endpoint: ${PAX_RELEASE_S3_ENDPOINT}"
  fi
  log "platforms: ${platforms}"

  local platform os arch name output sha size object storage_url content_type sha_file sha_file_sha generation
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
    storage_url=""
    if [[ -n "$public_base_url" ]]; then
      storage_url="${public_base_url%/}/${object}"
    fi
    content_type="$(content_type_for "$platform")"
    sha_file="${output}.sha256"

    printf '%s  %s\n' "$sha" "$name" >"$sha_file"
    sha_file_sha="$(sha256_file "$sha_file")"
    upload_file "$output" "$bucket" "$object" "$content_type" "$sha"
    upload_file "$sha_file" "$bucket" "${object}.sha256" "text/plain" "$sha_file_sha"
    verify_s3_object "$bucket" "$object" "$size" "$content_type" "$sha"
    verify_s3_object "$bucket" "${object}.sha256" "$(file_size "$sha_file")" "text/plain" "$sha_file_sha"
    generation="0"
    append_artifact_metadata "$artifacts_jsonl" "$platform" "$name" "$sha" "$size" "$storage_url" "$bucket" "$object" "$generation" "$content_type"
  done

  manifest="${dist_dir}/paxl_${version}_manifest.json"
  manifest_dst="${prefix_parent}/${version}/manifest.json"
  write_manifest "$manifest" "$version" "$build_id" "$tags_json" "$created_at" "$artifacts_jsonl"
  local manifest_sha
  manifest_sha="$(sha256_file "$manifest")"
  upload_file "$manifest" "$bucket" "$manifest_dst" "application/json" "$manifest_sha"
  verify_s3_object "$bucket" "$manifest_dst" "$(file_size "$manifest")" "application/json" "$manifest_sha"
  if [[ "${PAX_RELEASE_SKIP_INSTALLER:-0}" != "1" ]]; then
    local installer_path installer_sha installer_size
    installer_path="${dist_dir}/paxl_${version}_install.sh"
    bake_installer_manager_url "scripts/installer.sh" "$installer_path" "$manager_url"
    installer_sha="$(sha256_file "$installer_path")"
    installer_size="$(file_size "$installer_path")"
    upload_file "$installer_path" "$bucket" "$installer_object" "text/x-shellscript" "$installer_sha"
    verify_s3_object "$bucket" "$installer_object" "$installer_size" "text/x-shellscript" "$installer_sha"
    generation="0"
    append_artifact_metadata \
      "$artifacts_jsonl" \
      "script" \
      "install.sh" \
      "$installer_sha" \
      "$installer_size" \
      "${public_base_url:+${public_base_url%/}/${installer_object}}" \
      "$bucket" \
      "$installer_object" \
      "$generation" \
      "text/x-shellscript"
  else
    log "skipping installer upload"
  fi
  publish_artifact_metadata "$artifacts_jsonl" "$version" "$build_id" "$tags_json" "$manager_url"
  verify_resolver_artifacts "$artifacts_jsonl" "$version" "$tags" "$manager_url"
  verify_public_installer_redirect "$manager_url"
  rm -f "$artifacts_jsonl"
  create_release_tag "$version"

  log "release ${version} complete"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi

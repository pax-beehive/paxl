#!/usr/bin/env bash
set -euo pipefail

PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-https://api.paxtech.net}"
PAXL_RESOLVER_PATH="${PAXL_RESOLVER_PATH:-/api/v1/public/artifacts/download}"
PAXL_TAG="${PAXL_TAG:-stable}"
PAXL_VERSION="${PAXL_VERSION:-}"
PAXL_BUCKET="${PAXL_BUCKET:-pax-tech-bucket}"
PAXL_PREFIX="${PAXL_PREFIX:-paxl/releases}"
PAXL_MANIFEST_URL="${PAXL_MANIFEST_URL:-}"
PAXL_USE_RESOLVER="${PAXL_USE_RESOLVER:-0}"
PAXL_BINARY_NAME="${PAXL_BINARY_NAME:-paxl}"
PAXL_INSTALL_DIR="${PAXL_INSTALL_DIR:-}"
paxl_installer_tmpdir=""

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  bold="$(tput bold)"
  reset="$(tput sgr0)"
  red="$(tput setaf 1)"
  green="$(tput setaf 2)"
  yellow="$(tput setaf 3)"
  magenta="$(tput setaf 5)"
  cyan="$(tput setaf 6)"
else
  bold=""
  reset=""
  red=""
  green=""
  yellow=""
  magenta=""
  cyan=""
fi

log() {
  printf '%s\n' "${cyan}==>${reset} $*"
}

warn() {
  printf '%s\n' "${yellow}warning:${reset} $*" >&2
}

fail() {
  printf '%s\n' "${red}error:${reset} $*" >&2
  exit 1
}

print_banner() {
  local width=52
  banner_line() {
    local color="$1"
    local text="$2"
    printf '%b|%b %b%-*s%b %b|%b\n' \
      "${cyan}${bold}" "${reset}" "$color" $((width - 4)) "$text" "${reset}" "${cyan}${bold}" "${reset}"
  }

  printf '%b\n' "${cyan}${bold}+--------------------------------------------------+${reset}"
  banner_line "${magenta}${bold}" "    ____  ___   _  __"
  banner_line "${magenta}${bold}" "   / __ \\/   | | |/ /"
  banner_line "${magenta}${bold}" "  / /_/ / /| | |   /"
  banner_line "${yellow}${bold}" " / ____/ ___ |/   |"
  banner_line "${yellow}${bold}" "/_/   /_/  |_/_/|_|"
  banner_line "${green}${bold}" "                 installer for agent context"
  printf '%b\n' "${cyan}${bold}+--------------------------------------------------+${reset}"
  printf '\n'
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m | tr '[:upper:]' '[:lower:]')"

  case "$os" in
    darwin) os="darwin" ;;
    linux) os="linux" ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac

  case "$arch" in
    arm64 | aarch64) arch="arm64" ;;
    x86_64 | amd64) arch="amd64" ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac

  printf '%s/%s' "$os" "$arch"
}

urlencode() {
  python3 - "$1" <<'PY'
import sys
import urllib.parse

print(urllib.parse.quote(sys.argv[1], safe=""))
PY
}

json_field() {
  python3 -c 'import json, sys
path = sys.argv[1].split(".")
doc = json.load(sys.stdin)
value = doc
for part in path:
    value = value[part]
print(value)' "$1"
}

manifest_field() {
  python3 -c 'import json
import sys

platform, field = sys.argv[1], sys.argv[2]
doc = json.load(sys.stdin)
for artifact in doc.get("artifacts", []):
    if artifact.get("platform") == platform:
        print(artifact[field])
        raise SystemExit(0)
raise SystemExit(f"missing artifact for {platform}")
' "$1" "$2"
}

manifest_version() {
  python3 -c 'import json, sys; print(json.load(sys.stdin)["version"])'
}

gs_to_https() {
  local url="$1"
  local rest bucket object

  if [[ "$url" != gs://* ]]; then
    printf '%s' "$url"
    return
  fi
  rest="${url#gs://}"
  bucket="${rest%%/*}"
  object="${rest#*/}"
  printf 'https://storage.googleapis.com/%s/%s' "$bucket" "$object"
}

path_has_dir() {
  [[ ":${PATH:-}:" == *":$1:"* ]]
}

choose_install_dir() {
  if [[ -n "$PAXL_INSTALL_DIR" ]]; then
    printf '%s' "$PAXL_INSTALL_DIR"
    return
  fi

  if [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
    printf '%s' /usr/local/bin
    return
  fi

  local dir
  IFS=':' read -r -a path_dirs <<<"${PATH:-}"
  for dir in "${path_dirs[@]}"; do
    if [[ -n "$dir" && -d "$dir" && -w "$dir" ]]; then
      printf '%s' "$dir"
      return
    fi
  done

  printf '%s' "$HOME/.local/bin"
}

download_with_progress() {
  local url="$1"
  local output="$2"

  if curl --help all 2>/dev/null | grep -q -- '--progress-bar'; then
    curl -fL --progress-bar -o "$output" "$url"
  else
    curl -fL -o "$output" "$url"
  fi
}

checksum_file() {
  local path="$1"

  if command -v shasum >/dev/null 2>&1; then
    LC_ALL=C LANG=C shasum -a 256 "$path" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    fail "missing shasum or sha256sum for checksum verification"
  fi
}

resolve_from_manifest() {
  local platform="$1"
  local manifest_url="$2"
  local response storage_url

  log "Resolving paxl artifact from manifest"
  response="$(curl -fsSL "$manifest_url")" || fail "failed to fetch manifest from $manifest_url"
  storage_url="$(printf '%s' "$response" | manifest_field "$platform" storage_url)"
  paxl_resolved_url="$(gs_to_https "$storage_url")"
  paxl_resolved_sha="$(printf '%s' "$response" | manifest_field "$platform" sha256)"
  paxl_resolved_size="$(printf '%s' "$response" | manifest_field "$platform" size)"
  paxl_resolved_version="$(printf '%s' "$response" | manifest_version)"
}

resolve_from_api() {
  local platform="$1"
  local encoded_platform api response

  encoded_platform="$(urlencode "$platform")"
  api="${PAXL_DOWNLOAD_URL%/}${PAXL_RESOLVER_PATH}?product=paxl&platform=${encoded_platform}&tags=${PAXL_TAG}"

  log "Resolving latest ${bold}${PAXL_TAG}${reset} paxl artifact"
  response="$(curl -fsSL "$api")" || fail "failed to resolve paxl artifact from $api"
  if [[ "$response" != \{* ]]; then
    fail "expected JSON from $api; got a non-JSON response"
  fi

  paxl_resolved_url="$(printf '%s' "$response" | json_field data.url)"
  paxl_resolved_sha="$(printf '%s' "$response" | json_field data.sha256)"
  paxl_resolved_version="$(printf '%s' "$response" | json_field data.version)"
  paxl_resolved_size="$(printf '%s' "$response" | json_field data.size_bytes 2>/dev/null ||
    printf '%s' "$response" | json_field data.size)"
}

main() {
  print_banner
  require_cmd curl
  require_cmd python3

  local platform manifest_url tmpdir binary_path got_sha install_dir target
  platform="$(detect_platform)"
  log "Detected platform: ${bold}${platform}${reset}"

  manifest_url="$PAXL_MANIFEST_URL"
  if [[ -z "$manifest_url" ]]; then
    if [[ -n "$PAXL_VERSION" ]]; then
      manifest_url="https://storage.googleapis.com/${PAXL_BUCKET}/${PAXL_PREFIX}/${PAXL_VERSION}/manifest.json"
    elif [[ "$PAXL_USE_RESOLVER" != "1" ]]; then
      manifest_url="https://storage.googleapis.com/${PAXL_BUCKET}/${PAXL_PREFIX}/latest/${PAXL_TAG}/manifest.json"
    fi
  fi

  if [[ -n "$manifest_url" ]]; then
    resolve_from_manifest "$platform" "$manifest_url"
  else
    resolve_from_api "$platform"
  fi

  tmpdir="$(mktemp -d)"
  paxl_installer_tmpdir="$tmpdir"
  trap 'rm -rf "${paxl_installer_tmpdir:-}"' EXIT
  binary_path="$tmpdir/$PAXL_BINARY_NAME"

  log "Downloading paxl ${bold}${paxl_resolved_version}${reset} (${paxl_resolved_size} bytes)"
  download_with_progress "$paxl_resolved_url" "$binary_path"

  got_sha="$(checksum_file "$binary_path")"
  [[ "$got_sha" == "$paxl_resolved_sha" ]] ||
    fail "sha256 mismatch: got $got_sha expected $paxl_resolved_sha"
  chmod 0755 "$binary_path"

  install_dir="$(choose_install_dir)"
  mkdir -p "$install_dir"
  target="$install_dir/$PAXL_BINARY_NAME"
  log "Installing paxl to ${bold}${target}${reset}"
  if ! cp "$binary_path" "$target" 2>/dev/null; then
    if command -v sudo >/dev/null 2>&1; then
      sudo cp "$binary_path" "$target"
      sudo chmod 0755 "$target"
    else
      fail "cannot write to $install_dir and sudo is unavailable"
    fi
  fi
  chmod 0755 "$target" 2>/dev/null || true

  if ! path_has_dir "$install_dir"; then
    warn "$install_dir is not currently in PATH"
    warn "add it to your shell profile, or run paxl via: $target"
  fi

  log "Installed: $("${target}" version | head -n 1)"
  printf '%s\n' "${green}Done.${reset}"
}

main "$@"

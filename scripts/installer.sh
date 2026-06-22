#!/usr/bin/env bash
set -euo pipefail

PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-https://api.paxtech.net}"
PAXL_TAG="${PAXL_TAG:-stable}"
PAXL_BINARY_NAME="${PAXL_BINARY_NAME:-}"
PAXL_INSTALL_DIR="${PAXL_INSTALL_DIR:-}"
paxl_installer_tmpdir=""

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  bold="$(tput bold)"
  reset="$(tput sgr0)"
  red="$(tput setaf 1)"
  green="$(tput setaf 2)"
  yellow="$(tput setaf 3)"
  cyan="$(tput setaf 6)"
else
  bold=""
  reset=""
  red=""
  green=""
  yellow=""
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
  printf '%b\n' "${cyan}${bold}paxl installer${reset}"
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
    msys*|mingw*|cygwin*) os="windows" ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac

  case "$arch" in
    arm64|aarch64) arch="arm64" ;;
    x86_64|amd64) arch="amd64" ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac

  printf '%s/%s' "$os" "$arch"
}

urlencode_platform() {
  local value="$1"
  printf '%s' "${value/\//%2F}"
}

json_string_field() {
  local key="$1"
  LC_ALL=C sed -nE 's/.*"'"$key"'"[[:space:]]*:[[:space:]]*"([^"]*)".*/\1/p' |
    LC_ALL=C sed 's/\\u0026/\&/g; s/\\\//\//g'
}

json_number_field() {
  local key="$1"
  LC_ALL=C sed -nE 's/.*"'"$key"'"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p'
}

path_has_dir() {
  [[ ":${PATH:-}:" == *":$1:"* ]]
}

choose_install_dir() {
  if [[ -n "$PAXL_INSTALL_DIR" ]]; then
    printf '%s' "$PAXL_INSTALL_DIR"
    return
  fi

  if path_has_dir /usr/local/bin; then
    printf '%s' /usr/local/bin
    return
  fi

  local dir
  IFS=':' read -r -a path_dirs <<< "${PATH:-}"
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

main() {
  print_banner
  require_cmd curl

  local platform binary_name encoded_platform api response tmpdir binary_path
  local download_url sha256 size version install_dir target got_sha
  platform="$(detect_platform)"
  binary_name="$PAXL_BINARY_NAME"
  if [[ -z "$binary_name" ]]; then
    binary_name="paxl"
    if [[ "$platform" == windows/* ]]; then
      binary_name="paxl.exe"
    fi
  fi

  encoded_platform="$(urlencode_platform "$platform")"
  api="${PAXL_DOWNLOAD_URL%/}/api/v1/public/paxl/download?platform=${encoded_platform}&tags=${PAXL_TAG}"

  log "Detected platform: ${bold}${platform}${reset}"
  log "Resolving latest ${bold}${PAXL_TAG}${reset} paxl artifact"
  response="$(curl -fsSL "$api")" || fail "failed to resolve paxl artifact from $api"
  if [[ "$response" != \{* ]]; then
    fail "expected JSON from $api; got a non-JSON response"
  fi

  download_url="$(printf '%s' "$response" | json_string_field url)"
  sha256="$(printf '%s' "$response" | json_string_field sha256)"
  size="$(printf '%s' "$response" | json_number_field size_bytes)"
  version="$(printf '%s' "$response" | json_string_field version)"
  if [[ -z "$download_url" || -z "$sha256" || -z "$size" || -z "$version" ]]; then
    fail "failed to parse artifact response from $api"
  fi

  tmpdir="$(mktemp -d)"
  paxl_installer_tmpdir="$tmpdir"
  trap 'rm -rf "${paxl_installer_tmpdir:-}"' EXIT
  binary_path="$tmpdir/$binary_name"

  log "Downloading paxl ${bold}${version}${reset} (${size} bytes)"
  download_with_progress "$download_url" "$binary_path"

  got_sha="$(checksum_file "$binary_path")"
  [[ "$got_sha" == "$sha256" ]] || fail "sha256 mismatch: got $got_sha expected $sha256"
  chmod 0755 "$binary_path"

  install_dir="$(choose_install_dir)"
  mkdir -p "$install_dir"
  target="$install_dir/$binary_name"
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

  log "Installed: $("${target}" version)"
}

main "$@"

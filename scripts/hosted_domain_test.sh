#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
hosted_api="https://api.paxworkspace.net"
legacy_api="https://api.paxtech"'.net'

fail_test() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local path="$1"
  local expected="$2"

  grep -Fq "$expected" "$path" ||
    fail_test "${path#"${repo_root}/"} does not reference ${expected}"
}

migrated_files=(
  "${repo_root}/scripts/installer.sh"
  "${repo_root}/scripts/release_paxl.sh"
  "${repo_root}/README.md"
  "${repo_root}/doc/README_cn.md"
  "${repo_root}/skills/knowledge-transfer/SKILL.md"
  "${repo_root}/plan.md"
)

for path in "${migrated_files[@]}"; do
  assert_contains "$path" "$hosted_api"
  if grep -Fq "$legacy_api" "$path"; then
    fail_test "${path#"${repo_root}/"} retains the retired hosted API"
  fi
done

assert_contains \
  "${repo_root}/scripts/installer.sh" \
  'PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-https://api.paxworkspace.net}"'
assert_contains \
  "${repo_root}/scripts/release_paxl.sh" \
  'marker = '\''PAXL_DOWNLOAD_URL="${PAXL_DOWNLOAD_URL:-https://api.paxworkspace.net}"'\'''

printf 'ok - hosted defaults and installation docs use api.paxworkspace.net\n'

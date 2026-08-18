#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=installer.sh
source "${script_dir}/installer.sh"

fail_test() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
curl_call_file="${test_dir}/curl-args"
curl_mode="resolver"
signed_secret="installer-signed-secret"

curl() {
  if [[ "${1:-}" == "--help" ]]; then
    printf '%s\n' '--progress-bar'
    return
  fi
  printf '%s\n' "$@" >"$curl_call_file"
  if [[ "$curl_mode" == "fail" ]]; then
    printf 'curl failed for https://objects.test/paxl?X-Amz-Signature=%s\n' "$signed_secret" >&2
    return 22
  fi
  if [[ "$curl_mode" == "resolver" ]]; then
    printf '{"data":{"url":"https://objects.test/paxl?X-Amz-Signature=%s","sha256":"abc123","size_bytes":4,"version":"0.2.0"}}' \
      "$signed_secret"
    return
  fi
  local previous="" arg output=""
  for arg in "$@"; do
    if [[ "$previous" == "-o" ]]; then
      output="$arg"
      break
    fi
    previous="$arg"
  done
  [[ -n "$output" ]] || return 2
  printf 'paxl' >"$output"
}

assert_zero_redirects() {
  local args_file="$1"
  awk '
    previous == "--max-redirs" && $0 == "0" { found_limit = 1 }
    $0 ~ /^-[A-Za-z]*L[A-Za-z]*$/ { found_follow = 1 }
    { previous = $0 }
    END { exit(found_limit && found_follow ? 0 : 1) }
  ' "$args_file" || fail_test "curl was not constrained to zero redirects"
}

PAXL_DOWNLOAD_URL="https://manager.test"
PAXL_RESOLVER_PATH="/api/v1/public/artifacts/download"
PAXL_VERSION=""
PAXL_TAG="stable"
resolve_from_api "linux/amd64" >/dev/null
assert_zero_redirects "$curl_call_file"
printf 'ok - manager resolver curl follows zero redirects\n'

curl_mode="download"
download_path="${test_dir}/paxl"
download_with_progress \
  "https://objects.test/paxl?X-Amz-Signature=${signed_secret}" \
  "$download_path" >/dev/null
assert_zero_redirects "$curl_call_file"
[[ "$(sed -n '1p' "$download_path")" == "paxl" ]] ||
  fail_test "binary download did not write the artifact"
printf 'ok - signed object curl follows zero redirects\n'

curl_mode="fail"
resolver_error="${test_dir}/resolver-error"
if (resolve_from_api "linux/amd64") >"$resolver_error" 2>&1; then
  fail_test "resolver transport failure unexpectedly succeeded"
fi
if grep -q "$signed_secret" "$resolver_error"; then
  fail_test "resolver transport error leaked a signed URL"
fi
printf 'ok - resolver transport errors redact signed URLs\n'

download_error="${test_dir}/download-error"
if (download_with_progress \
  "https://objects.test/paxl?X-Amz-Signature=${signed_secret}" \
  "$download_path") >"$download_error" 2>&1; then
  fail_test "binary transport failure unexpectedly succeeded"
fi
if grep -q "$signed_secret" "$download_error"; then
  fail_test "binary transport error leaked a signed URL"
fi
printf 'ok - binary transport errors redact signed URLs\n'

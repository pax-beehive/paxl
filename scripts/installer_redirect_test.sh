#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=installer.sh
source "${script_dir}/installer.sh"

fail_test() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

stdin_marker="stdin-main-ran"
if ! stdin_output="$(
  awk '
    index($0, "if [[") == 1 && index($0, "BASH_SOURCE[0]") > 0 {
      print "main() { printf \"%s\\n\" \"stdin-main-ran\"; }"
    }
    { print }
  ' "${script_dir}/installer.sh" | bash 2>&1
)"; then
  fail_test "installer failed when executed from standard input: ${stdin_output}"
fi
if [[ "$stdin_output" != "$stdin_marker" ]]; then
  fail_test "installer did not run main when executed from standard input"
fi
printf 'ok - installer runs main when executed from standard input\n'

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

default_home="${test_dir}/home"
writable_path_dir="${test_dir}/writable-path-bin"
mkdir -p "$default_home" "$writable_path_dir"
default_install_dir="$(
  HOME="$default_home" \
    PATH="${writable_path_dir}:/usr/bin:/bin" \
    PAXL_INSTALL_DIR='' \
    choose_install_dir
)"
if [[ "$default_install_dir" != "${default_home}/.local/bin" ]]; then
  fail_test "default install dir was ${default_install_dir}, expected ${default_home}/.local/bin"
fi
printf 'ok - default install dir is HOME/.local/bin even when PATH has a writable directory\n'

explicit_install_dir="${test_dir}/explicit-bin"
actual_explicit_install_dir="$(
  HOME="$default_home" \
    PAXL_INSTALL_DIR="$explicit_install_dir" \
    choose_install_dir
)"
if [[ "$actual_explicit_install_dir" != "$explicit_install_dir" ]]; then
  fail_test "PAXL_INSTALL_DIR did not override the default install directory"
fi
printf 'ok - explicit install dir overrides HOME/.local/bin\n'

run_fake_install() (
  local fake_home="$1"
  local fake_path="$2"
  local fake_shell="$3"

  HOME="$fake_home"
  PATH="$fake_path"
  SHELL="$fake_shell"
  PAXL_INSTALL_DIR="${4:-}"
  PAXL_MANIFEST_URL=''
  PAXL_USE_RESOLVER='1'

  print_banner() {
    :
  }
  require_cmd() {
    :
  }
  detect_platform() {
    printf '%s' 'linux/amd64'
  }
  resolve_from_api() {
    paxl_resolved_url='https://objects.test/paxl'
    paxl_resolved_sha='test-sha'
    paxl_resolved_size='42'
    paxl_resolved_version='9.9.9'
  }
  download_with_progress() {
    local output="$2"
    printf '%s\n' \
      '#!/usr/bin/env bash' \
      'printf '\''paxl 9.9.9\\n'\''' >"$output"
  }
  checksum_file() {
    printf '%s' 'test-sha'
  }

  main
)

missing_path_home="${test_dir}/missing-path-home"
missing_path_output="${test_dir}/missing-path-output"
if ! run_fake_install \
  "$missing_path_home" \
  '/usr/bin:/bin' \
  '/bin/zsh' >"$missing_path_output" 2>&1; then
  fail_test "install failed when HOME/.local/bin was not in PATH"
fi
if [[ ! -x "${missing_path_home}/.local/bin/paxl" ]]; then
  fail_test "install did not write paxl to HOME/.local/bin"
fi
if ! grep -Fq \
  'echo '\''export PATH="$HOME/.local/bin:$PATH"'\'' >> "$HOME/.zshrc"' \
  "$missing_path_output"; then
  fail_test "zsh PATH guidance was not clear and copyable"
fi
if ! grep -Fq 'export PATH="$HOME/.local/bin:$PATH"' "$missing_path_output"; then
  fail_test "PATH guidance did not include a command for the current shell"
fi
if ! grep -Fq "${missing_path_home}/.local/bin/paxl version" "$missing_path_output"; then
  fail_test "PATH guidance did not include a directly runnable paxl command"
fi
printf 'ok - install succeeds outside PATH and prints copyable zsh guidance\n'

custom_home="${test_dir}/custom-home"
custom_install_dir="${test_dir}/custom bin's"
custom_path_output="${test_dir}/custom-path-output"
mkdir -p "$custom_home"
if ! run_fake_install \
  "$custom_home" \
  '/usr/bin:/bin' \
  '/bin/zsh' \
  "$custom_install_dir" >"$custom_path_output" 2>&1; then
  fail_test "install failed for a custom directory containing spaces and a quote"
fi
if [[ ! -x "${custom_install_dir}/paxl" ]]; then
  fail_test "install did not honor the custom directory"
fi
if grep -Fq '$HOME/.local/bin' "$custom_path_output"; then
  fail_test "custom install guidance incorrectly referenced HOME/.local/bin"
fi
custom_persist_command="$(awk '
  /^  printf .* >> / {
    sub(/^  /, "")
    print
    exit
  }
' "$custom_path_output")"
if [[ -z "$custom_persist_command" ]]; then
  fail_test "custom install guidance did not include a persistent PATH command"
fi
if ! eval "$custom_persist_command"; then
  fail_test "custom persistent PATH command was not safely copyable"
fi
printf -v quoted_custom_install_dir '%q' "$custom_install_dir"
expected_custom_path_line="export PATH=${quoted_custom_install_dir}:\$PATH"
actual_custom_path_line="$(sed -n '1p' "${custom_home}/.zshrc")"
if [[ "$actual_custom_path_line" != "$expected_custom_path_line" ]]; then
  fail_test "custom persistent PATH command wrote the wrong install directory"
fi
custom_path_first_entry="$(
  PATH='/usr/bin:/bin'
  eval "$actual_custom_path_line"
  printf '%s' "${PATH%%:*}"
)"
if [[ "$custom_path_first_entry" != "$custom_install_dir" ]]; then
  fail_test "custom PATH command did not preserve spaces and quotes in the install directory"
fi
printf 'ok - custom install guidance persists the actual safely quoted directory\n'

present_path_home="${test_dir}/present-path-home"
present_path_output="${test_dir}/present-path-output"
if ! run_fake_install \
  "$present_path_home" \
  "${present_path_home}/.local/bin:/usr/bin:/bin" \
  '/bin/zsh' >"$present_path_output" 2>&1; then
  fail_test "install failed when HOME/.local/bin was already in PATH"
fi
if [[ ! -x "${present_path_home}/.local/bin/paxl" ]]; then
  fail_test "install did not write paxl to HOME/.local/bin when it was in PATH"
fi
if grep -Eq 'not (currently )?in PATH|\.zshrc|export PATH=' "$present_path_output"; then
  fail_test "install printed PATH guidance for a directory already in PATH"
fi
printf 'ok - install omits PATH guidance when HOME/.local/bin is already in PATH\n'

#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=release_paxl.sh
source "${script_dir}/release_paxl.sh"

fail_test() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

installer_test_dir="$(mktemp -d)"
trap 'rm -rf "$installer_test_dir"' EXIT
installer_test_path="${installer_test_dir}/install.sh"
bake_installer_manager_url \
  "${script_dir}/installer.sh" \
  "$installer_test_path" \
  "https://pax.home.example/base/"
installer_preamble="$(sed -n '4,6p' "$installer_test_path")"
if [[ "$(env -u PAXL_DOWNLOAD_URL bash -c "${installer_preamble}; printf '%s' \"\$PAXL_DOWNLOAD_URL\"")" != \
  "https://pax.home.example/base" ]]; then
  fail_test "released installer does not default binary resolution to its manager"
fi
printf 'ok - released installer defaults binary resolution to its manager\n'

if [[ "$(PAXL_DOWNLOAD_URL=https://override.example bash -c "${installer_preamble}; printf '%s' \"\$PAXL_DOWNLOAD_URL\"")" != \
  "https://override.example" ]]; then
  fail_test "runtime PAXL_DOWNLOAD_URL did not override the baked manager"
fi
printf 'ok - runtime PAXL_DOWNLOAD_URL overrides the baked manager\n'

installer_error="${installer_test_dir}/error"
if bake_installer_manager_url \
  "${script_dir}/installer.sh" \
  "$installer_test_path" \
  "https://user:top-secret@pax.home.example" 2>"$installer_error"; then
  fail_test "installer accepted manager credentials in the public URL"
fi
if grep -q "top-secret" "$installer_error"; then
  fail_test "invalid manager URL leaked credentials"
fi
printf 'ok - installer rejects credential-bearing manager URLs without leaking them\n'

early_call_marker="${installer_test_dir}/early-network-call"
early_failure_output="${installer_test_dir}/early-failure-output"
if (
  aws() {
    : >"$early_call_marker"
  }
  curl() {
    : >"$early_call_marker"
  }
  PAX_RELEASE_MANAGER_URL="https://user:early-secret@pax.home.example"
  main "9.9.9"
) >"$early_failure_output" 2>&1; then
  fail_test "release main accepted a credential-bearing manager URL"
fi
if [[ -e "$early_call_marker" ]]; then
  fail_test "release made an AWS or manager request before validating the manager URL"
fi
if grep -q "early-secret" "$early_failure_output"; then
  fail_test "early manager URL validation leaked credentials"
fi
printf 'ok - release rejects an invalid manager URL before network calls without leaking it\n'

empty_sha_hex="e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
empty_sha_base64="47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
head_checksum="$empty_sha_base64"
head_content_type="application/octet-stream"
head_sha="$empty_sha_hex"
head_size="123"
checksum_mode_supported="1"
put_should_fail="0"
put_error_message="simulated put failure"
put_expected_body="test-binary"
put_expected_object="paxl/releases/9.9.9/paxl"
put_expected_content_type="application/octet-stream"
put_expected_sha="$empty_sha_hex"
put_expected_checksum="$empty_sha_base64"

file_size() {
  printf '%s' "123"
}

aws() {
  [[ "$1" == "--region" && "$2" == "us-east-1" ]] || return 1
  [[ "$3" == "s3api" ]] || return 1
  case "$4" in
    put-object)
      [[ "$#" == "18" ]] || return 1
      [[ "$5" == "--bucket" && "$6" == "test-bucket" ]] || return 1
      [[ "$7" == "--key" && "$8" == "$put_expected_object" ]] || return 1
      [[ "$9" == "--body" && "${10}" == "$put_expected_body" ]] || return 1
      [[ "${11}" == "--content-type" && "${12}" == "$put_expected_content_type" ]] || return 1
      [[ "${13}" == "--metadata" && "${14}" == "sha256=${put_expected_sha}" ]] || return 1
      [[ "${15}" == "--checksum-sha256" && "${16}" == "$put_expected_checksum" ]] || return 1
      [[ "${17}" == "--if-none-match" && "${18}" == "*" ]] || return 1
      if [[ "$put_should_fail" == "1" ]]; then
        printf '%s\n' "$put_error_message" >&2
        return 9
      fi
      ;;
    head-object)
      if [[ "$#" == "12" ]]; then
        [[ "$9" == "--checksum-mode" && "${10}" == "ENABLED" ]] || return 1
        [[ "${11}" == "--output" && "${12}" == "json" ]] || return 1
        [[ "$checksum_mode_supported" == "1" ]] || return 2
      else
        [[ "$#" == "10" ]] || return 1
        [[ "$9" == "--output" && "${10}" == "json" ]] || return 1
      fi
      printf '{"ContentLength":%s,"ContentType":"%s","Metadata":{"sha256":"%s"},"ChecksumSHA256":"%s"}' \
        "$head_size" "$head_content_type" "$head_sha" "$head_checksum"
      ;;
    *)
      return 1
      ;;
  esac
}

installer_baked_sha="$(sha256_file "$installer_test_path")"
put_expected_body="$installer_test_path"
put_expected_object="paxl/releases/9.9.9/install.sh"
put_expected_content_type="text/x-shellscript"
put_expected_sha="$installer_baked_sha"
put_expected_checksum="$(sha256_hex_to_base64 "$installer_baked_sha")"
if ! upload_file \
  "$installer_test_path" \
  "test-bucket" \
  "$put_expected_object" \
  "$put_expected_content_type" \
  "$installer_baked_sha" >/dev/null; then
  fail_test "release did not upload the manager-baked installer body"
fi
printf 'ok - release uploads the manager-baked installer body\n'
put_expected_body="test-binary"
put_expected_object="paxl/releases/9.9.9/paxl"
put_expected_content_type="application/octet-stream"
put_expected_sha="$empty_sha_hex"
put_expected_checksum="$empty_sha_base64"

if ! upload_file \
  "test-binary" \
  "test-bucket" \
  "paxl/releases/9.9.9/paxl" \
  "application/octet-stream" \
  "$empty_sha_hex" >/dev/null; then
  fail_test "S3 upload was not guarded by If-None-Match"
fi
printf 'ok - S3 upload is write-once\n'

secret_marker="AWS_SECRET_ACCESS_KEY=must-not-appear"
retry_output=""
if ! retry_output="$(
  put_should_fail="1"
  put_error_message="$secret_marker"
  PAX_RELEASE_SKIP_VERIFY="1"
  upload_file \
    "test-binary" \
    "test-bucket" \
    "paxl/releases/9.9.9/paxl" \
    "application/octet-stream" \
    "$empty_sha_hex" 2>&1
)"; then
  fail_test "a failed PUT with a matching existing object was not idempotent"
fi
if [[ "$retry_output" == *"$secret_marker"* ]]; then
  fail_test "S3 PUT stderr leaked into release output"
fi
printf 'ok - failed PUT accepts a strictly matching existing object without leaking stderr\n'

if (
  put_should_fail="1"
  head_sha="different-sha256"
  PAX_RELEASE_SKIP_VERIFY="1"
  upload_file \
    "test-binary" \
    "test-bucket" \
    "paxl/releases/9.9.9/paxl" \
    "application/octet-stream" \
    "$empty_sha_hex"
) >/dev/null 2>&1; then
  fail_test "a failed PUT accepted a different existing object"
fi
printf 'ok - failed PUT rejects a mismatched existing object\n'

if [[ "$(sha256_hex_to_base64 "$empty_sha_hex")" != "$empty_sha_base64" ]]; then
  fail_test "hex sha256 was not converted to base64"
fi
printf 'ok - hex sha256 converts to base64 without platform-specific tools\n'

verify_s3_object \
  "test-bucket" \
  "paxl/releases/9.9.9/paxl" \
  "123" \
  "application/octet-stream" \
  "$empty_sha_hex"
printf 'ok - head verification checks the returned checksum\n'

if (
  head_checksum="wrong-checksum"
  verify_s3_object \
    "test-bucket" \
    "paxl/releases/9.9.9/paxl" \
    "123" \
    "application/octet-stream" \
    "$empty_sha_hex"
) >/dev/null 2>&1; then
  fail_test "head verification accepted a mismatched checksum"
fi
printf 'ok - head verification rejects a mismatched checksum\n'

if ! (
  checksum_mode_supported="0"
  head_checksum=""
  verify_s3_object \
    "test-bucket" \
    "paxl/releases/9.9.9/paxl" \
    "123" \
    "application/octet-stream" \
    "$empty_sha_hex"
) >/dev/null 2>&1; then
  fail_test "head verification did not fall back for a service without checksum mode"
fi
printf 'ok - head verification falls back for S3-compatible services without checksum mode\n'

installer_object="$(release_installer_object "paxl/releases" "9.9.9")"
if [[ "$installer_object" != "paxl/releases/9.9.9/install.sh" ]]; then
  fail_test "default installer object is not versioned: ${installer_object}"
fi
printf 'ok - default installer object is versioned\n'

json_error="${installer_test_dir}/json-error"
if printf '%s' '<html>X-Amz-Signature=response-secret</html>' |
  json_field data.url \
    'https://objects.test/install.sh?X-Amz-Signature=source-secret' \
    2>"$json_error"; then
  fail_test "invalid release JSON was accepted"
fi
if grep -Eq 'response-secret|source-secret|<html>|X-Amz-Signature' "$json_error"; then
  fail_test "invalid release JSON error leaked response or signed URL details"
fi
printf 'ok - invalid release JSON errors are redacted\n'

if printf '%s' '{}' |
  json_field data.url \
    'https://objects.test/install.sh?X-Amz-Signature=missing-source-secret' \
    2>"$json_error"; then
  fail_test "missing release JSON field was accepted"
fi
if grep -Eq 'missing-source-secret|X-Amz-Signature' "$json_error"; then
  fail_test "missing release JSON field error leaked a signed source URL"
fi
printf 'ok - missing release JSON field errors are redacted\n'

fake_resolver_signed_url='https://objects.test/paxl/releases/9.9.9/install.sh?X-Amz-Signature=resolver-secret'
fake_resolver_status='200'
fake_installer_redirect_location='https://objects.test/paxl/releases/9.9.9/install.sh?X-Amz-Signature=redirect-secret'
fake_installer_redirect_status='302'
fake_cf_header_marker="${installer_test_dir}/public-cf-header"
fake_public_auth_header_marker="${installer_test_dir}/public-auth-header"
fake_admin_cf_header_marker="${installer_test_dir}/admin-cf-header"
fake_admin_auth_header_marker="${installer_test_dir}/admin-auth-header"
export PAX_CLOUD_CF_CLIENT_ID='test-service-client'
export PAX_CLOUD_CF_CLIENT_SECRET='test-service-secret'

curl() {
  local target=""
  local header_path=""
  local output_path=""
  local header=""
  local saw_cf_header="0"
  local saw_auth_header="0"

  while (($# > 0)); do
    case "$1" in
      -H | --header)
        header="$2"
        if [[ "$header" == CF-Access-Client-* ]]; then
          saw_cf_header="1"
        fi
        if [[ "$header" == Authorization:* ]]; then
          saw_auth_header="1"
        fi
        shift 2
        ;;
      -D | --dump-header)
        header_path="$2"
        shift 2
        ;;
      -o | --output)
        output_path="$2"
        shift 2
        ;;
      http://* | https://*)
        target="$1"
        shift
        ;;
      *)
        shift
        ;;
    esac
  done

  case "$target" in
    */api/v1/public/artifacts/download*)
      if [[ "$saw_cf_header" == "1" ]]; then
        : >"$fake_cf_header_marker"
      fi
      if [[ "$saw_auth_header" == "1" ]]; then
        : >"$fake_public_auth_header_marker"
      fi
      if [[ -n "$output_path" ]]; then
        printf '{"data":{"url":"%s","version":"9.9.9","sha256":"%s","size_bytes":123}}' \
          "$fake_resolver_signed_url" \
          "$empty_sha_hex" >"$output_path"
        printf '%s' "$fake_resolver_status"
      else
        printf '{"data":{"url":"%s","version":"9.9.9","sha256":"%s","size_bytes":123}}' \
          "$fake_resolver_signed_url" \
          "$empty_sha_hex"
      fi
      ;;
    */api/v1/public/paxl/install.sh)
      if [[ "$saw_cf_header" == "1" ]]; then
        : >"$fake_cf_header_marker"
      fi
      if [[ "$saw_auth_header" == "1" ]]; then
        : >"$fake_public_auth_header_marker"
      fi
      [[ -n "$header_path" ]] || return 1
      printf 'HTTP/1.1 %s Found\r\nLocation: %s\r\n\r\n' \
        "$fake_installer_redirect_status" \
        "$fake_installer_redirect_location" >"$header_path"
      printf '%s' "$fake_installer_redirect_status"
      ;;
    */api/v1/admin/artifacts)
      if [[ "$saw_cf_header" == "1" ]]; then
        : >"$fake_admin_cf_header_marker"
      fi
      if [[ "$saw_auth_header" == "1" ]]; then
        : >"$fake_admin_auth_header_marker"
      fi
      printf '%s' '{"code":200}'
      ;;
    *)
      return 1
      ;;
  esac
}

if ! verify_public_installer_redirect "https://manager.test" >/dev/null; then
  fail_test "public installer redirect did not match its stable installer resolver"
fi
if [[ -e "$fake_cf_header_marker" || -e "$fake_public_auth_header_marker" ]]; then
  fail_test "public installer smoke sent an admin authentication header"
fi
printf 'ok - public installer redirect matches the stable installer resolver target\n'
printf 'ok - public installer smoke remains anonymous with CF credentials set\n'

public_artifacts="${installer_test_dir}/public-artifacts.jsonl"
append_artifact_metadata \
  "$public_artifacts" \
  "linux/amd64" \
  "paxl_9.9.9_linux_amd64" \
  "$empty_sha_hex" \
  "123" \
  "" \
  "test-bucket" \
  "paxl/releases/9.9.9/paxl_9.9.9_linux_amd64" \
  "0" \
  "application/octet-stream"
if ! verify_resolver_artifacts \
  "$public_artifacts" \
  "9.9.9" \
  "stable" \
  "https://manager.test" >/dev/null; then
  fail_test "anonymous public artifact resolver smoke failed"
fi
if [[ -e "$fake_cf_header_marker" || -e "$fake_public_auth_header_marker" ]]; then
  fail_test "public artifact resolver smoke sent an admin authentication header"
fi
printf 'ok - public artifact resolver smoke remains anonymous with CF credentials set\n'

if (
  fake_resolver_status='302'
  verify_public_installer_redirect "https://manager.test"
) >/dev/null 2>&1; then
  fail_test "public installer verification accepted a redirecting JSON resolver"
fi
printf 'ok - public installer verification requires an HTTP 200 JSON resolver\n'

redirect_error="${installer_test_dir}/redirect-error"
if (
  fake_installer_redirect_location='https://access.test/cdn-cgi/access/login?token=cf-login-secret'
  verify_public_installer_redirect "https://manager.test"
) >"$redirect_error" 2>&1; then
  fail_test "public installer verification accepted a Cloudflare login redirect"
fi
if grep -Eq 'cf-login-secret|resolver-secret|X-Amz-Signature' "$redirect_error"; then
  fail_test "public installer mismatch leaked a signed URL or redirect query"
fi
printf 'ok - public installer verification rejects CF login without leaking URLs\n'

if (
  fake_installer_redirect_status='200'
  verify_public_installer_redirect "https://manager.test"
) >/dev/null 2>&1; then
  fail_test "public installer verification accepted a non-302 response"
fi
printf 'ok - public installer verification requires HTTP 302\n'

PAX_RELEASE_TOKEN='admin-publish-token-secret'
admin_output="$(publish_artifact_metadata \
  "$public_artifacts" \
  "9.9.9" \
  "test-build" \
  '["stable"]' \
  "https://manager.test" 2>&1)"
if [[ ! -e "$fake_admin_cf_header_marker" || ! -e "$fake_admin_auth_header_marker" ]]; then
  fail_test "admin metadata publish did not receive its CF and bearer headers"
fi
if [[ "$admin_output" == *'test-service-secret'* ||
  "$admin_output" == *'admin-publish-token-secret'* ]]; then
  fail_test "admin metadata publish leaked a credential"
fi
printf 'ok - only admin metadata publish receives CF and bearer headers\n'

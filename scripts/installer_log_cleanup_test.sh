#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/installer.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
export HOME="$test_dir"
python3 - <<'PY'
import os, pathlib, time
logs = pathlib.Path.home() / '.pax/paxl/logs'
logs.mkdir(parents=True)
for name in ['paxl-20260101T000000.000000000Z-123.log', 'paxl-20260101T00Z.log', 'notes.log', 'paxl-invalid.log']:
    path = logs / name
    path.write_text('old\n')
    old = time.time() - 73 * 3600
    os.utime(path, (old, old))
(logs / 'paxl-20260102T000000.000000000Z-456.log').write_text('recent\n')
(logs / 'paxl-20260102T00Z.log').mkdir()
(logs / 'paxl-20260103T00Z.log').symlink_to(logs / 'notes.log')
PY
cleanup_execution_logs
python3 - <<'PY'
import pathlib
logs = pathlib.Path.home() / '.pax/paxl/logs'
assert not (logs / 'paxl-20260101T000000.000000000Z-123.log').exists()
assert not (logs / 'paxl-20260101T00Z.log').exists()
for name in ['notes.log', 'paxl-invalid.log', 'paxl-20260102T000000.000000000Z-456.log', 'paxl-20260102T00Z.log', 'paxl-20260103T00Z.log']:
    assert (logs / name).exists(), name
PY
cleanup_execution_logs
HOME="$test_dir/missing" cleanup_execution_logs
printf 'ok - installation cleanup expires owned logs and preserves recent files, unrelated files, directories, and symlinks\n'

failure_output="$(
  python3() { return 1; }
  cleanup_execution_logs 2>&1
)"
if [[ "$failure_output" != *"installation will continue"* ]]; then
  printf 'not ok - cleanup failure did not warn without failing installation\n' >&2
  exit 1
fi
printf 'ok - cleanup failures warn without failing installation\n'

"""Audit secret input on disposable PTYs with a fake CLI and optional native SDK."""
import os
import pty
import select
import signal
import shutil
import subprocess
import sys
import tempfile
import termios
import time
from pathlib import Path

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else '/tmp/paxl-local-harness-auth').resolve())
secret = b'offline-terminal-audit-key'
with tempfile.TemporaryDirectory(prefix='paxl-terminal-audit-') as directory:
    cli = Path(directory) / 'codex'
    cli.write_text('#!/bin/sh\nread -r value\n[ "$value" = "offline-terminal-audit-key" ] || exit 12\n')
    cli.chmod(0o700)
    for flag in ([], ['--secret-stdin']):
        for cancellation in ('none', 'signal', 'key'):
            master, slave = pty.openpty()
            before = termios.tcgetattr(slave)
            process = None
            try:
                environment = {'HOME': directory, 'PATH': os.environ['PATH'], 'CODEX_PATH': str(cli)}
                process = subprocess.Popen([binary, 'auth', 'login', '--harness', 'codex', '--method', 'api-key'] + flag,
                                           stdin=slave, stdout=slave, stderr=slave, cwd=directory, env=environment)
                output = b''
                deadline = time.monotonic() + 10
                while b'Please input credential:' not in output and time.monotonic() < deadline:
                    if select.select([master], [], [], .1)[0]:
                        output += os.read(master, 4096)
                assert b'Please input credential:' in output, output
                if cancellation == 'signal':
                    os.kill(process.pid, signal.SIGINT)
                elif cancellation == 'key':
                    os.write(master, b'\x03')
                else:
                    os.write(master, secret + b'\r')
                process.wait(timeout=5)
                while select.select([master], [], [], 0)[0]:
                    output += os.read(master, 4096)
                assert secret not in output, 'Secret echoed'
                after = termios.tcgetattr(slave)
                # Darwin marks pending-input reprocessing when canonical mode returns.
                after[3] &= ~getattr(termios, 'PENDIN', 0)
                before[3] &= ~getattr(termios, 'PENDIN', 0)
                assert after == before, 'Terminal settings were not restored'
                assert (process.returncode != 0) if cancellation != 'none' else (process.returncode == 0), output
            finally:
                if process and process.poll() is None:
                    process.kill()
                    process.wait()
                termios.tcsetattr(slave, termios.TCSANOW, before)
                os.close(master)
                os.close(slave)
# Exercise the SDK-owned prompt too: no explicit method, with stderr redirected.
dsh = shutil.which('dsh')
if dsh:
    with tempfile.TemporaryDirectory(prefix='paxl-sdk-terminal-audit-') as directory:
        master, slave = pty.openpty()
        before = termios.tcgetattr(slave)
        process = None
        try:
            env = {'HOME': directory, 'PATH': os.environ['PATH'], 'DSH_HOME': directory,
                   'DSH_EXECUTABLE': dsh}
            process = subprocess.Popen([binary, 'auth', 'login', '--harness', 'dsh',
                                        '--provider', 'deepseek-official'],
                                       stdin=slave, stdout=slave, stderr=subprocess.PIPE,
                                       cwd=directory, env=env)
            output = b''
            deadline = time.monotonic() + 15
            while b'Please input credential:' not in output and time.monotonic() < deadline:
                if select.select([process.stderr], [], [], .1)[0]:
                    chunk = os.read(process.stderr.fileno(), 4096)
                    if not chunk:
                        break
                    output += chunk
            assert b'Please input credential:' in output, output
            assert not termios.tcgetattr(slave)[3] & termios.ECHO, 'SDK prompt left terminal echo enabled when stderr was redirected'
            os.write(master, secret + b'\r')
            process.wait(timeout=10)
            output += process.stderr.read()
            while select.select([master], [], [], 0)[0]:
                output += os.read(master, 4096)
            assert process.returncode == 0, output
            assert secret not in output, 'SDK prompt echoed the credential'
            after = termios.tcgetattr(slave)
            after[3] &= ~getattr(termios, 'PENDIN', 0)
            before[3] &= ~getattr(termios, 'PENDIN', 0)
            assert after == before, 'SDK prompt did not restore terminal settings'
        finally:
            if process and process.poll() is None:
                process.kill()
                process.wait()
            if process:
                process.stderr.close()
            termios.tcsetattr(slave, termios.TCSANOW, before)
            os.close(master)
            os.close(slave)
else:
    print('SKIP SDK terminal prompt: DSH installation absent.')
print('PASS: hidden input, redirected SDK prompts, and Ctrl-C restore terminal echo; no real credentials used.')

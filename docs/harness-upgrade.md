# Local harness and ACP adapter upgrades

`paxl daemon harness` manages an explicitly selected local installation. These
commands do not contact paxd:

```sh
paxl daemon harness inspect claude
paxl daemon harness upgrade codex
paxl daemon harness upgrade codex --version 1.2.3
paxl daemon harness upgrade codex --component acp --version 1.2.3 --dry-run
paxl daemon harness upgrade claude --component acp --path /path/to/claude-agent-acp --version 1.2.3
paxl daemon harness rollback claude --component acp --path /path/to/claude-agent-acp --rollback-id ID
```

Supported installations include Unix npm global installations of
`@agentclientprotocol/claude-agent-acp`, `@agentclientprotocol/codex-acp`,
and `@ccgv2/pi-acp` for adapters, plus `@anthropic-ai/claude-code`,
`@openai/codex`, and `@earendil-works/pi-coding-agent` for native CLIs.
The default component is `cli`; pass `--component acp` to manage an adapter.
Inspection follows the launcher and validates its package name and declared bin
entry. Claude CLI also supports the official native installation: a launcher
symlink targeting `claude/versions/<version>`, with identity and version verified
by executing that binary. Homebrew, pnpm, bun, wrappers, npx and Windows launcher
replacement are unsupported. No fallback switches installation sources.

Omitting `--version` (or passing `--version latest`) resolves the npm `latest`
tag or Claude native release channel once and installs that exact version. JSON
`target_version` reports the resolved version. An explicit semantic version skips the registry lookup.
`--dry-run` inspects the selected installation and resolves latest when needed,
but does not install packages or switch the launcher. Registry failures leave
the installation unchanged. A real upgrade locks the
launcher, installs into a unique `.paxl-harness-versions` directory beside it,
checks package metadata and the executable's `--version` (Pi ACP uses `node --check`
because its entrypoint has no version flag), then atomically
replaces the selected symlink. Old package files remain intact for running
processes and rollback. Subsequent package-manager updates can replace that
launcher; rollback refuses a launcher that has changed since this upgrade.

For native Claude on macOS/Linux (x64/arm64, including Linux musl), paxl downloads
from `https://downloads.claude.ai/claude-code-releases/`. It validates the exact
version's platform manifest, binary size and SHA-256 before making the file
executable, then checks `--version` before switching. It does not run the
self-install command, which would switch the launcher outside paxl's control.
The old binary is copied into the staging directory so rollback survives cleanup
of Claude's original versions directory. Both upgrade and rollback preserve the
selected launcher path used by `CLAUDE_CODE_EXECUTABLE`; the daemon's existing
restart and runtime-version verification apply without a new command.

Native installations report `source: native`; `package` remains the logical
`@anthropic-ai/claude-code` identity and does not imply npm owns the binary.
Claude's independent auto-update behavior is not reconfigured. If an external
installer replaces the selected launcher, rollback refuses to overwrite it.

JSON schema version 1 reports the selected path, resolved path, source, package,
component and installed version. Upgrade returns `phase: installed` and a
rollback identifier. This is a disk/executable result, not proof of an ACP
process's running version. paxd owns draining, process restart and handshake
verification. `--verbose` writes progress to stderr; stdout remains JSON.

Installation files are retained indefinitely in this version. Credentials,
agent configuration, npm configuration and other launchers are not rewritten.
SIGTERM cancels npm and its child processes; a failed post-switch verification
restores the previous launcher. A machine crash may leave an unused staging
directory. No automatic package garbage collection is included.

Pi's scoped package pins its Pi SDK dependencies. Its entrypoint has no
`--version` handling, so local staging validates package identity and JavaScript
syntax; only the daemon's ACP handshake establishes that the target version is
running. See the [Pi entrypoint](https://github.com/CCGV2/pi-acp/blob/main/src/index.ts)
and [agent initialization](https://github.com/CCGV2/pi-acp/blob/main/src/acp/agent.ts).

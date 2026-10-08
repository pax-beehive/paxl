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

The first implementation supports Unix npm global installations of
`@agentclientprotocol/claude-agent-acp`, `@agentclientprotocol/codex-acp`,
and `@ccgv2/pi-acp` for adapters, plus `@anthropic-ai/claude-code`,
`@openai/codex`, and `@earendil-works/pi-coding-agent` for native CLIs.
The default component is `cli`; pass `--component acp` to manage an adapter.
Inspection follows the launcher and validates its package name and declared bin
entry. Native installers, Homebrew, pnpm, bun, wrappers, npx and Windows launcher
replacement are unsupported. No fallback switches installation sources.

Omitting `--version` (or passing `--version latest`) resolves the npm `latest`
tag once and installs that exact version. JSON `target_version` reports the
resolved version. An explicit semantic version skips the registry lookup.
`--dry-run` inspects the selected installation and resolves latest when needed,
but does not install packages or switch the launcher. Registry failures leave
the installation unchanged. A real upgrade locks the
launcher, installs into a unique `.paxl-harness-versions` directory beside it,
checks package metadata and the executable's `--version` (Pi ACP uses `node --check`
because its entrypoint has no version flag), then atomically
replaces the selected symlink. Old package files remain intact for running
processes and rollback. Subsequent package-manager updates can replace that
launcher; rollback refuses a launcher that has changed since this upgrade.

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

# Harness authentication

`paxl harness auth` runs official authentication commands on this machine.
It does not require paxd, a Manager connection, or a Pax account. The shorter
`paxl auth` entry is an alias. `paxl login` still signs in to Pax itself.

## Local methods

| Harness | Methods | Native commands |
| --- | --- | --- |
| Claude Code | `subscription` (default), `console` | `claude auth login`, `claude auth login --console` |
| Codex | `device` (default), `browser`, `api-key`, `access-token` | `codex login --device-auth`, `codex login`, `codex login --with-api-key`, `codex login --with-access-token` |
| Kimi Code (new CLI) | `device` (default) | `kimi login` |

```sh
paxl harness auth login --harness claude
paxl harness auth login --harness claude --method console
paxl harness auth login --harness codex
paxl harness auth login --harness codex --method browser
paxl harness auth login --harness kimi
```

The native command stays attached to this invocation. Open the URL it displays.
For a device flow, enter the displayed code **in the browser**, not in the terminal.
For Claude's manual-code flow, paste the authorization code in the same running
terminal when prompted. A browser callback may finish without manual input.
Cancel with Ctrl-C. Local login does not create a background session. Complete authorization input
in the running terminal invocation.

Device login may require account/workspace permission. Browser login on a remote
machine may require forwarding its localhost callback port. This release does
not proxy browser callbacks or implement PTY fallback. A native CLI version
without the requested command/flag fails explicitly; Pax does not silently
switch authentication or billing methods. Kimi support targets the new CLI with
`kimi login`, not every legacy Python release.

## Codex secrets

```sh
printenv OPENAI_API_KEY | paxl harness auth login --harness codex --method api-key --secret-stdin
printenv CODEX_ACCESS_TOKEN | paxl harness auth login --harness codex --method access-token --secret-stdin
```

Secrets are read as one line (maximum 4096 bytes), passed only on the native
process's stdin, and never placed in argv or a daemon request. Native output
for these two secret-entry methods is suppressed, including error output,
because a command could echo the supplied secret. A nonzero native exit is
still returned as an error. `--secret-stdin` is required for these methods and
rejected for browser/device methods. Piping from a secret manager is preferred;
plain terminal input is not a masked secret-entry form.

## Status and logout

```sh
paxl harness auth status --harness claude --format json
paxl harness auth status --harness codex --format json
paxl harness auth logout --harness claude
paxl harness auth logout --harness codex
```

Status invokes the native status command with a 15-second deadline and returns
only recognized authentication metadata. A known signed-out result succeeds
with `logged_in: false`; unknown output, network errors, malformed JSON, and
unexpected native failures are errors, not evidence that an account is logged out.
No credential file is opened by Pax. Status is a native CLI observation, not a
live model-request verification.

Kimi status and logout are deliberately unsupported here: this implementation
has not established independent native commands for them. It does not start a
TUI automatically or guess credential file contents.

Logout delegates to the official command. Completion does not claim that all
authentication is removed: keys in the environment, cloud identity, helpers,
and other profiles may still authenticate future processes.

## Environment and output

Native commands inherit this paxl process's working directory and environment,
including HOME, PATH, CLAUDE_CONFIG_DIR, CODEX_HOME, and KIMI_CODE_HOME. Claude
honors `CLAUDE_CODE_EXECUTABLE` and Codex honors `CODEX_PATH` so callers can bind
the exact executable. Values are executable paths, not shell command strings.
The native tool owns credentials, billing selection and token refresh.

Browser/device native interaction goes to stderr. Stdout is reserved for the
final result (`--format text`, default, or `--format json`). A successful login
returns `state: succeeded`, indicating native command completion; it does not
claim provider verification or model entitlement. `--verbose` reports the local
operation without secret input. Do not retain/share browser URLs, codes or native
login diagnostics. Raw native interaction is not a structured remote event protocol.

A real terminal's stdin stays in its foreground process group so native reads
work normally. Pipe-backed commands use a separate process group on Unix for
cancellation and descendant cleanup. Windows currently relies on native process
cancellation and does not promise Unix-style process-tree cleanup.

## Remaining work

Hermes, Gemini, OpenCode, OpenClaw, Kiro, DSH and DSCODE still need their own
provider-aware native adapters. Discovery/session support does not imply login
support. Multi-provider/multi-credential work is tracked in
[KEV-92](https://linear.app/kevin-geng/issue/KEV-92). Local and remote PTY fallback
is tracked separately in [KEV-104](https://linear.app/kevin-geng/issue/KEV-104).
The new local facade is the execution boundary for a future paxd → paxl path;
the web/Manager flow will call this local execution boundary in a separate change.

## Pi installation and provider authentication

Install with the official managed installer using `paxl harness install pi`.
The existing npm route is available with `--via npm`.
The component defaults to `cli`, the version to `latest`; use `--dry-run` to
preview or `--component acp` to install `@ccgv2/pi-acp` separately.
`paxl harness inspect pi` and `paxl harness upgrade pi` inspect and upgrade it.
Node 22.19+ is required for the CLI; use Node 24+ for the ACP adapter.

```sh
paxl harness auth methods --harness pi
paxl harness auth login --harness pi
paxl harness auth login --harness pi --provider openai --method oauth
printenv OPENAI_API_KEY | paxl harness auth login --harness pi --provider openai --method api-key --secret-stdin
paxl harness auth status --harness pi --format json
paxl harness auth logout --harness pi --provider openai
```

Pi authentication uses the SDK belonging to the selected npm or managed launcher
(`pi` on PATH, or `PI_EXECUTABLE`), requiring its ModelRuntime API (verified with
1.3.0). Managed installs resolve the owner marker and active release; the helper
uses Pi's private Node when present, as the official wrapper does.
There is no automatic SDK download or fallback to a different Pi installation.
The installed SDK supplies provider and method choices. Interactive login can
select a provider and method, and masks secret prompts; noninteractive callers
must select explicitly. Unsupported provider methods list supported choices.
Pi owns credential storage and its stable OAuth installation identity, respecting
`PI_CODING_AGENT_DIR`. Pax never writes a separate credential store.

Status lists stored credential metadata only, without resolving API key commands,
refreshing tokens, or making provider requests. `configured` does not mean that
credentials are accepted or unexpired; `unknown` does not rule out environment or
ambient credentials. Logout removes only the selected stored provider credential.
Session extensions and remote transport are outside this local adapter.

## Official local installation

`paxl harness install <harness>` defaults to `--via official` and `--component cli`.
Twelve external installation targets are supported, including Antigravity CLI. Use `--dry-run` to inspect the
actual route without downloads or mutations. Other routes are deferred except
for Pi's existing explicit `--via npm`.

| Harness | Official route |
| --- | --- |
| claude | https://claude.ai/install.sh (native) |
| codex | https://chatgpt.com/codex/install.sh (standalone) |
| pi | https://pi.dev/install.sh (managed) |
| gemini | npm package @google/gemini-cli |
| antigravity | https://antigravity.google/cli/install.sh (CLI binary: agy) |
| kimi | https://code.kimi.com/kimi-code/install.sh |
| opencode | https://opencode.ai/install (v1; no silent v2 migration) |
| hermes | https://hermes-agent.nousresearch.com/install.sh --skip-setup |
| openclaw | https://openclaw.ai/install.sh --no-onboard |
| kiro | https://cli.kiro.dev/install |
| dsh | published npm package @deepseek-ai/dsh, installed globally for a persistent launcher |
| dscode | https://raw.githubusercontent.com/qiz029/dscode/main/install.sh |

DSH's upstream quickstart uses npx for running; persistent installation here uses
the same published package through npm. This is not a separate vendor installer.
ACP packages for Claude, Codex, and Pi remain separate `--component acp` installs.
Other harnesses use their native ACP entry or separately managed adapters.

`install`, `inspect`, `upgrade`, and `rollback` default to `--format text`.
Explicit `--format json` retains the structured response without wrapping it in
text. Invalid formats are rejected before installation or launcher mutation.
Inspection and completed upgrades/rollbacks show observed versions and paths.
Installation summaries label requested versions separately and report installer
completion without claiming an independently verified version or launcher.

Scripts are fetched completely over HTTPS into private temporary files, bounded
to 2 MiB, checked for a shell-script header, and removed after execution or failure.
Their fixed interpreter receives validated version arguments without shell string
interpolation. Native stdout/stderr goes to stderr, stdin remains interactive,
and final results stay on stdout. Human-readable text is the default; use
`--format json` for machine-readable output. Inherited environment selects native paths and
installer behavior. No installer is run during dry-run. The publisher's script
owns platform support, dependency preparation, shell profile changes and existing
installation migration; failure does not promise a transaction or rollback.

For script routes `state: completed` means the official script exited successfully,
not that a requested install action or provider access was independently verified.
Scripts can offer maintenance actions for existing installs. npm routes return
`installed` on npm success. Open a new terminal if the installer updated PATH.
Installation does not imply that every harness has local login/inspect/upgrade
support: those command capabilities remain independent. For Pi managed installs,
inspect and SDK authentication are supported; use native `pi update` for updates.
Paxl refuses to replace the owned managed launcher with an npm-upgrade staging tree.

Verified exact-version mappings exist for Claude/DSCODE (positional), Codex
(`--release`), Kimi/OpenCode/OpenClaw (`--version`) and npm packages. Pi managed,
Hermes and Kiro currently reject explicit versions rather than ignoring them.
Default versions follow the official installer's channel. Shell routes currently
support macOS/Linux/WSL; native Windows shell installation requires a separate
PowerShell backend. npm routes rely on npm and the package's platform support.

### Antigravity CLI

```sh
paxl harness install antigravity --dry-run
paxl harness install antigravity
agy --version
```

This installs the independent CLI through Google's official Bash installer, not
Antigravity IDE and not a renamed Gemini installation. The current script targets
`~/.local/bin/agy`, supports macOS/Linux platform manifests, verifies the payload's
SHA-512 against its manifest, and delegates shell setup to `agy install`. An
existing binary makes this installer exit without reinstalling it; a successful
script exit therefore remains `completed`, not a verified installation claim.
No verified exact-version flag is exposed, so explicit `--version` is rejected.
Auth, discovery, sessions and ACP integration remain follow-up work under KEV-107.

Official installation reference: https://www.antigravity.google/docs/cli/install/

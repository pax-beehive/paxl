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

Gemini, Antigravity, DSH and DSCODE still need their own SDK/TUI authentication
adapters. OpenCode, Kiro, Hermes and OpenClaw native commands are described below. Discovery/session support does not imply login
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
Paxl compares common user shell startup files before and after successful installs,
including Bash profiles, Zsh files (also under absolute `ZDOTDIR`), publisher XDG
candidates and both hardcoded and XDG Fish paths. Results contain only changed
paths in the optional JSON field `changed_shell_files`, never configuration contents.
Text output lists observed changes and recommends a new terminal. A single changed,
existing Bash or Zsh file also gets a safely quoted `source` example, conditional
on the shell currently in use. Other shells and multiple-file changes get no guessed
reload command. If no changes are observed, output points to a new terminal or the
installer's PATH instructions when the command is not found.
This is best-effort observation of conventional files, not attribution to the
installer or verification of PATH. Unreadable, non-regular and over-2-MiB files are
skipped. Arbitrary custom files are not monitored; `/etc/profile` is included on
Unix because OpenCode can select it. Dry runs do not
inspect configuration. Paxl cannot update the parent terminal's PATH and does not
execute shell configuration files.
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

### Shell configuration audit (2026-10-18)

The following records inspection of downloaded publisher scripts without executing
installers. A candidate is not a promise of mutation: scripts check PATH, existing
content, shell and user choices. Bootstrap binaries and dependency installers may
have additional behavior that is not visible in the outer script.

| Publisher entry | Observed shell configuration behavior |
| --- | --- |
| [Codex](https://chatgpt.com/codex/install.sh) | `pick_profile`: macOS Zsh uses `~/.zprofile`, macOS Bash `~/.bash_profile`; Linux Zsh uses `~/.zshrc`, Linux Bash `~/.bashrc`; fallback `~/.profile`. Adds/replaces a delimited PATH block when needed. |
| [Pi](https://pi.dev/install.sh) | `shell_config_file` selects `${ZDOTDIR:-$HOME}/.zshrc`, `~/.bashrc` if present otherwise `~/.profile`, or hardcoded `~/.config/fish/config.fish`. `prompt_add_path_to_profile` asks through `/dev/tty` before appending and skips a matching line. |
| [Kimi](https://code.kimi.com/kimi-code/install.sh) | `_detect_shell_rc` selects `~/.zshrc`; Bash picks the first existing `.bashrc`, `.bash_profile`, `.profile` (otherwise `.bashrc`); Fish uses hardcoded `~/.config/fish/config.fish`; fallback `.profile`. `_update_path` skips when disabled, already on PATH or already configured. |
| [OpenCode](https://opencode.ai/install) | Chooses the first existing candidate. Zsh checks `${ZDOTDIR:-$HOME}/.zshrc`, `.zshenv`, then `$XDG_CONFIG_HOME/zsh/.zshrc`, `.zshenv`; Bash checks `~/.bashrc`, `.bash_profile`, `.profile`, then `$XDG_CONFIG_HOME/bash/.bashrc`, `.bash_profile`. Fish uses hardcoded `~/.config/fish/config.fish`; sh/ash checks `~/.ashrc`, `~/.profile`, `/etc/profile`. Respects `--no-modify-path`; does not create a missing startup file in this branch. |
| [Hermes](https://hermes-agent.nousresearch.com/install.sh) | `wire_shell_path` updates both `.zshrc` and `.zprofile` for Zsh; hardcoded `~/.config/fish/config.fish` for Fish; otherwise `.bashrc`, `.profile`, and existing `.bash_profile`/`.bash_login`. Skips matching PATH setup. |
| [OpenClaw](https://openclaw.ai/install.sh) | `persist_shell_path_prepend` can update multiple Bash/Zsh files and hardcoded `~/.config/fish/conf.d/openclaw.fish`, including existing files for other shells. Other branches also inspect `${ZDOTDIR:-$HOME}` files and may prepare Node/Homebrew. |
| [DSCODE](https://raw.githubusercontent.com/qiz029/dscode/main/install.sh) | Fresh-install shell script creates the command link and prints `Add ... to PATH` if needed; it does not itself write a shell startup file. An existing managed install may delegate to the binary's updater. |
| [Claude](https://claude.ai/install.sh) | Downloads a binary and runs `claude install`; the outer script does not choose or write a startup file. Do not infer a specific file from it. |
| [Antigravity](https://antigravity.google/cli/install.sh) | Runs `agy install` after downloading; the outer script does not implement startup-file selection. Existing binaries cause an early exit. An [upstream report](https://github.com/google-antigravity/antigravity-cli/issues/745) describes changes to `.zshrc`, `.zprofile`, `.bashrc`, `.profile`; this is reported binary behavior, not independently reproduced here. |
| [Kiro](https://cli.kiro.dev/install) | macOS copies and opens Kiro CLI.app, delegating setup to the app; Linux executes the archive's install.sh with `KIRO_CLI_SKIP_SETUP=1`. Outer script does not enumerate dotfiles. An [upstream report](https://github.com/kirodotdev/Kiro/issues/4138) describes Zsh startup integration; timing can outlive the outer installer. |
| Gemini / DSH | Official persistent routes use npm. Registry metadata for `@google/gemini-cli@0.64.0` and `@deepseek-ai/dsh@0.2.0-rc.2` has no package-level lifecycle scripts. This does not rule out dependency hooks or changes made by a separately installed Node/npm toolchain. |

The observer covers these explicit startup-file candidates, including both the
hardcoded and XDG config roots and the `/etc/profile` fallback on Unix. It does
not recursively watch arbitrary dotfiles, dependency installation paths or
background mutations occurring after the installer returns. The reminder also
covers already-configured PATH entries: an unchanged file does not prove the
current terminal has loaded it.

## Additional local native authentication

These adapters run dedicated native commands, without paxd, PTY emulation, hidden
installs, or a second credential store. The installed CLI must expose the mapped
subcommands and flags; older versions fail explicitly. Secrets entered through a
native masked prompt stay in that native process. Status captures bounded output
and returns only recognized metadata, never raw credentials or diagnostics.

| Harness | Login | Status | Logout |
| --- | --- | --- | --- |
| OpenCode | `opencode auth login`, optional `--provider`; method selection stays native | `opencode auth list`; stored credential count only | `opencode auth logout`, optional provider positional argument |
| Kiro | `kiro-cli login`; `--method device` adds `--use-device-flow` | `kiro-cli whoami --format json`; recognized account type only | `kiro-cli logout` |
| Hermes | `hermes auth add <provider>`, optional `--type oauth` or `--type api-key` | `hermes auth status <provider>`; recognized native provider state only | `hermes auth logout <provider>` |
| OpenClaw | `openclaw models auth login`, optional native provider/method; API-key stdin uses `models auth paste-api-key` | `models auth list --json`; selected profile ID, provider and credential type only | `models auth logout <profileId>`; native confirmation remains enabled |

### OpenCode

```sh
paxl harness auth login --harness opencode
paxl harness auth login --harness opencode --provider openai
paxl harness auth status --harness opencode
paxl harness auth logout --harness opencode --provider openai
```

The installed OpenCode provider/plugin selects subscription/OAuth versus API-key
methods; native API-key prompts are masked. `--method` is rejected: native plugins use method labels, but non-plugin
providers can silently ignore that flag and take the API-key path. Select the
method in the native menu so Pax cannot pretend a requested method was applied. Pax does not invent OAuth support for a provider.
`--secret-stdin` is rejected because no dedicated safe native key-stdin flag was
established. `auth methods` remains Pi-only. Status lists all providers, so
`--provider` on OpenCode status is rejected instead of silently ignored.

The credential-summary parser was checked against the native list implementation
in [OpenCode v1.19.3](https://github.com/anomalyco/opencode/blob/v1.19.3/packages/opencode/src/cli/cmd/providers.ts).
It returns `configured` for a nonzero count, `unknown` for zero (environment
credentials may still apply), and an error for unrecognized output. Native
command completion does not guarantee provider access or that OAuth succeeded.

### Kiro

```sh
paxl harness auth login --harness kiro
paxl harness auth login --harness kiro --method device
paxl harness auth status --harness kiro
paxl harness auth logout --harness kiro
```

`kiro-cli` is accepted as a harness alias. Default login and `--method browser`
run the same native command; Kiro can still select device flow in a remote
environment. Only `--method device` explicitly forces device flow. Browser
account selection stays native. API-key environments remain owned by
`KIRO_API_KEY`; Pax does not persist a key or change the shell environment.
Status requires recognized JSON account metadata; native failures and unknown
schemas remain errors. `configured` is not proof of an active subscription or
model entitlement. See the [native command reference](https://kiro.dev/docs/cli/reference/cli-commands/).

### Hermes

```sh
paxl harness auth login --harness hermes --provider openai-codex --method oauth
paxl harness auth login --harness hermes --provider anthropic --method api-key
paxl harness auth status --harness hermes --provider anthropic
paxl harness auth logout --harness hermes --provider anthropic
```

Hermes requires `--provider` for these operations. An omitted method uses the
provider's native default, which can be API key or OAuth. The installed CLI checks
provider capability. API-key entry uses its secure prompt; `--secret-stdin` is
rejected. Provider logout follows Hermes' native semantics and may clear several
credentials owned by that provider. Pax does not substitute removal of one pool
entry. Status is conservative: recognized configured credentials become
`configured`; a recognized no-active-login result becomes `unknown` because other
pooled/environment credentials may apply. Errors or unrecognized output are not
reported as logged out. Hermes status can perform its own credential-pool healing;
Pax does not suppress the native behavior. References:
[native parser](https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/subcommands/auth.py),
[native handlers](https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/auth_commands.py).

### OpenClaw

```sh
paxl harness auth login --harness openclaw
paxl harness auth login --harness openclaw --provider openai-codex --method device-code
printenv OPENAI_API_KEY | paxl harness auth login --harness openclaw --provider openai --method api-key --secret-stdin
paxl harness auth status --harness openclaw
paxl harness auth status --harness openclaw --provider openai
paxl harness auth logout --harness openclaw --profile openai:manual
```

Interactive login requires a real terminal. Native plugins own method IDs;
`api-key` is the explicit Pax route to `paste-api-key` and requires one bounded
secret line on stdin. Secret-entry native output is discarded, including failure
output, to prevent key echoes. No secret appears in argv. The native CLI owns
profile persistence/configuration and any communication with an existing Gateway.
Pax does not pass `--set-default`, `--relogin`, or `--yes`.
Status is stored profile metadata, not a live credential probe. An empty list
means `unknown`; profiles mean `configured`, including possibly expired profiles.
Logout requires one exact `--profile` from status and rejects `--provider` to avoid
ambiguously removing accounts. Other credentials and environment keys may remain.
The native version must support [models auth list/logout](https://github.com/openclaw/openclaw/blob/main/src/cli/models-cli.ts).

Executable overrides for these adapters are `OPENCODE_EXECUTABLE`,
`KIRO_CLI_EXECUTABLE`, `HERMES_EXECUTABLE` and `OPENCLAW_EXECUTABLE`. They are
executable paths, not shell strings. Otherwise the caller's PATH selects the CLI.

Gemini, Antigravity, DSH and DSCODE still require individual SDK/TUI authentication
adapters. Installation/discovery does not imply local auth support. Do not
simulate authentication by launching a general chat session and claiming success;
PTY fallback and remote forwarding remain separate work under KEV-104.

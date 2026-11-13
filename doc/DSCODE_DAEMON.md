# DSCODE discovery and connections

Install the official self-contained [DSCODE](https://github.com/qiz029/dscode)
release on the machine running paxd. It bundles Node and the pinned runtime;
system Node and npm are not required. GitHub terminal archives, the maintained
Homebrew formula, and the separate Desktop package currently report 0.7.54.
Legacy npm launcher/bundle packages stop at 0.7.40 and are not the current
terminal distribution. Resolve future terminal updates from GitHub releases.

```sh
paxl daemon harness install dscode --dry-run
paxl daemon harness install dscode
# Add ~/.local/bin to the paxd service PATH.
# Start dscode and use /login to configure providers, then exit it.
paxl daemon harness discover dscode
paxl daemon agent create --harness dscode --name dscode
paxl daemon agent list
```

The install command downloads a reviewed, commit-pinned official installer on
the CLI host. The installer resolves the newest complete platform release,
verifies its SHA-256 checksum and bundled modules, installs under
`~/.local/share/dscode-app`, and links `~/.local/bin/dscode`. Existing foreign
command links are refused. There is no npm fallback. Discovery only checks for
the executable; it does not start, install or authenticate it.

Subsequent standalone updates use `dscode update` (or an exact version), after
all sessions and schedulers exit. Homebrew installations use `brew upgrade dscode`.
This installation alignment does not add DSCODE to the remote harness upgrade
command; that coordinator still requires separate process and version support.

Migration from npm/Hub or source installations is explicit. Keep old data and
credentials until sessions have been checked. Select an existing Hub data home
with `DSCODE_HOME` only after stopping its old processes; the new application
creates its own profile. It does not automatically import `~/.dscode` credentials.
Follow the upstream [migration guide](https://github.com/qiz029/dscode/blob/main/docs/distribution.md#migration-from-older-dscode-installations).
Do not run legacy and standalone applications against the same data concurrently.

Update both paxl and paxd. The daemon advertises `dscode acp` directly. DSCODE
owns preset composition and uses the saved default model and native credentials.
ACP clients answer approval requests under the `ask` permission preset. First-install progress goes
to stderr so stdout remains protocol-only. See the upstream
[ACP guide](https://github.com/qiz029/dscode/blob/main/docs/acp.md).

For a source checkout, configure `node /absolute/path/to/dscode/bin/dscode.mjs acp`
as the connection's explicit command, or expose that checkout through a `dscode`
wrapper on PATH. Append `--model provider/id` to select an initial model explicitly.

Use `--remote <id>` when creating a connection if multiple remotes are configured.
Existing paxd ACP forwarding and supervision handle the connection. This creates
or resumes ACP-owned sessions; it does not attach ACP to a TUI that already holds
the session's write lock.

## Local sessions

```sh
paxl agent list
paxl session list --agent dscode
paxl session get dscode:<native-id>
paxl resume dscode:<native-id>
```

The adapter reads durable Harness logs without launching DSCODE or loading
credentials. Titles, transcripts, tool results and workspace roots reuse the
DSH reader, including v0–v4 JSONL and concatenated Zstandard frames. Session IDs
use the independent `dscode:` prefix. Native resume runs `dscode resume <id>`.
Prompt delivery to an already running session uses `dscode send <id> -- <text>`;
it queues the message and returns the bridge acknowledgment, not a completed
model response. Inactive sessions must first be resumed. New remote sessions
are created through the daemon's ACP connection.

The default log root is `~/.local/share/dscode-state/sessions`. If that directory
is absent, an existing `~/.local/share/dscode-hub/sessions` remains readable.
When both exist, the standalone directory takes precedence. `DSCODE_HOME`
overrides the home and `PAXL_DSCODE_SESSIONS_DIR` overrides the session directory.
A source/tar installation stores sessions under its own `.runtime/sessions`;
set `PAXL_DSCODE_SESSIONS_DIR` to that directory for local listing/history.
If the source ACP launcher uses `DSCODE_ACP_HOME`, point the read override at
`<DSCODE_ACP_HOME>/sessions`.
These read overrides do not redirect the native launcher's storage. DSH's
`DSH_HOME` and `PAXL_DSH_SESSIONS_DIR` do not select DSCODE logs. For daemon
history reporting, paxd forwards only the DSCODE storage overrides to paxl.

## Validation

Go tests cover discovery without automatic installation, native connection
command selection, local delivery/resume, independent storage and v3/v4
transcripts. Upstream's `tests/acp.test.mjs` covers native launcher routing and
preset composition; `scripts/verify-acp.mjs` exercises the real runtime with a
local model fixture, including client approval and cancellation.

Verified against upstream main `6bd354e`: all six native ACP unit tests and
the real-runtime probe passed (handshake, custom provider, tool call, client
allow/reject, cancellation, reply, list, resume, close and stdin EOF). The probe
used an isolated home and a local model fixture, with no paid model requests.

# DSCODE discovery and connections

Install and initialize [DSCODE](https://github.com/qiz029/dscode) on the machine
running paxd. Node 22.19+ on the 22.x line or Node 24+ is required.

```sh
paxl daemon harness install dscode --dry-run
paxl daemon harness install dscode
dscode install
# Start dscode and use /login to configure the provider, then exit it.
paxl daemon harness discover dscode
paxl daemon agent create --harness dscode --name dscode
paxl daemon agent list
```

Use a build containing upstream commit `6bd354e` (PR #7), which adds native
`dscode acp`. Main still reports version 0.7.35 at that commit, but the earlier
published 0.7.35 package lacks the command. Use a source build containing the
commit or a subsequent release with ACP support; installing `@latest` alone
does not prove support.

The installation command runs `npm install -g @toddzheng024/dscode@latest` on
the CLI host. Discovery only checks for the executable and never starts it.
The daemon must resolve both DSCODE and a supported Node version in its PATH.
Use the same user/home as the configured installation, or supply `DSCODE_HOME`
and provider credentials in the connection environment. Discovery availability
does not verify credentials, profile dependencies or ACP support.

Update both paxl and paxd. The daemon advertises `dscode acp` directly. DSCODE
owns preset composition and uses the saved default model and native credentials.
ACP clients answer approval requests under the `ask` permission preset. Title,
session-card and memory generation are disabled. First-install progress goes
to stderr so stdout remains protocol-only. See the upstream
[ACP guide](https://github.com/qiz029/dscode/blob/6bd354e/docs/acp.md).

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

The default log root is `~/.local/share/dscode-hub/sessions`. `DSCODE_HOME`
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

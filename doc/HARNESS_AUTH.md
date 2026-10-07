# Harness authentication

Use the top-level `auth` commands to check or restore Claude Code and Codex authentication
on the machine running the target paxd. These are separate from `paxl login`,
which signs in to Pax itself. This version supports `claude` (alias `claude-code`)
and `codex`; other harnesses are rejected before contacting the daemon.

Both paxl and paxd must include harness authentication support. Building paxl
alone does not update an already-running paxd.

## Check and log in

```sh
paxl auth status --harness claude
paxl auth login --harness claude
paxl auth login --harness claude --method console
```

Login prints an authorization URL and deadline. Open the URL in your browser.
If the browser supplies an authorization code, run:

```sh
paxl auth login --harness claude --code-stdin
```

Paste the full authorization code (including its `#...` suffix if supplied) and
press Enter. One input line is read; EOF is not required. No code is accepted in
command-line arguments or printed in verbose output. A local browser callback
may finish the login without a manual code.

The default Claude method is `subscription`. Use `console` explicitly for
Anthropic Console billing. Both use Claude's native browser flow.

## Codex

```sh
paxl auth status --harness codex
paxl auth login --harness codex
```

The default Codex method is `device`. Open the displayed URL and enter the
displayed one-time code **in the browser**. Do not submit it with `--code-stdin`.
The daemon waits for the native Codex process to finish. Device-code login must
be enabled for the account or workspace; failure never switches login methods.

For native API-key or access-token login, read the secret from stdin:

```sh
paxl auth login --harness codex --method api-key --secret-stdin
paxl auth login --harness codex --method access-token --secret-stdin
```

Paste one line and press Enter, or pipe it from a secret manager. The secret is
sent through the target daemon socket and the native process's stdin, never
argv or Pax's durable command journal. Native Codex owns credential storage.
An installed Codex version must support the selected native flag.

Use `paxl auth status --harness <harness>` to inspect progress. The latest owned
attempt, including a failed or cancelled result, remains visible until its
original deadline. After that, status checks native credentials again. Thus a
terminal result is a recent observation, not a fresh provider probe. Successful
native login and status do not prove that a later model request will succeed.

## Other harnesses

Hermes, Pi, Gemini, Kimi, OpenCode, OpenClaw, Kiro and DSH are not implemented in
this authentication API. Harness discovery or session support does not imply
login support. In particular, Hermes has provider-specific pooled credentials;
Pax must not silently add to or replace that pool. See the daemon's
`internal/harnessauth/DESIGN.md` for the separate multi-provider proposal.
Deferred support is tracked in [KEV-92](https://linear.app/kevin-geng/issue/KEV-92).

## Lifetime

paxd launches the selected native login with a fixed argument list, retains the process
and its stdin pipe, and reads its output. paxl communicates only with the daemon
API. The login stays alive when paxl exits; no local CLI session file is created.

Each attempt expires five minutes after it starts. A repeated `login` from the
same control source returns the same pending attempt and URL without extending
its deadline, provided the harness, method and input are unchanged. Another
source cannot take over the attempt. One login mutation runs at a time. On completion,
expiry, or daemon shutdown, paxd reaps the process. Restarting paxd invalidates
unfinished logins. There is no user-facing session CRUD or session ID flag.

Cancel an attempt with `paxl auth cancel --harness claude` or
`paxl auth cancel --harness codex`. Cancellation does not log out an account.
If the old process is still stopping, a new start returns a retryable conflict.
Code submission and cancellation first resolve the owned attempt and bind the
operation to that exact internal handle; they cannot be retargeted if it changes.

paxl waits up to 30 seconds for the authorization URL or submitted-code result.
If that client-side wait times out, use `auth status` or repeat `auth login` to
retrieve the pending attempt; the daemon deadline remains unchanged. There is
no automatic logout, agent restart, or background sign-in by paxl.

## Output and target

Both commands accept `--format json` and `--verbose`. JSON includes `harness`,
`state`, and applicable `logged_in`, `auth_method`, `authorization_url`,
`expires_at`, `method`, `user_code`, and `error_code` fields. Device codes are
user-facing login secrets; do not share or retain the JSON login output.
Internal login handles are not exposed.
A logged-out status is a successful inspection (exit 0); inspect `logged_in` or
`state` in scripts. Command errors and failed login attempts exit nonzero.

The default target is `~/.paxd/paxd.sock`. Use `--socket /path/to/paxd.sock` on
both commands to select another Unix socket, including a locally forwarded
socket. Alternatively run paxl on the target machine through SSH. These commands
do not yet route through a cloud manager or accept a cloud node selector.

Credentials belong to the system user and environment running paxd, including
its HOME, PATH, CLAUDE_CONFIG_DIR and CODEX_HOME. Existing agent processes with another
credential context may not use that login. The native harness owns credential
storage and token refresh.

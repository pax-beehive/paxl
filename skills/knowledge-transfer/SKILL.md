---
name: knowledge-transfer
description: Use paxl for local-first agent session knowledge transfer. Trigger this skill when the user asks to move context between Codex, Claude, Pi, or Kiro sessions; create, inject, list, get, or archive a knowledge capsule; mirror one agent session into another; or resume work in a different local agent.
---

# Knowledge Transfer

Use `paxl` to transfer local agent context without asking the user to manually
copy long transcripts. Prefer the installed `paxl` on `PATH`.

## Install Paxl

Before transferring context, make sure `paxl` is installed:

```sh
paxl version
```

When network access is available, optionally check whether the local binary is
behind the hosted stable build:

```sh
paxl update check
```

If an update is available, tell the user before continuing. Do not install or
replace the binary without explicit user approval.

If it is missing, install the latest stable hosted build:

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
```

Install a specific uploaded version when needed:

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | PAXL_VERSION=0.1.0 bash
```

If the installer warns that the install directory is not in `PATH`, either add
that directory to the shell profile or call the printed absolute `paxl` path.

## Choose the Flow

- Use `session mirror` when the user wants to move the current working context
  into another agent session. Mirror transfers the source session timeline
  without keyword summarization.
- Use `capsule create` plus `capsule inject` when the user wants reusable,
  keyword-focused context that can be stored, inspected, and injected later.
- Use `capsule inject --new --agent <agent>` when the capsule should start a
  new target agent session instead of targeting an existing session.
- Use `session get` to inspect transcript content and confirm what was
  delivered.

## Session Discovery

List available agents:

```sh
paxl agent list
```

List sessions, syncing local session data into paxl SQLite:

```sh
paxl session list --agent codex --limit 10
paxl session list --agent claude --limit 10
paxl session list --agent kiro --limit 10
```

Use cached metadata only when explicitly wanted:

```sh
paxl session list --no-sync
```

Search indexed session content. By default this returns cached SQLite results
immediately, then starts a best-effort background refresh for future queries:

```sh
paxl session query "keyword or phrase"
paxl session query "keyword or phrase" --limit 20
paxl session query "keyword or phrase" --format jsonl
```

For a pure cached lookup with no background refresh side effect:

```sh
paxl session query "keyword or phrase" --no-background-sync
```

If search misses content that may only exist in fresh local logs, explicitly
refresh recent session indexes before searching. This foreground sync is bounded
and may return cached results if the sync budget is exhausted:

```sh
paxl session query "keyword or phrase" --sync
paxl session query "keyword or phrase" --sync --timeout 10s
```

User-facing session IDs are typed:

```text
codex:<native-id>
claude:<native-id>
pi:<native-id>
kiro:<native-id>
```

If the user gives a bare native ID, pass `--agent <agent>` or convert it to a
typed ID before invoking transfer commands.

## Knowledge Capsules

Create a keyword-focused capsule from a source session:

```sh
paxl capsule create codex:<source-session-id> --keyword "topic or feature"
```

Create a capsule from prepared content without asking the source agent to
summarize:

```sh
paxl capsule create codex:<source-session-id> \
  --keyword "topic" \
  --title "short title" \
  --summary "short summary" \
  --content "Prepared transfer context..."
```

Create a manual capsule from prepared content when there is no useful source
session:

```sh
paxl capsule create --manual \
  --keyword "topic" < /path/to/content.md
```

Use local transcript extraction only when the user explicitly wants local
matching instead of source-agent generation:

```sh
paxl capsule create codex:<source-session-id> --keyword "topic" --local
```

Inspect stored capsules:

```sh
paxl capsule list --limit 20
paxl capsule get <capsule-id>
```

Queue a capsule for an existing target session. The capsule is delivered by the
agent hook the next time that session receives a user prompt:

```sh
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Queue by route when the exact target session is not known yet:

```sh
paxl capsule inject <capsule-id> --match project --project paxl --agent codex
paxl capsule inject <capsule-id> --match keyword --keyword "handoff" --agent claude
```

Start a new session from a capsule:

```sh
paxl capsule inject <capsule-id> --new --agent codex
```

Archive obsolete capsules:

```sh
paxl capsule archive <capsule-id>
```

## Session Mirror

Mirror a source session into an existing target session:

```sh
paxl session mirror claude:<source-session-id> --to-session codex:<target-session-id>
```

Mirror a source session into a new target agent session:

```sh
paxl session mirror codex:<source-session-id> --to claude
```

Do not add a keyword to mirror. If a keyword is required, use capsule creation
instead.

Capsule handoffs should include language equivalent to:

```text
NO ACTIONABLE ITEMS: This is knowledge transfer only.
Acknowledge receipt only; do not start implementation or run tools.
```

If the user says it is acceptable to trigger an agent reply, keep this wording
so the target agent acknowledges receipt rather than starting work.

## Confirmation

After injection or mirror, verify the durable record:

```sh
paxl capsule injection --target-session codex:<target-session-id> --format jsonl --limit 5
```

Check for:

- `status:"delivered"`
- the expected target session ID

Verify transcript visibility:

```sh
paxl session get codex:<target-session-id> --format jsonl | tail -n 8
```

## Accepted Inbox Sync

If the user accepted an envelope outside the local CLI, such as through the
manager API or a hosted UI, the local SQLite store may not yet contain the
capsule or routed hook injection. Sync accepted inbox envelopes before
debugging the hook path:

```sh
paxl inbox sync
paxl inbox sync --limit 20
paxl inbox sync --format jsonl
```

Use this when:

- the remote inbox shows `status:"accepted"` but `paxl capsule list` does not
  show the expected capsule;
- the user expects a routed capsule to appear on the next prompt, but
  `paxl capsule injection --format jsonl` does not show a pending route;
- an envelope was accepted from a web surface instead of `paxl inbox accept`.

`paxl inbox accept <envelope-id>` is safe to run for an already accepted
envelope. It should skip the remote accept call and materialize missing local
state. Repeated sync or accept operations should reuse the local capsule keyed
by `remote_envelope:<envelope-id>` and should not create duplicate route
injections.

After syncing, verify durable local state:

```sh
paxl capsule list --source-session remote_envelope:<envelope-id> --format jsonl
paxl capsule injection --format jsonl --limit 10
```

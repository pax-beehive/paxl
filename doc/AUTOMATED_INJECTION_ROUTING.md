# Automated Injection Routing Design

This document records the design and current implementation slice for
automatic, pre-user-prompt knowledge injection.

## Goal

`paxl` should be able to deliver accepted knowledge capsules into local agent
sessions without requiring users to run a visible hook command. The user-facing
surface stays centered on setup, capsule creation, local injection routing, and
friend envelope delivery.

The runtime hook should be installed by `paxl setup`. When an agent is about to
process a user prompt, the hook asks paxl whether any local capsule or accepted
envelope route should be injected before that user prompt. If no route matches,
the hook is a no-op.

## User-Facing Commands

`paxl setup` installs local integration hooks for supported agents. The exact
runtime entrypoint is an implementation detail and should not appear as a
normal user command.

Current setup slice:

- Claude Code: writes a real `UserPromptSubmit` command hook into the user's
  Claude settings.
- Codex: writes a paxl-owned hook descriptor and shim. Activation depends on a
  Codex hook host reading that descriptor.
- Hermes: writes a paxl-owned hook descriptor and shim. Activation depends on a
  Hermes hook host reading that descriptor.

Local injection can either deliver immediately to a known target session, start
a new target session, or create a conditional hook route.

Immediate local injection:

```sh
paxl capsule inject <capsule-id> codex:<target-session-id>
paxl capsule inject <capsule-id> --new --agent codex
```

This preserves the existing explicit delivery behavior.

Conditional local injection:

```sh
paxl capsule inject <capsule-id> --match any
paxl capsule inject <capsule-id> --match project --project paxl
paxl capsule inject <capsule-id> --match keyword --keyword "hook layer"
paxl capsule inject <capsule-id> --match project --project paxl --agent codex
```

This creates a local pending injection route. It uses the same local hook-time
matching rules as accepted envelope routes, but the capsule already exists
locally and no inbox acceptance step is involved. A matching hook atomically
claims the route, renders the `system_handoff` context to stdout for the agent
hook, and marks the route consumed so it cannot be injected again.

Cross-user delivery uses envelope routing conditions because the sender cannot
know the recipient's local session IDs:

```sh
paxl capsule send <capsule-id> --to @alice --match any
paxl capsule send <capsule-id> --to @alice --match project --project paxl
paxl capsule send <capsule-id> --to @alice --match keyword --keyword "hook layer"
```

`--agent` may narrow any conditional route to a specific agent:

```sh
paxl capsule send <capsule-id> --to @alice --match project --project paxl --agent codex
```

`paxl inbox accept <envelope-id>` accepts the envelope, stores its capsule
locally, and persists the routing condition as a pending local route. It does
not resolve the target session. Target session selection happens only when a
hook event is triggered by a real local agent prompt.

## Routing Model

An injection route has three concerns:

- Source: either a local `capsule inject` request or an accepted inbox
  envelope.
- Owner: the local user who owns the route.
- Match condition: when a hook event is eligible for injection.
- Consume policy: how the route is claimed and marked consumed.

The first implementation supports these match kinds:

- `any`: matches any eligible hook event after acceptance.
- `project`: matches when any hook event workspace root has
  `filepath.Base(root) == project`.
- `keyword`: matches when the current user input prompt contains the keyword as
  a substring.

Project matching intentionally uses only the final path segment as an exact
match. For example, `/Users/alice/work/paxl` matches project `paxl`. The route
does not store or compare the sender's absolute path because recipient machines
use different filesystem layouts.

Keyword matching uses the current user input prompt, not prior transcript
history. The current implementation uses case-sensitive substring matching.

Session IDs are valid only for direct local injection and for the local hook
event that eventually consumes a conditional route. They are not part of
cross-user `capsule send` routing. Remote session IDs are local to the
recipient and are not knowable by the sender.

## Hook Event

The hidden runtime hook receives a structured event from the agent integration:

```json
{
  "schema_version": "paxl.hook.user_prompt.v1",
  "agent": "codex",
  "session_id": "session-id",
  "hook_event_id": "stable-turn-or-user-prompt-id",
  "cwd": "/Users/alice/work/paxl",
  "prompt": "continue the paxl hook layer design"
}
```

The hook event must be available before the agent assembles the model request
for the current user prompt. If an agent only supports appending a later prompt
or steering an already-started turn, paxl must not claim that it can inject into
the current prompt-cache-friendly position for that agent.

## Prompt Placement

Automatic injection must preserve prompt cache behavior as much as possible.
The injected context is placed after stable existing context and before the
current user prompt:

```text
stable cached context
pre-user paxl knowledge injection
current user prompt
```

The hook must not rewrite prior transcript history, mutate system or developer
messages, or append the knowledge after the current user prompt. If the
integration cannot place context before the current user prompt, the route
should remain pending or be treated as a later-turn fallback with explicit
status.

## Claim and Consume State

Route consumption must be durable and atomic. A process-local mutex is
insufficient because multiple paxl processes may be invoked by agent hooks.

The store exposes an atomic claim operation backed by a SQLite transaction:

```text
pending -> claimed -> consumed
                 \-> failed
                 \-> expired
```

State meanings:

- `pending`: route is available for future hook events.
- `claimed`: one hook event has atomically reserved the route.
- `consumed`: the agent confirmed that the context was inserted into the model
  request before the current user prompt.
- `failed`: the hook or agent integration failed before consumption.
- `expired`: a claim lease expired before consumption and may be retried if the
  route policy allows it.

The hook should use a stable `hook_event_id` supplied by the agent integration.
Retries for the same hook event must return the same claim instead of creating a
second injection. The store should enforce uniqueness for the route, target
session, and hook event combination so a route cannot repeatedly pollute the
same prompt context.

## Package Placement

The user-visible CLI remains thin:

- `cmd/paxl` parses `setup`, `capsule inject`, `capsule send`, and inbox
  commands into request structs.
- `internal/facade` owns local route creation, envelope acceptance,
  hook-event matching, claim, render, and consume workflows.
- `internal/model` and `internal/model/store` own route, claim, and consumption
  persistence.
- `pkg/adaptor` continues to hide agent-specific session and delivery details.
  It should not own envelope routing decisions.

The runtime hook entrypoint may be implemented as a hidden command or helper
binary, but it is plumbing installed by `paxl setup`, not part of the normal
user-facing command surface.

## Safety Constraints

- `capsule send` requires an accepted friend alias and keeps the recipient's
  inbox user-owned.
- `inbox accept` stores the capsule and route condition, but does not bind a
  session.
- Cross-user routes do not use remote session IDs.
- Local `capsule inject` may use either a precise `--to-session` route or a
  conditional `--match` route.
- `any` routes are allowed but should be presented as broad routing because
  they can match the next eligible prompt.
- A route is consumed once by default.
- If no route matches a hook event, the hook returns no context and exits
  successfully.
- Durable injection records are the source of truth for whether an injection
  was claimed, rendered, consumed, failed, or expired.

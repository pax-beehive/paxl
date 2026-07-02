# Architecture

`paxl` is structured as a local-first CLI with a small command surface and clear
package boundaries. The core rule is that upper layers orchestrate use cases,
while lower layers hide agent-specific transports and persistence details.

## Package Layout

```text
cmd/paxl
  CLI parsing, validation, rendering, and process entrypoint.

internal/facade
  Application use cases. Coordinates adapters and local persistence.

internal/model
  Domain models and SQLite persistence.

pkg/adaptor
  Agent adapters. Exposes an ACP-like session interface over local logs,
  native CLIs, or future gateways.
```

Dependency direction:

```text
cmd/paxl -> internal/facade -> internal/model
                           -> pkg/adaptor
```

Rules:

- `cmd/paxl` must not contain application orchestration.
- `internal/facade` owns workflows such as session listing, mirroring, capsule
  creation, and injection.
- `internal/model` owns durable state and must not depend on CLI or adapters.
- `pkg/adaptor` hides agent-specific details and must not depend on facade or
  SQLite persistence.

## CLI Layer

`cmd/paxl` uses `github.com/urfave/cli/v3`.

Responsibilities:

- Parse raw CLI input.
- Convert raw enum strings into typed model values.
- Validate required flags and arguments.
- Create facade request structs.
- Pass stdout/stderr explicitly.
- Render table, JSONL, transcript, or HTML output.
- Wire `--verbose` into facade/adaptor options.

The CLI should stay thin. If a command needs non-trivial behavior, that behavior
belongs in `internal/facade`.

## Facade Layer

`internal/facade` contains application use cases.

Main responsibilities:

- Resolve agent/session identifiers.
- Sync local session metadata into SQLite.
- Load session timelines.
- Create knowledge capsules.
- Inject capsules into target sessions.
- Mirror source sessions into target agents.
- Store durable records for capsules and injections.

The facade layer is the place where local persistence and adapter capabilities
meet. It should not know how a specific agent stores logs or runs its native CLI.

## Model and Store Layer

`internal/model` defines domain models such as:

- agents
- sessions
- session elements
- knowledge capsules
- knowledge injections

`internal/model/store` persists local state in SQLite:

- session metadata
- session elements
- capsule records
- injection records
- sync metadata

SQLite is local cache and local truth for Pax artifacts created by `paxl`.
Source agent logs remain the source of truth for raw agent session timelines.

## Adapter Layer

`pkg/adaptor` exposes a stable agent interface:

- agent info
- session list
- session get
- prompt existing session
- start new session

The adapter interface is intentionally ACP-like: upper layers ask for session
capabilities, not for specific file formats or process invocations.

Current adapters:

- Codex: reads local Codex logs and delivers with Codex app-server or CLI.
- Claude: reads local Claude Code logs and delivers with Claude Code CLI.
- Pi: reads local Pi logs and delivers with Pi CLI.
- Kiro: reads local Kiro CLI logs and delivers with Kiro CLI.
- OpenClaw: uses ACP `session/list` and `session/prompt` through the configured
  OpenClaw ACP command.

Current delivery commands:

```text
Codex App/Desktop existing session:
                        codex app-server thread/resume + turn/steer when an
                        active turn is steerable, otherwise turn/start
Codex other existing session or app-server fallback:
                        codex exec resume --all <session-id> -
Codex new session:      codex exec -

Claude existing session: claude --print --resume <session-id>
Claude new session:      claude --print

Pi existing session:     pi --session <session-id> -p
Pi new session:          pi -p

Kiro existing session:   kiro-cli chat --resume-id <session-id> --no-interactive <message>
Kiro new session:        kiro-cli chat --no-interactive <message>

OpenClaw existing session:
                        openclaw acp + ACP session/prompt
OpenClaw session list:  openclaw acp + ACP session/list
OpenClaw command override:
                        PAXL_OPENCLAW_ACP_COMMAND
```

Adapter stdout/stderr is buffered by default. `--verbose` can surface delivery
details without polluting normal command output.

## Hook Entry Point

`paxl setup` installs hidden agent hook plumbing. Agent-specific native hook
formats and descriptor files converge on one paxl entrypoint:

```text
paxl __agent-hook --agent <agent> --event user-prompt
```

The CLI entrypoint parses native payload shapes into a small hook event:
agent, event name, session ID, project path, and current prompt. The facade then
claims matching pending injection routes, renders the handoff, writes the
agent-specific hook response, and marks the route consumed after output
succeeds.

Hook scripts and hosts should not implement routing policy. They report the
event and let the facade decide whether to inject, ignore, or keep a route
pending. This keeps duplicate-consume prevention and delivery records in the
SQLite-backed model layer.

## Session Identity

`paxl` uses typed session IDs at user-facing boundaries:

```text
codex:<native-id>
claude:<native-id>
pi:<native-id>
kiro:<native-id>
openclaw:<native-id>
```

The facade parses these IDs before business logic uses them. Bare native IDs are
allowed only when the caller also provides an agent.

This separation matters because local transcript IDs are not necessarily ACP
session IDs. Existing local sessions are resumed through
their native CLIs instead of pretending they are attachable ACP sessions.

## Session Mirror

`LocalCollaborationFacade` is the local multi-agent collaboration seam. It
exposes user-intent operations such as moving session context between agents,
then hides whether the implementation uses mirror capsules, native CLI resume,
hook injection, or a future local memory surface. The interface returns a
normalized communication view so callers can reason about source agent, target
agent, session IDs, and delivery method without knowing the adapter transport.
`MoveSessionContext` is for live continuity. `ShareSessionMemory` is for
creating reusable memory first, then optionally delivering or queueing it for a
target agent. Hook-time memory delivery is described with `LocalMemoryRoute`
instead of the lower-level capsule `MatchType` and `MatchValue` fields.

`session mirror` moves source session context into another agent session.

Important semantics:

- It reads the source timeline locally.
- It does not ask the source agent to summarize.
- It does not use a keyword.
- It sends a `system_handoff` message to the target agent.
- It carries `From` and `To` metadata with node, agent, and session IDs.
- The target agent decides whether to summarize, compress, or keep the context.

High-level flow:

```text
CLI request
  -> facade parses source and target sessions
  -> session facade loads full source timeline
  -> capsule facade builds a mirror handoff from local elements
  -> adapter delivers the handoff to an existing or new target session
  -> store records the mirror injection
```

Use this when a live working context should continue elsewhere, such as moving a
Claude Code session into Codex after Claude quota is exhausted.

## Knowledge Capsules

Knowledge capsules are reusable handoff artifacts.

Unlike `session mirror`, capsule creation is keyword-driven:

- Default mode asks the source agent to generate a portable capsule.
- `--local` mode extracts matching raw local transcript lines as an offline
  fallback.
- `--content` creates a capsule from prepared operator-written content
  instead of prompting the source agent or extracting transcript lines.
- `--manual` reads `--content` or stdin to create a prepared-content capsule
  without loading a source session and records `source_agent=paxl`,
  `source_session_id=manual`.
- Capsules store source node, source agent, and source session metadata.
- Injections store target node, target agent, and target session metadata.
- Capsules are stored in SQLite.
- Capsules can be listed, rendered, archived, and injected later.

High-level flow:

```text
capsule create
  -> facade resolves source session, loading local logs on cache miss
  -> default: adapter prompts source agent to generate marked JSON
  -> local: facade extracts matching local transcript context
  -> content: facade stores prepared content with source metadata
  -> store writes capsule

capsule inject
  -> facade loads capsule and target session, loading local logs on cache miss
  -> existing session: adapter delivers system_handoff to target
  -> new session: adapter starts target agent with system_handoff
  -> store records injection
```

Use capsules when the goal is reusable knowledge transfer. Use mirror when the
goal is live session continuity.

The automated injection routing design is documented separately in
[AUTOMATED_INJECTION_ROUTING.md](AUTOMATED_INJECTION_ROUTING.md). The current
implementation keeps hook installation behind `paxl setup`, routes local
conditional capsule injections at hook-trigger time, and places injected context
before the current user prompt instead of rewriting existing transcripts.

Node IDs are local identity hints, not pax-manager node records. `paxl` uses
`PAXL_NODE_ID` when it is set, otherwise it falls back to the local hostname and
then `local`. This keeps local transfers self-describing without requiring
cloud registration.

## Team Graph

`TeamFacade` (`internal/facade/team.go`) performs read-only GETs against
`/api/v1/user/:user_id/teams[...]` on PAX Manager and never caches results in
SQLite. `ListAllAgents` aggregates per-team agents client-side, de-duplicating
by `agent_id` and attaching the teams each agent belongs to, while excluding the
caller's own agents by default; this produces a ranked set of delivery candidates
for future cross-team routing. The `paxl team` CLI commands are read-only
(list/get/agents); team management operations such as create, invite, and remove
are not part of the CLI.

## Verbose Output

Normal output should stay machine-readable where requested. Adapter child
process stdout/stderr is buffered to avoid corrupting JSONL/table output.

When `--verbose` is set, delivery details may be written to stderr through the
configured verbose writer. Verbose messages should be English, start with a
capital letter, and end with a period.

## Extension Points

To add another agent adapter:

1. Implement the `pkg/adaptor.Adapter` interface.
2. Parse local session metadata into `model.Session`.
3. Parse timeline entries into `model.Element`.
4. Implement existing-session prompt delivery if available.
5. Implement new-session start delivery if available.
6. Register the adapter in the default registry.
7. Add adapter tests with real local-log-like fixtures.

Future adapters may use ACP, native CLIs, gateways, local logs, or combinations
of those transports. The facade should not need to change for each transport
choice.

## Design Constraints

Project coding rules that affect architecture:

- Request and response structs are passed by pointer.
- Options use `opts ...func(*Option)`.
- External input is parsed before business logic.
- Errors are wrapped with useful context.
- String enums start with an unknown value.
- Method receivers prefer pointers.
- Code and comments are English.
- Non-trivial behavior should have focused tests.

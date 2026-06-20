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

- Codex: reads local Codex logs and delivers with Codex CLI.
- Claude: reads local Claude Code logs and delivers with Claude Code CLI.

Current delivery commands:

```text
Codex existing session: codex exec resume --all <session-id> -
Codex new session:      codex exec -

Claude existing session: claude --print --resume <session-id>
Claude new session:      claude --print
```

Adapter stdout/stderr is buffered by default. `--verbose` can surface delivery
details without polluting normal command output.

## Session Identity

`paxl` uses typed session IDs at user-facing boundaries:

```text
codex:<native-id>
claude:<native-id>
```

The facade parses these IDs before business logic uses them. Bare native IDs are
allowed only when the caller also provides an agent.

This separation matters because local transcript IDs are not necessarily ACP
session IDs. For Codex and Claude, existing local sessions are resumed through
their native CLIs instead of pretending they are attachable ACP sessions.

## Session Mirror

`session mirror` moves source session context into another agent session.

Important semantics:

- It reads the source timeline locally.
- It does not ask the source agent to summarize.
- It does not use a keyword.
- It sends a `system_handoff` message to the target agent.
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
- `--local` mode extracts matching local transcript lines.
- Capsules are stored in SQLite.
- Capsules can be listed, rendered, archived, and injected later.

High-level flow:

```text
capsule create
  -> facade resolves source session, loading local logs on cache miss
  -> default: adapter prompts source agent to generate marked JSON
  -> local: facade extracts matching local transcript context
  -> store writes capsule

capsule inject
  -> facade loads capsule and target session, loading local logs on cache miss
  -> existing session: adapter delivers system_handoff to target
  -> new session: adapter starts target agent with system_handoff
  -> store records injection
```

Use capsules when the goal is reusable knowledge transfer. Use mirror when the
goal is live session continuity.

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

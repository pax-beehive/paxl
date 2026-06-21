# AGENTS.md

## Project Context

This repository contains `paxl`, an open-source Pax CLI. The project is a hard fork of the earlier `paxctl` work, but code must be reimplemented inside this repository. Do not import packages from the old `paxd` repository.

The CLI binary name is `paxl`.

## Architecture

Use this layering:

- `cmd/paxl`: CLI wiring only. Use `github.com/urfave/cli/v3`.
- `internal/facade`: Application use cases and orchestration.
- `internal/model`: Domain models and SQLite persistence.
- `pkg/adaptor`: Agent adapters and the stable session interface exposed to upper layers.

Dependency direction:

- `cmd/paxl` depends on `internal/facade`.
- `internal/facade` depends on `internal/model` and `pkg/adaptor`.
- `pkg/adaptor` must not depend on `internal/facade` or SQLite persistence.
- `internal/model` must not depend on CLI or adapters.

The adapter layer should hide whether an agent is backed by ACP, local logs, a native CLI resume command, or a gateway. Upper layers should interact with an ACP-like session interface.

## Target Capabilities

The CLI should preserve the important user-facing capabilities from the earlier local-first Pax CLI:

- List supported agents and their capabilities.
- List local agent sessions.
- Render a session timeline.
- Create knowledge capsules from source sessions.
- List, get, archive, and inject knowledge capsules.
- Mirror one session into another agent session.

Codex and Claude should be modeled as local-log plus native delivery, not as arbitrary ACP attachment to existing local sessions.

Important behavior to preserve:

- Codex App/Desktop existing session delivery uses `codex app-server` `thread/resume` plus `turn/steer` when an active turn is steerable; it falls back to `turn/start` for idle app sessions and to `codex exec resume --all <session-id> -` when app-server delivery fails.
- Codex non-App existing session delivery uses `codex exec resume --all <session-id> -`.
- Codex new session delivery uses `codex exec -`.
- Claude existing session delivery uses `claude --print --resume <session-id>`.
- Claude new session delivery uses `claude --print`.
- Adapter stdout and stderr must be buffered unless intentionally emitted through verbose tracing.
- Capsule creation should default to asking the source agent to generate a capsule.
- Local transcript extraction should be explicitly requested.
- Generated capsule parsing should only accept assistant-like output roles so prompt examples are not parsed as results.

## Go Style

Follow these rules for all Go code:

- Request and response parameters must be pointers unless they are primitive values, `context.Context`, or `error`.
- Options must use the `opts ...func(*Option)` style.
- Method receivers should usually be pointers.
- Non-trivial enums should be string enums.
- The first enum value must be the unknown value.
- IPC and external input boundaries must parse raw values before business logic uses them.
- Errors must be wrapped with useful context using `%w`.
- Logs and verbose messages must be English, start with a capital letter, and end with a period.
- Code, comments, help text, and test names must be English.
- Keep cyclomatic complexity below 20 per method.
- Add comments for non-trivial design reasons. Do not comment obvious code behavior.

Example API shape:

```go
func (f *SessionsFacade) List(
	ctx context.Context,
	req *ListSessionsRequest,
	opts ...func(*Option),
) (*ListSessionsResponse, error)
```

Example option shape:

```go
type Option struct {
	VerboseWriter io.Writer
}

func WithVerboseWriter(writer io.Writer) func(*Option) {
	return func(option *Option) {
		option.VerboseWriter = writer
	}
}
```

## CLI Design

Use `urfave/cli/v3`.

The CLI layer should:

- Parse CLI arguments into request structs.
- Validate CLI input before calling facade methods.
- Convert raw enum strings into typed enum values before business logic.
- Pass stdout and stderr explicitly.
- Support verbose output from the beginning of each feature design.
- Avoid embedding application orchestration in command actions.

Command actions should follow this shape:

```go
req, err := parseListSessionsRequest(cmd)
if err != nil {
	return fmt.Errorf("parse list sessions request: %w", err)
}

resp, err := facade.List(ctx, req, facade.WithVerboseWriter(stderr))
if err != nil {
	return fmt.Errorf("list sessions: %w", err)
}

if err := renderSessions(stdout, resp, req.Format); err != nil {
	return fmt.Errorf("render sessions: %w", err)
}
```

## Testing Workflow

Before implementing code:

- Align the change direction and package boundaries with the user.
- Align the test plan with the user.
- Then execute a TDD loop.

Use this TDD flow:

- Write one behavior test.
- Confirm it fails for the expected reason.
- Implement the minimum code to pass.
- Refactor only after tests are green.
- Repeat with the next behavior.

Testing preferences:

- Prefer `testify/suite` for suites when it improves setup clarity.
- Prefer table-driven tests for input and output matrices.
- Test behavior through public interfaces.
- Avoid tests coupled to private implementation details.
- Do not write all tests first and all implementation afterward.

Test names should describe behavior, not implementation.

## Migration Rules

This repository is a hard fork.

- Do not import the old `paxd` code.
- Do not preserve old `paxctl` package paths.
- Do not copy old package boundaries blindly.
- It is acceptable to inspect the old repository to understand behavior.
- Reimplement code under the new `paxl` architecture.

SQLite schema and command behavior may be semantically inherited, but the model types, migrations, facade APIs, adapters, and tests must live in this repository.

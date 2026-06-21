# paxl

`paxl` is a local-first CLI for inspecting, transferring, and reusing AI agent
session context.

It is useful when you work across multiple local coding agents and need a
practical way to keep context moving without manually copying long transcripts.
For example, if your Claude Code quota is exhausted, you can mirror the current
Claude session into Codex, Pi, or Kiro so another local agent can continue from
the same context.

Chinese documentation: [doc/README_cn.md](doc/README_cn.md)

Architecture documentation: [doc/ARCHITECTURE.md](doc/ARCHITECTURE.md)

## What It Does

- Lists supported local agents.
- Lists local agent sessions from local logs.
- Renders a session timeline as transcript, JSONL, or HTML.
- Mirrors one session into another agent session.
- Creates reusable knowledge capsules from source sessions.
- Injects knowledge capsules into target sessions.
- Stores local metadata in SQLite.

Current built-in agents:

- `codex`: local Codex logs plus Codex CLI delivery.
- `claude`: local Claude Code logs plus Claude Code CLI delivery.
- `pi`: local Pi logs plus Pi CLI delivery.
- `kiro`: local Kiro CLI logs plus Kiro CLI delivery.

## Install

Build from source:

```sh
go build -trimpath -o ./paxl ./cmd/paxl
```

Optional local install:

```sh
mkdir -p ~/bin
cp ./paxl ~/bin/paxl
```

Check the installed binary:

```sh
paxl version
```

## Data Model

`paxl` is local-first. It reads local agent logs and uses a local SQLite
database for cached metadata, capsules, and injection records.

Use `--db` to choose the SQLite file:

```sh
paxl --db .local/paxl.sqlite session list
```

If `--db` is omitted, `paxl` uses its default local database path.

## Common Workflows

### List Available Agents

```sh
paxl agent list
```

### List Local Sessions

```sh
paxl session list
paxl session list --agent claude --limit 10
paxl session list --agent codex --format jsonl
```

Use cached metadata without scanning local logs:

```sh
paxl session list --no-sync
```

### Read a Session

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format html --output session.html
```

### Mirror a Session Into Another Agent

Mirror transfers the source session context into a target agent session. It does
not ask the source agent to summarize with a keyword. The target agent receives a
`system_handoff` message and can decide whether to summarize, compress, or keep
the full context.

Mirror Claude into an existing Codex session:

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to-session codex:<target-session-id>
```

Start a new Codex session from a Claude session:

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to codex
```

Start a new Claude session from a Codex session:

```sh
paxl session mirror \
  codex:<source-session-id> \
  --to claude
```

### When Claude Code Quota Is Exhausted

1. Find the latest Claude session:

   ```sh
   paxl session list --agent claude --limit 5
   ```

2. Find the Codex session you want to continue in:

   ```sh
   paxl session list --agent codex --limit 5
   ```

3. Mirror the Claude context into Codex:

   ```sh
   paxl session mirror \
     claude:<source-session-id> \
     --to-session codex:<target-session-id> \
     --verbose
   ```

Codex will receive the mirrored context through its native resume path. You can
then ask Codex to continue the work, review the handoff, or compress the context.

### Create a Knowledge Capsule

Capsules are reusable handoff artifacts. Unlike `session mirror`, capsule
creation is keyword-driven and asks the source agent to produce a portable
summary by default.

Ask the source agent to generate a capsule:

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
```

Use local transcript extraction instead:

```sh
paxl capsule create codex:<session-id> --keyword "sqlite schema" --local
```

List and inspect capsules:

```sh
paxl capsule list
paxl capsule get <capsule-id>
```

Inject a capsule into a target session:

```sh
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Start a new target agent session with a capsule:

```sh
paxl capsule inject <capsule-id> --new --agent codex
```

Archive a capsule:

```sh
paxl capsule archive <capsule-id>
```

## Agent Delivery Semantics

Codex delivery:

- Existing session: `codex exec resume --all <session-id> -`
- New session: `codex exec -`

Claude delivery:

- Existing session: `claude --print --resume <session-id>`
- New session: `claude --print`

Pi delivery:

- Existing session: `pi --session <session-id> -p`
- New session: `pi -p`

Kiro delivery:

- Existing session: `kiro-cli chat --resume-id <session-id> --no-interactive <message>`
- New session: `kiro-cli chat --no-interactive <message>`

`paxl` buffers child process output by default. Use `--verbose` when you want
delivery details.

## Development

Common commands:

```sh
make format
make lint
make test
make test-cover
make mock
make gen
```

The CI coverage gate is 80%.

## Status

`paxl` is an early open-source CLI. The architecture is designed for more agent
adapters. Codex, Claude, Pi, and Kiro are built in today.

## Platform Support

The CLI architecture and SQLite storage are cross-platform Go code, but the
built-in adapters depend on local agent log locations and native CLIs.

Current support boundary:

- macOS: verified with local Codex, Claude Code, Pi, and Kiro CLI log shapes.
- Linux: expected to be close to macOS if `~/.codex/sessions`,
  `~/.claude/projects`, `~/.pi/agent/sessions`, `~/.kiro/sessions`, and the
  matching CLIs are available, but it still needs real-world validation.
- Windows: not fully validated. Path handling, Claude project directory
  decoding, fake-command tests, and native CLI resume behavior need dedicated
  Windows coverage.

In short: macOS is verified, Linux is expected to work with similar local agent
layouts, and Windows should be treated as experimental until tested.

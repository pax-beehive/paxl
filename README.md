# paxl

`paxl` is a local-first context bridge for AI coding agents.

It helps you move working context between Codex, Claude Code, Pi, Kiro, Gemini,
and OpenClaw without manually copying long transcripts or uploading your session
history to a cloud service.

The practical use case is simple: when one local agent is out of quota, stuck on
a task, or better suited for a different step, `paxl` can hand the current
session context to another agent and keep the work moving.

Chinese documentation: [doc/README_cn.md](doc/README_cn.md)

Architecture documentation: [doc/ARCHITECTURE.md](doc/ARCHITECTURE.md)

Planned automated injection routing:
[doc/AUTOMATED_INJECTION_ROUTING.md](doc/AUTOMATED_INJECTION_ROUTING.md)

## Quick Install

Install the latest stable hosted build:

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
```

Install a specific uploaded version:

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | PAXL_VERSION=0.1.0 bash
```

Check the installed binary:

```sh
paxl version
```

Check whether a newer hosted stable build is available:

```sh
paxl version --check
```

Upgrade the installed binary in place:

```sh
paxl update
```

Build from source instead:

```sh
go build -trimpath -o ./paxl ./cmd/paxl
mkdir -p ~/bin
cp ./paxl ~/bin/paxl
```

## First Win: Continue Elsewhere

This is the workflow to try first. It proves the core idea without needing to
learn every command.

1. Check which local agents have a CLI and which have local session logs:

   ```sh
   paxl agent list
   ```

2. List recent Claude Code sessions:

   ```sh
   paxl session list --agent claude --limit 5
   ```

3. List recent Codex sessions:

   ```sh
   paxl session list --agent codex --limit 5
   ```

4. Mirror Claude into an existing Codex session:

   ```sh
   paxl session mirror \
     claude:<source-session-id> \
     --to-session codex:<target-session-id> \
     --verbose
   ```

If an agent has no local logs yet, session listing returns an empty list for that
agent instead of failing the whole command.

## Why Use It

- Continue work in another agent when quota, model behavior, or tool access
  changes mid-task.
- Preserve a session timeline as transcript, JSONL, or HTML before handing work
  off.
- Create reusable knowledge capsules for decisions, bugs, release plans, and
  project-specific context.
- Inject a prepared handoff into an existing session or start a new target agent
  session from it.
- Let a Codex skill call `paxl` for repeatable context-transfer workflows.

## Agent Skill

This repository includes a Codex skill for repeatable local knowledge transfer
workflows:

```sh
mkdir -p ~/.codex/skills
cp -R skills/knowledge-transfer ~/.codex/skills/
```

If you want an agent to install it for you, ask the agent to read this
repository first and then install the skill from `skills/knowledge-transfer`.
A good prompt is:

```text
Read this repository, inspect skills/knowledge-transfer/SKILL.md, then install
the knowledge-transfer skill into the Codex skills directory for all future
sessions on this machine.
```

After installing the skill, ask Codex to use `knowledge-transfer` when moving
context between Codex, Claude, Pi, Kiro, Gemini, or OpenClaw sessions. The skill
is useful when you want an agent to choose the right `paxl` command instead of
asking you to remember flags.

## Mental Model

`paxl` has three core concepts:

- A **session** is a local agent conversation discovered from local logs.
- A **mirror** is a live handoff from one session into another agent session.
- A **capsule** is reusable context stored locally, then inspected, archived, or
  injected later.

Use `session mirror` when you want continuity now. Use `capsule create` and
`capsule inject` when you want reusable knowledge for later.

Current built-in agents:

- `codex`: local Codex logs plus Codex app-server or CLI delivery.
- `claude`: local Claude Code logs plus Claude Code CLI delivery.
- `pi`: local Pi logs plus Pi CLI delivery.
- `kiro`: local Kiro CLI logs plus Kiro CLI delivery.
- `gemini`: local Gemini CLI logs plus Gemini CLI delivery.
- `openclaw`: OpenClaw ACP session listing and existing-session prompt delivery.
  The default command is `openclaw acp`; set `PAXL_OPENCLAW_ACP_COMMAND` when
  your local OpenClaw ACP entrypoint is different.

## Advanced Workflows

### Install Agent Hooks

Install local hook adapters for supported agents:

```sh
paxl setup
paxl setup --agent claude --format jsonl
```

The current setup command installs Claude Code's `UserPromptSubmit` hook, writes
a Codex `UserPromptSubmit` command hook object into Codex config, installs a Pi
`before_agent_start` extension, and writes paxl-owned hook descriptors for
agents that need descriptor-based hosts. Codex may require trusting changed
hooks before they run.
Conditional local route matching and one-time hook consumption are documented in
[doc/AUTOMATED_INJECTION_ROUTING.md](doc/AUTOMATED_INJECTION_ROUTING.md).

### Preserve a Timeline

Render a session before switching agents:

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format jsonl
paxl session get codex:<session-id> --format html --output session.html
```

Use transcript output for reading, JSONL for scripts, and HTML when you want a
portable review artifact.

### Start Fresh With Context

You do not need an existing target session. Start a new target agent with the
source context:

```sh
paxl session mirror claude:<source-session-id> --to codex
```

This is useful when the target agent should receive the handoff and decide how
to continue from a clean session.

### Create Reusable Knowledge

Ask the source agent to summarize a specific topic into a capsule:

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
paxl capsule get <capsule-id>
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Capsules work well for architecture decisions, debugging history, release
checklists, and project conventions that should survive beyond one conversation.

### Send Knowledge to a Friend

Cloud inbox delivery is gated by accepted friends. You cannot send a capsule to a
raw email address; `--to` must be a friend alias such as `@alice`.

```sh
paxl friend request alice@example.com --alias alice
# after Alice accepts the friend request
paxl friend alias <friend-id> alice
paxl capsule send <capsule-id> --to @alice --message "please review"
paxl outbox list
paxl inbox list
paxl inbox accept <envelope-id>
paxl inbox accept --all
paxl inbox watch
```

The sender can track sent envelopes from outbox while the recipient works from
inbox. Accepting an inbox envelope stores the remote payload as a local capsule.
Use `paxl inbox accept --all` to accept every pending inbox envelope in one
shot, or run `paxl inbox watch` to keep accepting pending envelopes in the
foreground until the process is stopped.
Inject that capsule into a local agent session when you want work to continue
there.

### Transfer Prepared Context

When you already know exactly what should be handed off, create a capsule from a
file instead of asking the source agent to summarize:

```sh
paxl capsule create codex:<session-id> \
  --keyword "production incident" \
  --title "api timeout incident" \
  --summary "Known facts, mitigations, and next checks." \
  --content-file handoff.md
```

If there is no useful source session, create a manual capsule:

```sh
paxl capsule create --manual \
  --keyword "production incident" \
  --content-file handoff.md
```

### Work Through an Agent

Install the bundled Codex skill when you want to say "move this context to
Claude" or "create a capsule for this bug" and let the agent run the concrete
`paxl` commands.

## Data Model

`paxl` is local-first. It reads local agent logs and uses a local SQLite
database for cached metadata, capsules, and injection records.

Use `--db` to choose the SQLite file:

```sh
paxl --db .local/paxl.sqlite session list
```

If `--db` is omitted, `paxl` uses its default local database path.

## Privacy

`paxl` does not require cloud registration to inspect or transfer local sessions.
It reads local agent transcript files, stores cached metadata and generated
capsules in local SQLite, and writes command execution logs locally under
`~/.pax/paxl/logs/`.

Mirror and capsule injection intentionally send selected session context to the
target local agent CLI. Review capsule content with `paxl capsule get <id>` when
you need to inspect what will be delivered.

## Execution Logs

Every `paxl` command writes a JSONL execution log under:

```text
~/.pax/paxl/logs/
```

Logs include command start and finish events, duration, errors, and buffered
adapter diagnostics. Normal command output is unchanged; `--verbose` still
controls whether delivery diagnostics are also printed to stderr.

## Quality Metrics

Statement coverage remains the merge gate:

```sh
make test-cover
```

Branch coverage is available as a non-gating quality metric through
[`gobco`](https://github.com/rillig/gobco):

```sh
make branch-cover-install
make branch-cover
```

The branch coverage report prints per-package missed branches and a total such
as `Branch coverage total: 792/1186 (66.8%)`. Use it to guide test review; it is
not part of CI enforcement.

Mutation testing is available as another non-gating quality signal through
[`go-mutesting`](https://github.com/avito-tech/go-mutesting). The tool is pinned
in `go.mod` as a Go tool dependency, so no separate install step is required:

```sh
make mutation-test
make mutation-test MUTATION_TARGETS=./internal/model/...
make mutation-test MUTATION_TARGETS=./internal/facade MUTATION_TIMEOUT=60
```

The default target is `./internal/model/store`, which exercises non-trivial
persistence behavior without running mutation testing across the whole
repository. The report prints surviving mutations and a mutation score. Use it
when deciding whether a high-coverage area is actually asserting important
behavior.

Cognitive complexity is available through
[`gocognit`](https://github.com/uudashr/gocognit), also pinned as a Go tool
dependency:

```sh
make cognitive-complexity
make cognitive-complexity COGNITIVE_TARGETS=./pkg/adaptor COGNITIVE_TOP=10
```

The default report prints the top 20 production functions and the repository
average. Use it alongside cyclomatic complexity when deciding whether a function
is hard to reason about.

## Release Uploads

`paxl` releases are native Go binaries uploaded to GCS. The release script
defaults to the next patch version, derived from the latest local
`paxl/vX.Y.Z` git tag. If no release tag exists, it starts from the version in
`cmd/paxl/main.go`.

Dry-run the release without uploading or tagging:

```sh
make release-paxl-dry-run
make release-paxl-dry-run RELEASE_VERSION=minor RELEASE_TAGS=beta
```

Upload a stable release:

```sh
make release-paxl
```

The script builds `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and
`linux/arm64`, stamps the binary with version and commit metadata, smoke-tests
the native host binary with `paxl version`, writes sha256 files and a
`manifest.json`, uploads to:

```text
gs://pax-tech-bucket/paxl/releases/<version>/
```

For each release tag, it also updates:

```text
gs://pax-tech-bucket/paxl/releases/latest/<tag>/manifest.json
```

It also uploads the installer to:

```text
gs://pax-tech-bucket/paxl/install.sh
```

After a successful upload it creates a local git tag:

```text
paxl/v<version>
```

Set `PAX_RELEASE_PUSH_TAG=1` to push the tag. Use `RELEASE_VERSION=0.2.0` for an
explicit semantic version, or `RELEASE_VERSION=major|minor|patch` for automatic
incrementing.

## Common Workflows

### List Available Agents

```sh
paxl agent list
```

The `CLI` column shows whether the native agent command is on `PATH`. The
`SESSIONS` column shows whether `paxl` can see local session logs for that agent.

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

Mirror handoffs include `From` and `To` metadata with node, agent, and session
IDs. The node ID comes from `PAXL_NODE_ID` when set, otherwise from the local
hostname.

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

Codex App/Desktop sessions are delivered through `codex app-server` when paxl
can identify them from local rollout metadata. Other Codex sessions, or
app-server failures, fall back to the native CLI resume path.

### Create a Knowledge Capsule

Capsules are reusable handoff artifacts. Unlike `session mirror`, capsule
creation is keyword-driven and asks the source agent to produce a portable
summary by default.

Capsules store their source node, source agent, and source session. Injection
records add the target node, target agent, and target session, and this metadata
is included in JSONL output and delivered handoff text.

Ask the source agent to generate a capsule:

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
```

Use local transcript extraction as an offline fallback. This stores matching raw
transcript lines and does not ask the source agent to summarize:

```sh
paxl capsule create codex:<session-id> --keyword "sqlite schema" --local
```

Create a capsule from prepared content when you need to transfer a precise
operator-written requirement:

```sh
paxl capsule create codex:<session-id> \
  --keyword "installer hosting" \
  --title "paxl installer hosting" \
  --summary "Installer upload and hosting requirement." \
  --content-file capsule.md
```

Create a manual capsule when the content should not be tied to a source
session:

```sh
paxl capsule create --manual \
  --keyword "installer hosting" \
  --content-file capsule.md
```

List and inspect capsules:

```sh
paxl capsule list
paxl capsule get <capsule-id>
```

Inject a capsule into a target session:

```sh
paxl capsule inject <capsule-id> codex:<target-session-id>
paxl capsule inject <capsule-id> codex:<target-session-id> \
  --action-items "run go test ./..." \
  --action-items "open a PR"
```

Capsule handoffs are knowledge-only by default. Repeat `--action-items` to pass
explicit actionable work to the target agent. Action items are concrete next
steps such as planning, editing files, running tools, or otherwise continuing
from the capsule. They can be used with direct injection or queued `--match`
hook injection.

Queue a capsule for the next matching pre-user hook:

```sh
paxl capsule inject <capsule-id> --match any
paxl capsule inject <capsule-id> --match project --project paxl --agent claude
paxl capsule inject <capsule-id> --match keyword --keyword "release plan"
```

Start a new target agent session with a capsule:

```sh
paxl capsule inject <capsule-id> --new --agent codex
```

Archive a capsule:

```sh
paxl capsule archive <capsule-id>
```

Send a capsule to an accepted friend:

```sh
paxl friend request alice@example.com --alias alice
# after Alice accepts the friend request
paxl capsule send <capsule-id> --to @alice
paxl capsule send <capsule-id> --to @alice --match project --project pax-manager --agent codex
paxl capsule send <capsule-id> --to @alice --match keyword --keyword "capsule routing"
```

`capsule send` requires an accepted friend alias. The manager also enforces this
boundary, so direct email delivery is rejected even if a client bypasses the CLI.
Conditional sends store a route in the envelope. The recipient chooses when to
accept it, and the target session is selected later by the local agent hook.

Read received envelopes:

```sh
paxl inbox list
paxl inbox get <envelope-id>
paxl inbox accept <envelope-id>
paxl inbox archive <envelope-id>
```

Track sent envelopes:

```sh
paxl outbox list
paxl outbox list --status accepted
paxl outbox get <envelope-id>
```

Manage friends:

```sh
paxl friend list
paxl friend accept <friend-id> --alias alice
paxl friend alias <friend-id> alice
paxl friend remove <friend-id>
paxl friend block <friend-id>
```

## Agent Delivery Semantics

Codex delivery:

- Codex App/Desktop existing session: `codex app-server` `thread/resume`, then
  `turn/steer` when an active turn is steerable, otherwise `turn/start`.
- Other existing sessions or app-server fallback:
  `codex exec resume --all <session-id> -`
- New session: `codex exec -`
- Conditional hook injection: Codex `UserPromptSubmit` hook JSON with
  `hookSpecificOutput.additionalContext` before the current user prompt.

Claude delivery:

- Existing session: `claude --print --resume <session-id>`
- New session: `claude --print`

Pi delivery:

- Existing session: `pi --session <session-id> -p`
- New session: `pi -p`
- Conditional hook injection: Pi `before_agent_start` extension message before
  the agent loop starts.

Kiro delivery:

- Existing session: `kiro-cli chat --resume-id <session-id> --no-interactive <message>`
- New session: `kiro-cli chat --no-interactive <message>`

Gemini delivery:

- Existing session: `gemini --resume <session-id> -p <message>`
- New session: `gemini -p <message>`

OpenClaw delivery:

- Existing session: ACP `session/prompt` through `openclaw acp`
- Session listing: ACP `session/list`
- Override command: `PAXL_OPENCLAW_ACP_COMMAND='openclaw --acp'`

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
adapters. Codex, Claude, Pi, Kiro, Gemini, and OpenClaw are built in today.

## Platform Support

The CLI architecture and SQLite storage are cross-platform Go code, but the
built-in adapters depend on local agent log locations and native CLIs.

Current support boundary:

- macOS: verified with local Codex, Claude Code, Pi, Kiro CLI, and Gemini CLI
  log shapes. OpenClaw is covered through ACP contract tests and requires a
  local OpenClaw ACP command.
- Linux: expected to be close to macOS if `~/.codex/sessions`,
  `~/.claude/projects`, `~/.pi/agent/sessions`, `~/.kiro/sessions`,
  `~/.gemini/tmp`, and the matching CLIs are available, but it still needs
  real-world validation.
- Windows: not fully validated. Path handling, Claude project directory
  decoding, fake-command tests, and native CLI resume behavior need dedicated
  Windows coverage.

In short: macOS is verified, Linux is expected to work with similar local agent
layouts, and Windows should be treated as experimental until tested.

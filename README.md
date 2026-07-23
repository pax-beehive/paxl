# paxl

**Local-first context transfer for AI coding agents.**

`paxl` lets Codex, Claude Code, Pi, Kiro, OpenCode, Kimi Code, Hermes, and
OpenClaw hand work to each other without copy-pasting transcripts or uploading
your session history.

When one agent is out of quota, stuck, or simply the wrong tool for the next
step, `paxl` can preserve the working context and deliver it to another local
agent session.

## Install

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
paxl version
```

Build from source:

```sh
go build -trimpath -o ./paxl ./cmd/paxl
```

Useful links:

- Chinese docs: [doc/README_cn.md](doc/README_cn.md)
- Architecture: [doc/ARCHITECTURE.md](doc/ARCHITECTURE.md)
- Hook routing: [doc/AUTOMATED_INJECTION_ROUTING.md](doc/AUTOMATED_INJECTION_ROUTING.md)

## The First Five Minutes

See what `paxl` can reach:

```sh
paxl agent list
```

Find the session you want to move:

```sh
paxl session list --agent claude --limit 5
paxl session list --agent codex --limit 5
```

Resume one directly in its native interactive CLI:

```sh
paxl resume codex:<session-id>
paxl resume opencode:<session-id>
```

Move the Claude session into an existing Codex session:

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to-session codex:<target-session-id> \
  --verbose
```

Or start a clean target session with the same working context:

```sh
paxl session mirror claude:<source-session-id> --to codex
```

## What It Moves

`paxl` works with four local-first objects:

| Object | Use it when | Example |
| --- | --- | --- |
| Session | You need to inspect a local conversation. | `paxl session get claude:<id>` |
| Mirror | You want another agent to continue now. | `paxl session mirror claude:<id> --to codex` |
| Capsule | You want reusable knowledge for later. | `paxl capsule create codex:<id> --keyword "release plan"` |
| Envelope | You want to send a capsule to an accepted friend. | `paxl capsule send <id> --to @alice` |

The important boundary: local inspection stays local. Context is delivered only
when you explicitly mirror, inject, or send it.

## Supported Agents

| Agent | Local sessions | Delivery | Hook setup |
| --- | --- | --- | --- |
| Codex | Local logs | App server or `codex exec` | `UserPromptSubmit` |
| Claude Code | Local logs | `claude --print` | `UserPromptSubmit` |
| Pi | Local logs | Pi CLI | `before_agent_start` extension |
| Kiro | Kiro CLI logs | `kiro-cli chat` | Kiro `userPromptSubmit` agent hook |
| OpenCode | Local SQLite | `opencode run` | Global OpenCode plugin |
| Kimi Code | Local session index and wire log | `kimi --session` | `UserPromptSubmit` and `Stop` hooks |
| Hermes | Local state, ACP, or HTTP | ACP or Hermes HTTP | Native Hermes hooks |
| OpenClaw | ACP | ACP `session/prompt` | Descriptor only |

Run `paxl agent list` to see which of these are available on your machine.

Gemini CLI support has been retired. Legacy `gemini` values can still be parsed
from old local data, but new CLI entrypoints reject Gemini as unsupported.

## Hook-Based Injection

Install local hooks once:

```sh
paxl setup
```

Then queue a capsule for the next matching prompt:

```sh
paxl capsule inject <capsule-id> --match keyword --keyword "release plan"
paxl capsule inject <capsule-id> --match project --project paxl --agent claude
```

The hidden hook entrypoint claims each matching injection once, renders the
capsule as a handoff, and passes it back through the agent's native hook shape.

## Common Moves

Preserve a timeline before switching agents:

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format jsonl
paxl session get codex:<session-id> --format html --output session.html
```

Use transcript output for reading, JSONL for scripts, and HTML when you want a
portable review artifact.

Create reusable knowledge:

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
paxl capsule get <capsule-id>
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Capsules work well for architecture decisions, debugging history, release
checklists, and project conventions that should survive beyond one conversation.

Send knowledge to an accepted friend:

```sh
paxl friend request alice@example.com --alias alice
# after Alice accepts the friend request
paxl friend alias <friend-id> alice
paxl capsule send <capsule-id> --to @alice --message "please review"
```

Transfer prepared context:

```sh
paxl capsule create --manual \
  --keyword "production incident" \
  --content "Prepared incident context..."
```

Install the bundled Codex skills when you want an agent to run the concrete
`paxl` commands:

```sh
mkdir -p ~/.codex/skills
cp -R skills/knowledge-transfer ~/.codex/skills/
cp -R skills/session-search ~/.codex/skills/
cp -R skills/session-condense ~/.codex/skills/
cp -R skills/wiki-recall ~/.codex/skills/
```

- `knowledge-transfer`: move context, create capsules, inject capsules, or
  mirror sessions.
- `session-search`: search local session history and optionally record reusable
  query trails into the qmd wiki.
- `session-condense`: maintain the local memex by extracting durable decisions,
  constraints, facts, failures, commands, artifacts, and open questions from
  changed paxl sessions and `.llm-wiki/recalls/`, then updating qmd wiki pages,
  backlinks, `memex.graph.json`, recall-index, inbox, lint, and visualization
  artifacts.
- `wiki-recall`: recall answers from the qmd LLM wiki, `memex.graph.json`,
  recall-index, backlinks, and query trails, then write recall traces so the
  memex improves from later queries before falling back to raw session search.

Preview the maintained memex locally:

```sh
paxl memex render --html --port 8787
```

The command reads `wiki/` and `.llm-wiki/` from the current project by default.
Use `--wiki-root` to point at another project root or directly at a `wiki/`
directory.

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

After upload, the script publishes the same artifact metadata to pax-manager and
verifies the public resolver for each platform:

```text
https://api.paxtech.net/api/v1/public/artifacts/download?product=paxl&platform=<platform>&tags=<tag>
```

This resolver publish step is required for `paxl update` and the installer flow
to see the new version. Set `PAX_RELEASE_SKIP_METADATA=1` only when you
intentionally want a GCS-only upload.

After a successful upload it creates a local git tag:

```text
paxl/v<version>
```

Set `PAX_RELEASE_PUSH_TAG=1` to push the tag. Use `RELEASE_VERSION=0.2.0` for an
explicit semantic version, or `RELEASE_VERSION=major|minor|patch` for automatic
incrementing.

## Command Reference

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

Only show sessions whose project is the current directory:

```sh
paxl session list --local
paxl session list --local --agent claude
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
  --content "The installer should be uploaded and hosted at GCS."
```

Create a manual capsule when the content should not be tied to a source
session:

```sh
paxl capsule create --manual \
  --keyword "installer hosting" < capsule.md
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

### Team Memory On-Prem Channel

The default envelope channel remains PAX Manager. A single-Team Team Memory
installation can be connected as an independent credential-bound channel; its
credential and Agent identity are stored separately from manager login state.
Prefer an environment variable so the one-time enrollment token is not written
to shell history:

```sh
read -rs PAXL_ENROLLMENT_TOKEN
export PAXL_ENROLLMENT_TOKEN
paxl channel connect onprem
unset PAXL_ENROLLMENT_TOKEN

paxl channel list
paxl channel status onprem
paxl channel agents list --channel onprem --query receiver
paxl channel agents get receiver-agent --channel onprem
```

Current self-describing enrollment tokens include the deployment origin, so
`--url` is optional. Continue to pass `--url https://memory.internal` for a
legacy two-part token or to explicitly confirm a profile origin change.

For a workstation CA, add its PEM certificate to the profile with
`--ca-file /path/to/team-memory-ca.pem`. System roots remain trusted. There is
no persisted insecure TLS mode. Plain HTTP is limited to loopback origins and
Tailscale IP literals in `100.64.0.0/10` or `fd7a:115c:a1e0::/48`; other remote
origins require HTTPS.

On-prem delivery is Agent-to-Agent and therefore uses an Agent id, not a friend
alias or email:

```sh
paxl capsule send <capsule-id> --channel onprem \
  --to-agent-id receiver-agent --match project --project paxl --agent codex
paxl inbox list --channel onprem
paxl inbox get <envelope-id> --channel onprem
paxl inbox accept <envelope-id> --channel onprem
paxl inbox archive <envelope-id> --channel onprem
paxl outbox list --channel onprem --status archived
```

Each enabled auto-receive profile is polled independently by the user-prompt
hook. A failed channel is diagnosed without blocking another channel or an
already queued local injection. See [Team Memory on-prem channels](doc/ONPREM_CHANNEL.md)
for trust, recovery, migration, and E2E details.

Manage friends:

```sh
paxl friend list
paxl friend accept <friend-id> --alias alice
paxl friend alias <friend-id> alice
paxl friend remove <friend-id>
paxl friend block <friend-id>
```

### List Teams and Team Agents (Read-Only)

`paxl team` reads the team graph from PAX Manager so the local node knows which
teammate agents it could deliver to. These commands are read-only.

List the teams you belong to:

```sh
paxl team list
```

Show one team:

```sh
paxl team get <team-id>
```

List a team's agents:

```sh
paxl team agents <team-id>
```

Aggregate delivery-candidate agents across all your teams. Agents you own are
excluded by default; each agent shows which teams it belongs to:

```sh
paxl team agents --all
paxl team agents --all --include-self
paxl team agents --all --online
paxl team agents --all --agent <agent-id>
```

All `team` commands support `--format table|jsonl`.

## Agent Delivery Semantics

Resume a known paxl session in the foreground with `paxl resume
<agent:session-id>`. The command connects the current terminal directly to the
agent's native interactive CLI:

| Agent | Interactive resume command |
| --- | --- |
| Codex | `codex resume <session-id>` |
| Claude Code | `claude --resume <session-id>` |
| Pi | `pi --session <session-id>` |
| Kiro | `kiro-cli chat --resume-id <session-id>` |
| OpenCode | `opencode --session <session-id>` |
| Kimi Code | `kimi --session <session-id>` |
| Hermes | `hermes --resume <session-id>` |

OpenClaw does not expose a native interactive resume CLI, so `paxl resume`
returns an unsupported-operation error for OpenClaw sessions.

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

OpenCode delivery:

- Existing session: `opencode run --session <session-id> <message>`
- New session: `opencode run <message>`
- Session discovery and timelines: local OpenCode SQLite data.
- Conditional hook injection: global `plugins/paxl.ts` OpenCode plugin.

Kimi Code delivery:

- Existing session: `kimi --session <session-id> --prompt <message>`
- New session: `kimi --prompt <message>`
- Session discovery and timelines: `$KIMI_CODE_HOME/session_index.jsonl` and
  the main-agent `wire.jsonl` stream.
- Conditional hook injection: managed `UserPromptSubmit` and `Stop` entries in
  `$KIMI_CODE_HOME/config.toml`.

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
adapters. Codex, Claude, Pi, Kiro, OpenCode, Kimi Code, Hermes, and OpenClaw are
built in today.

## Platform Support

The CLI architecture and SQLite storage are cross-platform Go code, but the
built-in adapters depend on local agent log locations and native CLIs.

Current support boundary:

- macOS: verified with local Codex, Claude Code, Pi, Kiro, OpenCode, Kimi Code,
  and Hermes session shapes. OpenClaw is covered through ACP contract tests and
  requires a local OpenClaw ACP command.
- Linux: expected to be close to macOS if `~/.codex/sessions`,
  `~/.claude/projects`, `~/.pi/agent/sessions`, `~/.kiro/sessions`, OpenCode's
  local data directory, `~/.kimi-code/sessions`, and the matching CLIs are
  available, but it still needs real-world validation.
- Windows: not fully validated. Path handling, Claude project directory
  decoding, fake-command tests, and native CLI resume behavior need dedicated
  Windows coverage.

In short: macOS is verified, Linux is expected to work with similar local agent
layouts, and Windows should be treated as experimental until tested.

## Accepted Inbox Sync

If an envelope is accepted outside the local CLI, for example through the
manager API or a hosted UI, the remote inbox status can become `accepted` before
this machine has stored the capsule locally. Use explicit inbox sync to repair
that local state:

```sh
paxl inbox sync
paxl inbox sync --limit 20
```

`inbox sync` lists accepted inbox envelopes, materializes any missing local
capsules, and recreates routed hook injections when the envelope payload
contains a route. It is idempotent: accepted envelopes are keyed locally by the
source session id `remote_envelope:<envelope-id>` for manager or
`remote_envelope:onprem:<profile-id>:<envelope-id>` for on-prem, so repeated
syncs reuse the existing local capsule and route injection without collisions
between installations.

`paxl inbox accept <envelope-id>` is also idempotent. If the remote envelope is
already accepted, it skips the remote accept call and performs the same local
materialization step.

The hidden agent hook performs the same reconciliation before matching routes:
it first accepts pending envelopes, then syncs a small batch of recently
accepted envelopes. This lets capsules accepted from the web or manager API
arrive on the next matching local prompt without a manual CLI accept step.

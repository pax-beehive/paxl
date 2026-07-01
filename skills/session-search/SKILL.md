---
name: session-search
description: Search, inspect, recall, remember, retrieve, or look back through local paxl session history, and optionally record useful query trails into a local qmd LLM wiki. Use when the user asks to find prior Codex, Claude, Pi, Kiro, Gemini, Hermes, or agent sessions, search local transcripts, recover earlier context, locate a session by keyword, run paxl session query, list, or get previous session context, or make a reusable memex trail from a query without transferring knowledge.
---

# Session Search

Use `paxl` to search local agent session history. This skill is read-only unless
the user asks for a follow-up action. For knowledge handoff, use
`knowledge-transfer` instead.

When the query uncovers reusable context, record a compact memex trail in the
local qmd wiki. The trail should preserve the question, commands, evidence, and
result so future agents benefit from the search path. Record a rationale
summary, not hidden chain-of-thought.

## Quick Checks

Prefer the installed `paxl` on `PATH`:

```sh
paxl version
paxl agent list
```

If `paxl` cannot open its default database in this environment, retry with a
known writable database path using the global `--db` flag.

## Search

Start with cached search. It returns current SQLite results quickly and may
start a background refresh for later queries:

```sh
paxl session query "keyword or phrase"
paxl session query "keyword or phrase" --limit 20
paxl session query "keyword or phrase" --format jsonl
```

For a pure cached lookup with no background refresh side effect:

```sh
paxl session query "keyword or phrase" --no-background-sync
```

If fresh local logs may contain the missing result, run a bounded foreground
sync:

```sh
paxl session query "keyword or phrase" --sync
paxl session query "keyword or phrase" --sync --timeout 10s
```

Filter by agent when the likely source is known:

```sh
paxl session query "keyword or phrase" --agent codex --format jsonl
paxl session query "keyword or phrase" --agent claude --format jsonl
```

## Session Discovery

List recent sessions:

```sh
paxl session list --agent codex --limit 20
paxl session list --agent claude --limit 20
paxl session list --agent kiro --limit 20
```

Use JSONL when another tool or agent will consume the result:

```sh
paxl session list --agent codex --limit 20 --format jsonl
```

Use cached metadata only when avoiding a local log scan is important:

```sh
paxl session list --agent codex --no-sync --format jsonl
```

Typed session IDs look like:

```text
codex:native-id
claude:native-id
pi:native-id
kiro:native-id
gemini:native-id
hermes:native-id
```

If the user gives a bare native ID, pass `--agent AGENT` or convert it to a
typed ID before using `session get`.

## Inspect

Render a readable transcript:

```sh
paxl session get codex:SESSION_ID
```

Use JSONL for structured inspection:

```sh
paxl session get codex:SESSION_ID --format jsonl
```

The first JSONL record is `paxl.session.snapshot.v1`; following records are
`paxl.session.element.v1`. Use `currentSyncVersion`, `seq`, `role`,
`startedAt`, `completedAt`, and `contentText` to cite or summarize what was
found.

## Reporting Results

- Report the session ID, agent, title, updated time, and the specific matching
  snippets or element roles.
- Distinguish cached results from results found after `--sync`.
- Do not create capsules, inject context, mirror sessions, or update wiki files
  unless the user explicitly asks for that next step or asks to record a memex
  trail.

## Query Trail Logging

If the user asks to record the query, maintain the local LLM wiki, or make the
result reusable for other agents, write a qmd page under the existing wiki. Find
the wiki root by checking the user's path first, then `wiki/`, `docs/wiki/`, and
directories containing `.qmd` files.

Prefer:

```text
wiki/
  index.qmd
  log.qmd
  trails/
```

Create `wiki/trails/YYYY-MM-DD-short-slug.qmd` when no local convention exists.
Use this template:

```markdown
---
type: query-trail
query: "original user question or search phrase"
updated_at: "2026-07-01T10:05:00Z"
tags: [session-search]
---

# Query Trail: Short title

## Question

One sentence stating the question.

## Search Trail

- Ran `paxl session query "..."`
- Inspected `agent:session-id` with `paxl session get ... --format jsonl`
- Read or updated `wiki/...qmd`

## Rationale Summary

Concise explanation of why these results answer the question. Do not include
hidden chain-of-thought or private deliberation.

## Findings

- Durable fact, decision, command, caveat, or contradiction.

## Reusable Result

Short answer future agents should reuse.
```

After writing a trail:

- Append one line to `wiki/log.qmd` when it exists, using a parseable date prefix
  such as `## [2026-07-01] query | Short title`.
- Add or update an `index.qmd` entry when the trail is broadly useful.
- Keep raw transcript excerpts short. Prefer session IDs, element roles, and
  concise summaries over copying long session text.

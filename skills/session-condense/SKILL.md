---
name: session-condense
description: Maintain a local qmd LLM wiki from paxl session history using a Karpathy-style LLM wiki and memex trail pattern. Use when the user asks for session_condense, session-condense, knowledge-condense, continuous LLM wiki maintenance, qmd wiki updates from local agent sessions, full wiki condensation, or keeping durable docs updated from Codex, Claude, Pi, Kiro, Gemini, Hermes, query trails, or other paxl sessions.
---

# Session Condense

Use `paxl session list` and `paxl session get` to feed changed session history
into a local qmd wiki. The wiki is maintained by editing files directly with
normal filesystem tools. Do not add paxl wiki commands or treat knowledge
capsules as the wiki source.

Default to a maintenance pass, not a query-only trail. If the user asks to
condense, maintain, update, or refresh the wiki, process candidate sessions with
`paxl session list` and `paxl session get`, update durable concept or decision
pages, and advance `processedSessions` in state. A query trail by itself is only
acceptable when the user asks to record one search path or explicitly scopes the
run to a single question.

Follow the LLM wiki pattern described by Andrej Karpathy's gist:
`https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f`.
Raw sources stay immutable, the generated wiki is the durable synthesis layer,
and query results that discover useful connections should be filed back into the
wiki so future agents do not rediscover the same path from scratch.

## Wiki Shape

Use the repo's existing qmd conventions when present. Otherwise prefer:

```text
wiki/
  index.qmd
  log.qmd
  concepts/
  decisions/
  trails/
.llm-wiki/
  session-condense-state.json
  runs/
```

- `index.qmd` is content-oriented: page catalog, one-line summaries, and major
  entry points for agents.
- `log.qmd` is chronological and append-only: ingests, query trails, lint passes,
  and wiki maintenance runs.
- `trails/` stores reusable query artifacts produced by `session-search` or by
  this skill when a session-maintenance pass answers an implicit question.
- `.llm-wiki/` stores process state and run provenance. Keep it out of the
  reader-facing wiki unless the user asks for operational details.

## Inputs

Prefer the installed `paxl` on `PATH`.

List candidate sessions:

```sh
paxl session list --agent codex --limit 20 --format jsonl
paxl session list --agent claude --limit 20 --format jsonl
```

Use cached metadata when deciding what was already processed:

```sh
paxl session list --agent codex --no-sync --format jsonl
```

Fetch the stable session snapshot:

```sh
paxl session get codex:SESSION_ID --format jsonl
```

`session get --format jsonl` starts with `paxl.session.snapshot.v1`, followed by
`paxl.session.element.v1` records. Use `currentSyncVersion` from the snapshot as
the processed-session cursor. Element records include `syncVersion` so the input
can be tied to the snapshot that produced it.

## State

Store processing state outside the reader-facing wiki:

```text
.llm-wiki/
  session-condense-state.json
  runs/
wiki/
  index.qmd
```

Use this state shape unless the repo already has a local convention:

```json
{
  "sources": {
    "codex:/absolute/project/path": {
      "processedSessions": {
        "codex:session-id": {
          "currentSyncVersion": 123,
          "updatedAt": "2026-07-01T10:00:00Z",
          "processedAt": "2026-07-01T10:05:00Z"
        }
      }
    }
  }
}
```

Write a run log under `.llm-wiki/runs/` only for maintenance provenance. Do not
copy raw session transcripts into the public qmd wiki.

## Workflow

Use this workflow for normal `session-condense` / `knowledge-condense` requests.
Do not stop after writing only `wiki/trails/*.qmd` unless the user explicitly
asked for a query trail.

1. Find the wiki root. Prefer the user's explicit path; otherwise look for
   `.qmd` files, `wiki/`, `docs/wiki/`, or a project-local `.llm-wiki/`.
2. Read `.llm-wiki/session-condense-state.json` if it exists.
3. List recent sessions with `paxl session list --format jsonl`.
4. Skip sessions whose stored `currentSyncVersion` equals the listed
   `currentSyncVersion`.
5. For each changed session, run `paxl session get SESSION --format jsonl`.
6. Read existing qmd files with `rg`, `grep`, `find`, and normal file reads.
   Read `index.qmd` first when it exists, then relevant topic pages and recent
   `log.qmd` entries.
7. Include useful `wiki/trails/*.qmd` pages as sources. Query trails are part of
   the memex: they capture why a path was investigated and what it found.
8. Extract only durable knowledge: decisions, architecture notes, file paths,
   commands that mattered, interface contracts, caveats, and unresolved
   questions.
9. Update qmd files by merging into existing sections. Remove stale or duplicate
   claims when the session clearly supersedes them.
10. Update `index.qmd` for new or materially changed pages.
11. Append one compact entry to `log.qmd` describing the ingest or maintenance
    run.
12. Update the state file only after the qmd edits are complete.

## Maintenance Completion Rules

A maintenance pass is incomplete unless one of these is true:

- At least one changed session was fetched with `paxl session get`, durable
  knowledge was merged into reader-facing qmd pages, and
  `.llm-wiki/session-condense-state.json` records the processed session cursor.
- Recent candidate sessions were listed, all were already processed at the same
  `currentSyncVersion`, and `log.qmd` records a no-op maintenance run.
- Session listing or fetching failed; the final answer states the exact command
  and error, and no state cursor is advanced.

When a session contains durable project rules, architecture decisions, interface
contracts, validation recipes, or recurring caveats, create or update a focused
page under `wiki/concepts/` or `wiki/decisions/`. Do not leave that knowledge
only in `wiki/trails/`.

## Query Trail Pages

When a query trail deserves to become reusable knowledge, store it under
`wiki/trails/YYYY-MM-DD-short-slug.qmd` or the closest existing wiki convention.

Use this shape:

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

- Ran `paxl session query ...`
- Inspected `codex:...` with `paxl session get ...`
- Read `wiki/...qmd`

## Rationale Summary

A concise explanation of why the selected evidence answers the question. Do not
record hidden chain-of-thought or private reasoning traces.

## Findings

- Stable finding with link or session reference when useful.
- Contradiction, gap, or follow-up question.

## Reusable Result

The short answer future agents should reuse.
```

## Editing Rules

- Keep the wiki reader-facing. Do not expose session IDs, raw transcripts, or
  run provenance in normal qmd pages unless the user explicitly asks. Query
  trail pages may include session IDs when they are useful evidence pointers,
  but keep raw transcript text brief and quoted only when necessary.
- Prefer `updated_at` or a short "Last updated" field over session provenance.
- Preserve the repo's existing qmd frontmatter, headings, cross-links, and style.
- Do not append endless summaries. Integrate new knowledge into the right page.
- If there is no good destination page, create one focused qmd page and link it
  from `index.qmd` when an index exists.
- If a session is noisy or task-specific with no durable knowledge, record it as
  processed in state and do not change the wiki.
- Treat the wiki as a maintained codebase: keep links current, remove stale
  statements, prefer small focused pages, and leave associative trails between
  related concepts.

## Failure Handling

- If the default paxl database cannot be opened, locate the project/user paxl DB
  or ask for the right `--db` path before diagnosing wiki logic.
- If `paxl session list` misses fresh content, run a foreground sync by listing
  the relevant agent without `--no-sync`, then retry.
- If qmd edits cannot be made safely because the wiki shape is unclear, stop
  after gathering candidate session facts and ask for the wiki root.

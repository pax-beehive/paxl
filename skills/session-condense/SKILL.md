---
name: session-condense
description: Maintain a local qmd LLM wiki from paxl session history using a Karpathy-style LLM wiki and memex trail pattern. Use when the user asks for session_condense, session-condense, knowledge-condense, continuous LLM wiki maintenance, qmd wiki updates from local agent sessions, full wiki condensation, or keeping durable docs updated from Codex, Claude, Pi, Kiro, OpenCode, Kimi Code, Gemini, Hermes, query trails, or other paxl sessions.
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
  memex.graph.json
  concepts/
  decisions/
  failures/
  trails/
  _indexes/
.llm-wiki/
  session-condense-state.json
  recall-index.json
  inbox.json
  memex-lint.json
  memex-visualization.json
  recalls/
  runs/
```

- `index.qmd` is content-oriented: page catalog, one-line summaries, and major
  entry points for agents.
- `log.qmd` is chronological and append-only: ingests, query trails, lint passes,
  and wiki maintenance runs.
- `trails/` stores reusable query artifacts produced by `session-search` or by
  this skill when a session-maintenance pass answers an implicit question.
- `_indexes/` stores generated qmd indexes for agents that can only use `rg`,
  `grep`, and normal file reads. These files materialize page metadata,
  aliases, query trails, backlinks, reasoning paths, and maintenance gaps.
- `memex.graph.json` stores a lightweight graph of reader-facing wiki pages and
  knowledge-unit links. It is publishable and must not contain raw transcripts or
  hidden run provenance.
- `.llm-wiki/recalls/` stores private recall traces produced by `wiki-recall`.
  Use them as demand signals for what the memex should promote, merge, or fix.
- `.llm-wiki/recall-index.json` aggregates trace usage counts for nodes, edges,
  trails, sources, fallback queries, and maintenance signals.
- `.llm-wiki/inbox.json` queues weak answers and maintenance signals for future
  promotion or explicit no-op decisions.
- `.llm-wiki/memex-lint.json` records graph, backlink, and page-coverage
  findings.
- `.llm-wiki/memex-visualization.json`, `wiki/memex.graph.mmd`, and
  `wiki/memex.graph.svg` visualize the graph with usage and issue signals.
- `.llm-wiki/` stores process state, recall traces, and run provenance. Keep it
  out of the reader-facing wiki unless the user asks for operational details.

## Memex Extraction Contract

Treat `session-condense` as the local memex maintainer. A maintenance pass should
extract stable knowledge units before editing pages. Prefer these types:

- `decision`: A decision, its rationale, and any meaningful rejected
  alternatives.
- `constraint`: A user preference, project boundary, compatibility requirement,
  or architectural rule that should guide future work.
- `fact`: Current behavior verified from code, commands, docs, or runtime
  output.
- `failure`: A recurring error, failure mode, root cause, and concrete recovery
  path.
- `command`: A command that was useful, including cwd, required environment, and
  what it validated.
- `artifact`: A durable output such as a PR, release, capsule, document, wiki
  page, or important file path.
- `open_question`: An unresolved question or risk that should be revisited.

Do not publish raw session summaries as knowledge units. If a session mostly
contains task chatter, mark it processed in state after confirming there is no
durable knowledge to merge.

When creating or updating a knowledge unit in qmd, use the repo's existing page
style when present. Otherwise use this frontmatter shape:

```yaml
---
type: decision
id: decision-paxl-hook-routing
title: "Paxl hook routing stays hidden behind setup"
updated_at: "2026-07-02T10:05:00Z"
status: active
topics: [paxl, hook-routing]
links:
  - "[[knowledge-capsules]]"
  - "[[session-handoff]]"
---
```

The `id` must be stable and page-local or topic-local. Prefer readable slugs over
opaque session ids. Store exact session ids, transcript offsets, and run-specific
evidence under `.llm-wiki/runs/`, not in normal wiki pages, unless the user asks
for source provenance in public docs.

## Links And Graph

Maintain bidirectional navigation as part of every meaningful edit:

- Add explicit `[[wikilinks]]` from the edited page to related concepts,
  decisions, failures, commands, and trails.
- Add or update a compact `## Related` section when the relationship is useful
  to a future agent.
- If a new page becomes a primary entry point, link it from `index.qmd`.
- If a claim supersedes older guidance, mark the older page or section with
  `status: superseded` or a short "Superseded by" note instead of leaving both
  claims as active.

When page links materially change, update `wiki/memex.graph.json` unless the repo
has a stronger graph convention. Use this publishable shape:

```json
{
  "schemaVersion": "paxl.memex.graph.v1",
  "updatedAt": "2026-07-02T10:05:00Z",
  "nodes": [
    {
      "id": "decision-paxl-hook-routing",
      "type": "decision",
      "path": "wiki/decisions/paxl-hook-routing.qmd",
      "title": "Paxl hook routing stays hidden behind setup",
      "summary": "Hook plumbing is installed by setup and kept out of the public command surface.",
      "status": "active",
      "topics": ["paxl", "hook-routing"]
    }
  ],
  "edges": [
    {
      "source": "decision-paxl-hook-routing",
      "target": "concept-knowledge-capsules",
      "type": "relates_to"
    }
  ]
}
```

Keep graph nodes reader-facing. Do not include raw session ids or transcript
fragments. Useful edge types are `relates_to`, `supports`, `depends_on`,
`supersedes`, `uses`, and `mentions`. Add Mermaid diagrams to qmd pages only
when they make a workflow or dependency easier to inspect; the graph JSON is the
canonical machine-readable visualization input.

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
  recall-index.json
  inbox.json
  memex-lint.json
  recalls/
  runs/
wiki/
  index.qmd
  _indexes/
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

## Memex Tools

Use the bundled `wiki-recall` tool for deterministic maintenance helpers:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py refresh --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py aggregate --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py lint --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py inbox --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py visualize --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py indexes --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py mark-processed --wiki-root . \
  --trace .llm-wiki/recalls/TRACE.json \
  --outcome "promoted to durable qmd page" \
  --output-path wiki/concepts/example.qmd
```

`refresh` runs aggregate, lint, inbox, qmd index generation, and visualization
in order. Run `indexes` alone when only the generated qmd retrieval surface needs
to be refreshed.

## Recall Trace Consumption

Read `.llm-wiki/recalls/*.json` when present. These files record which wiki
nodes, edges, trails, and sources were reused by `wiki-recall`:

```json
{
  "schemaVersion": "paxl.memex.recall-trace.v1",
  "createdAt": "2026-07-02T10:05:00Z",
  "question": "original user question",
  "answerSummary": "short reusable result",
  "usedNodes": ["concept-session-condense-local-memex"],
  "usedEdges": [
    {
      "source": "decision-keep-local-memex-in-session-condense",
      "type": "depends_on",
      "target": "concept-session-condense-local-memex"
    }
  ],
  "usedTrails": ["wiki/trails/2026-07-01-omniagent-harness-flattening.qmd"],
  "answerSources": ["wiki/concepts/session-condense-local-memex.qmd#Reader-Facing-Shape"],
  "fallbackSessionSearch": false,
  "maintenanceSignals": ["Create a durable concept when this answer repeats."]
}
```

Use recall traces to make the memex improve with use:

- Promote repeated or high-value `answerSummary` content into concept, decision,
  failure, command, or trail pages.
- Strengthen `wiki/memex.graph.json` when traces repeatedly traverse the same
  node or edge path.
- Fix stale pages, missing backlinks, weak summaries, or graph drift mentioned
  in `maintenanceSignals`.
- Create or update query trails when `fallbackSessionSearch` was required and
  the result will likely be useful again.
- Use `.llm-wiki/recall-index.json` to identify hot nodes, hot edges, frequently
  cited sources, repeated fallback questions, and common maintenance signals.
- Use `wiki/_indexes/reasoning-paths.qmd` as the grep-native form of recall
  trace consumption. It should expose prior questions, reused trails, used
  nodes, traversed edges, answer sources, and summaries without requiring future
  agents to parse JSON.
- Use `wiki/_indexes/backlinks.qmd` and `wiki/_indexes/all.qmd` to make
  relationship traversal, aliases, tags, summaries, and page paths searchable
  with normal text tools.
- Use `.llm-wiki/inbox.json` as the weak-answer queue. Each item must be
  promoted, fixed, merged, or explicitly recorded as no-op in a run log.
- Record processed recall trace paths and content hashes in
  `.llm-wiki/session-condense-state.json` with `mark-processed` so maintenance
  does not repeatedly process the same trace.

## Memex Lint

Run lint after graph or backlink edits:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py lint --wiki-root .
```

Fix `error` findings before treating maintenance as complete. Warnings should
either be fixed or named in the run log as intentional. The lint checks include:

- graph nodes pointing to missing qmd paths;
- graph edges pointing to missing nodes;
- qmd pages missing graph nodes;
- qmd wikilinks missing graph edges;
- graph edges not reflected in source pages;
- orphan graph nodes.

## Weak-Answer Inbox

Build the inbox from recall traces:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py inbox --wiki-root .
```

`fallbackSessionSearch=true` becomes a high-priority weak-answer item.
`maintenanceSignals` become maintenance items. Do not leave the inbox as a
permanent TODO dump; use it to drive the next concrete qmd, graph, or trail edit.
After resolving an item, run `mark-processed` for the source trace and rebuild
the inbox.

## Visualization

Generate visualization artifacts after recall-index or graph changes:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py visualize --wiki-root .
```

The generated SVG uses node radius for recall count, edge stroke width for
traversal count, and red nodes for lint issues. The Mermaid file gives a compact
text-reviewable graph.

Inspect the generated reader-facing retrieval surface with ordinary text tools:

```sh
rg -n "keyword|alias|Entry Question|Reused Trails|inbound:|outbound:" wiki/_indexes wiki
```

## Workflow

Use this workflow for normal `session-condense` / `knowledge-condense` requests.
Do not stop after writing only `wiki/trails/*.qmd` unless the user explicitly
asked for a query trail.

1. Find the wiki root. Prefer the user's explicit path; otherwise look for
   `.qmd` files, `wiki/`, `docs/wiki/`, or a project-local `.llm-wiki/`.
2. Read `.llm-wiki/session-condense-state.json` if it exists.
3. Run or read `.llm-wiki/recall-index.json`, `.llm-wiki/inbox.json`, and
   `.llm-wiki/memex-lint.json` when present.
4. Read new `.llm-wiki/recalls/*.json` files. Treat them as memex usage and
   demand signals, not reader-facing source pages.
5. List recent sessions with `paxl session list --format jsonl`.
6. Skip sessions whose stored `currentSyncVersion` equals the listed
   `currentSyncVersion`.
7. For each changed session, run `paxl session get SESSION --format jsonl`.
8. Read existing qmd files with `rg`, `grep`, `find`, and normal file reads.
   Read `index.qmd` first when it exists, then relevant topic pages and recent
   `log.qmd` entries.
9. Include useful `wiki/trails/*.qmd` pages and recall traces as sources. Query
   trails capture why a path was investigated; recall traces capture what later
   agents actually reused.
10. Extract durable knowledge units using the memex extraction contract:
   decisions, constraints, facts, failures, commands, artifacts, and unresolved
   questions.
11. Update qmd files by merging knowledge units into existing sections. Remove
   stale or duplicate claims when the session clearly supersedes them.
12. Maintain bidirectional links and update `index.qmd` for new or materially
    changed entry points.
13. Update `wiki/memex.graph.json` when pages or links materially change.
14. Run `memex_tools.py refresh --wiki-root .` after qmd and graph edits.
15. Append one compact entry to `log.qmd` describing the ingest or maintenance
    run.
16. Update the state file only after the qmd edits, graph updates, recall trace
    processing, lint, inbox, and visualization refresh are complete.

## Maintenance Completion Rules

A maintenance pass is incomplete unless one of these is true:

- At least one changed session was fetched with `paxl session get`, durable
  knowledge was merged into reader-facing qmd pages, and
  `.llm-wiki/session-condense-state.json` records the processed session cursor.
- Page links or knowledge-unit relationships changed and `wiki/memex.graph.json`
  was created or updated to match the reader-facing wiki.
- Recall traces were processed and either promoted into durable pages, recorded
  as no-op usage signals, or listed in the run log with a clear reason for no
  promotion.
- `.llm-wiki/recall-index.json`, `.llm-wiki/inbox.json`,
  `.llm-wiki/memex-lint.json`, `wiki/_indexes/*.qmd`, and visualization
  artifacts were refreshed after qmd or graph changes.
- Recent candidate sessions were listed, all were already processed at the same
  `currentSyncVersion`, and `log.qmd` records a no-op maintenance run.
- Session listing or fetching failed; the final answer states the exact command
  and error, and no state cursor is advanced.

When a session contains durable project rules, architecture decisions, interface
contracts, validation recipes, or recurring caveats, create or update a focused
page under `wiki/concepts/`, `wiki/decisions/`, or `wiki/failures/`. Do not
leave that knowledge only in `wiki/trails/`.

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
- Prefer atomic knowledge-unit edits over broad prose dumps. Merge duplicates,
  mark stale guidance as superseded, and keep open questions distinct from
  active facts.
- If there is no good destination page, create one focused qmd page and link it
  from `index.qmd` when an index exists.
- Keep `wiki/memex.graph.json` aligned with reader-facing page ids, wikilinks,
  and supersession notes when those relationships change.
- Keep recall traces in `.llm-wiki/recalls/`; summarize only durable lessons in
  reader-facing qmd pages.
- Keep `.llm-wiki/recall-index.json`, `.llm-wiki/inbox.json`,
  `.llm-wiki/memex-lint.json`, `wiki/_indexes/*.qmd`, and visualization
  artifacts generated from the current qmd graph and recall traces.
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

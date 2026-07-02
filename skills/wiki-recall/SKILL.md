---
name: wiki-recall
description: Recall, retrieve, remember, look back, or recover context from a local qmd LLM wiki or memex graph. Use when the user asks an agent to consume the maintained wiki, answer from the memex, retrieve durable project knowledge, recall previous decisions, inspect memex.graph.json, search wiki trails, follow backlinks, rank recall candidates with recall-index, write recall traces, or use index.qmd, log.qmd, and trails before falling back to paxl session-search.
---

# Wiki Recall

Use the local qmd LLM wiki as the first recall layer. Agents should be able to
find useful context with `rg`, `grep`, and normal qmd reads even when they never
call a custom search command. This skill consumes the wiki produced by
`session-condense` and query trails produced by `session-search`. It should
answer from durable wiki pages, generated qmd indexes, trails, and explicit
relationships before searching raw sessions, then record what was reused so the
memex becomes smarter from later maintenance passes.

## Recall Order

1. Find the wiki root. Prefer the user's explicit path; otherwise look for
   `wiki/`, `docs/wiki/`, `.llm-wiki/`, or directories containing `.qmd` files.
2. Read `wiki/_indexes/*.qmd` when present. Treat these generated files as the
   grep-native map of pages, trails, aliases, backlinks, reasoning paths, and
   maintenance gaps.
3. Read `index.qmd` when it exists. Treat it as the human map of concepts,
   decisions, failures, trails, and high-value entry points.
4. Search qmd files with `rg` or `grep`, starting with `wiki/_indexes` and then
   the rest of `wiki`. Match query terms against titles, aliases, tags, paths,
   questions, reusable results, and related/backlink lines.
5. Read the candidate pages and neighbor pages before broad text search.
   Prefer `concept`, `decision`, `failure`, and `query-trail` pages over raw
   logs.
6. Read `memex.graph.json` only as supporting structure when qmd indexes are
   missing or contradictory. The qmd indexes are the preferred retrieval surface.
7. Read `log.qmd` only for recent changes or chronology-sensitive questions.
8. Answer from the wiki with file paths and headings as evidence.
9. Write a recall trace when the answer used graph nodes, qmd pages, query
    trails, fallback session search, or maintenance-worthy gaps.
10. Use `session-search` only when the wiki is missing, stale, or contradicts
   itself.

## Graph Recall

When `wiki/memex.graph.json` exists, parse it before reading many qmd files.
The supported graph shape is:

```json
{
  "schemaVersion": "paxl.memex.graph.v1",
  "nodes": [
    {
      "id": "decision-keep-local-memex-in-session-condense",
      "type": "decision",
      "path": "wiki/decisions/keep-local-memex-in-session-condense.qmd",
      "title": "Keep Local Memex In Session Condense",
      "summary": "Keep local memex maintenance in the skill workflow.",
      "status": "active",
      "topics": ["paxl", "session-condense", "memex"]
    }
  ],
  "edges": [
    {
      "source": "decision-keep-local-memex-in-session-condense",
      "target": "concept-session-condense-local-memex",
      "type": "depends_on"
    }
  ]
}
```

Use this graph procedure:

1. Ignore nodes whose `status` is `archived` unless the user asks about history.
2. Score nodes loosely by query terms in `title`, `summary`, `topics`, `type`,
   and `path`. Exact title/topic hits are stronger than summary hits.
3. Read the top matching node pages.
4. Follow one-hop edges from those nodes. Prioritize `depends_on`, `supports`,
   `supersedes`, and `relates_to`; include `mentions` when the query is broad or
   the source is `index.qmd`.
5. If a page has `[[wikilinks]]` or a `## Related` section that disagrees with
   the graph, read both targets and treat the mismatch as a maintenance issue.
6. Cite both the page path/heading and, when useful, the graph relationship that
   led to the related page.

Do not answer from graph metadata alone when the question requires details. Use
the graph to choose which qmd pages to read, then answer from those pages.

## Recall Ranking

When `wiki/_indexes/reasoning-paths.qmd` exists, prefer it as the usage-memory
surface. It materializes prior recall traces into qmd lines that `rg` can find:
entry questions, reused trails, used nodes, traversed edges, answer sources, and
answer summaries.

The JSON usage index remains useful for maintainers and deterministic tooling.
When `.llm-wiki/recalls/*.json` exists, it can be rebuilt with:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py aggregate --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py rank --wiki-root . \
  --query "What did we decide about local memex?"
```

Use the ranking as a starting point, not as the answer. Read the ranked qmd
pages before responding. The ranking combines:

- lexical match against graph node title, summary, topics, type, and path;
- node recall counts from `.llm-wiki/recall-index.json`;
- one-hop edge traversal counts from previous recalls.

## Commands

Examples:

```sh
rg -n "keyword|related phrase" wiki/_indexes wiki docs/wiki
rg -n "aliases:|tags:|path:|related:" wiki/_indexes/all.qmd
rg -n "Entry Question|Reused Trails|Answer Sources|keyword" wiki/_indexes/reasoning-paths.qmd
rg -n "inbound:|outbound:|page: wiki/concepts/example.qmd" wiki/_indexes/backlinks.qmd
rg -n "type: query-trail|Reusable Result|Question|reusable:" wiki/_indexes wiki/trails docs/wiki
find wiki docs/wiki -name '*.qmd' -maxdepth 4
python3 /path/to/wiki-recall/scripts/memex_tools.py indexes --wiki-root .
python3 /path/to/wiki-recall/scripts/memex_tools.py write-trace --wiki-root . \
  --question "What did we decide about local memex?" \
  --used-node decision-keep-local-memex-in-session-condense \
  --used-edge decision-keep-local-memex-in-session-condense,depends_on,concept-session-condense-local-memex \
  --answer-source wiki/decisions/keep-local-memex-in-session-condense.qmd#Decision
```

When the wiki root is unknown, search lightly:

```sh
find . -name 'memex.graph.json' -maxdepth 5
find . -name '*.qmd' -maxdepth 5
rg -n "type: query-trail|index.qmd|llm wiki|memex|paxl.memex.graph.v1" .
```

## Answering Rules

- Cite the qmd page path and heading or section name that supports the answer.
- Mention important graph relationships when they explain why a related page was
  used, for example `decision -> depends_on -> concept`.
- Prefer synthesized wiki knowledge over raw session transcript excerpts.
- Use query trails to explain how previous agents found an answer, but do not
  expose hidden chain-of-thought or private deliberation.
- If multiple wiki pages disagree, state the conflict and prefer the newest
  `updated_at` or `log.qmd` entry.
- If `memex.graph.json` and qmd backlinks disagree, answer from the newer page
  content and flag the graph/backlink drift for `session-condense`.
- If the wiki lacks the answer, say that clearly and then use `session-search`
  for raw session recall.

## Recall Traces

Write recall traces under `.llm-wiki/recalls/` so future `session-condense`
passes can learn from real query demand. Use the bundled tool instead of
hand-writing JSON when possible:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py write-trace --wiki-root . \
  --question "original user question" \
  --answer-summary "short reusable result" \
  --used-node concept-session-condense-local-memex \
  --used-trail wiki/trails/2026-07-01-omniagent-harness-flattening.qmd \
  --answer-source wiki/concepts/session-condense-local-memex.qmd#Reader-Facing-Shape \
  --maintenance-signal "Create a durable concept when this answer repeats."
```

The trace schema is `paxl.memex.recall-trace.v1`:

```json
{
  "schemaVersion": "paxl.memex.recall-trace.v1",
  "createdAt": "2026-07-02T10:05:00Z",
  "tool": "wiki-recall",
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

Do not record hidden chain-of-thought, raw transcript dumps, secrets, or private
deliberation. A trace is a consumption audit and maintenance signal, not a full
answer archive.

After writing a trace, refresh the usage index when continuing to work in the
same repo:

```sh
python3 /path/to/wiki-recall/scripts/memex_tools.py aggregate --wiki-root .
```

## Trail Reuse

When a trail answers the question, reuse its `Reusable Result` and cite the trail
page. If the current query improves the answer, update the relevant trail or
create a new one using the `session-search` query-trail format. Always include
the reused trail path in the recall trace.

## Maintenance Handoff

If recall reveals stale pages, duplicate concepts, broken links, or a useful raw
session finding that should become durable knowledge, hand off to
`session-condense` after answering. Also hand off when graph nodes, graph edges,
qmd backlinks, or recall traces are stale, missing, or contradictory. Do not
silently rewrite broad wiki structure unless the user asked for maintenance.

---
name: wiki-recall
description: Recall, retrieve, remember, look back, or recover context from a local qmd LLM wiki. Use when the user asks an agent to consume the maintained wiki, answer from the memex, retrieve durable project knowledge, recall previous decisions, search wiki trails, or use index.qmd, log.qmd, and trails before falling back to paxl session-search.
---

# Wiki Recall

Use the local qmd LLM wiki as the first recall layer. This skill consumes the
wiki produced by `session-condense` and query trails produced by
`session-search`. It should answer from durable wiki pages before searching raw
sessions.

## Recall Order

1. Find the wiki root. Prefer the user's explicit path; otherwise look for
   `wiki/`, `docs/wiki/`, `.llm-wiki/`, or directories containing `.qmd` files.
2. Read `index.qmd` first when it exists. Treat it as the map of concepts,
   decisions, trails, and high-value entry points.
3. Search qmd files with `rg`, `grep`, and normal file reads. Prefer topic pages
   and `trails/*.qmd` over raw logs.
4. Read `log.qmd` only for recent changes or chronology-sensitive questions.
5. Answer from the wiki with file paths and headings as evidence.
6. Use `session-search` only when the wiki is missing, stale, or contradicts
   itself.

## Commands

Examples:

```sh
rg -n "keyword|related phrase" wiki docs/wiki
rg -n "type: query-trail|Reusable Result|Question" wiki/trails docs/wiki
find wiki docs/wiki -name '*.qmd' -maxdepth 4
```

When the wiki root is unknown, search lightly:

```sh
find . -name '*.qmd' -maxdepth 5
rg -n "type: query-trail|index.qmd|llm wiki|memex" .
```

## Answering Rules

- Cite the qmd page path and heading or section name that supports the answer.
- Prefer synthesized wiki knowledge over raw session transcript excerpts.
- Use query trails to explain how previous agents found an answer, but do not
  expose hidden chain-of-thought or private deliberation.
- If multiple wiki pages disagree, state the conflict and prefer the newest
  `updated_at` or `log.qmd` entry.
- If the wiki lacks the answer, say that clearly and then use `session-search`
  for raw session recall.

## Trail Reuse

When a trail answers the question, reuse its `Reusable Result` and cite the trail
page. If the current query improves the answer, update the relevant trail or
create a new one using the `session-search` query-trail format.

## Maintenance Handoff

If recall reveals stale pages, duplicate concepts, broken links, or a useful raw
session finding that should become durable knowledge, hand off to
`session-condense` after answering. Do not silently rewrite broad wiki structure
unless the user asked for maintenance.

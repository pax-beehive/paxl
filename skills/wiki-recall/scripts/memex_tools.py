#!/usr/bin/env python3
"""Deterministic helpers for local qmd memex recall and maintenance."""

from __future__ import annotations

import argparse
import collections
import datetime as dt
import hashlib
import html
import json
import math
import os
import re
import sys
import xml.sax.saxutils
from pathlib import Path
from typing import Any


TRACE_SCHEMA = "paxl.memex.recall-trace.v1"
RECALL_INDEX_SCHEMA = "paxl.memex.recall-index.v1"
LINT_SCHEMA = "paxl.memex.lint.v1"
INBOX_SCHEMA = "paxl.memex.inbox.v1"
VISUALIZATION_SCHEMA = "paxl.memex.visualization.v1"
INDEX_DIRNAME = "_indexes"
TRAIL_REQUIRED_SECTIONS = [
    "Question",
    "Search Trail",
    "Rationale Summary",
    "Findings",
    "Reusable Result",
    "Related",
]


def utc_now() -> str:
    return dt.datetime.now(dt.UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def slugify(value: str, limit: int | None = None) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", value.lower()).strip("-")
    slug = slug or "item"
    return slug[:limit].strip("-") if limit else slug


def tokenize(value: str) -> list[str]:
    return [term for term in re.split(r"[^a-z0-9]+", value.lower()) if len(term) > 1]


def parse_edge(value: str) -> dict[str, str]:
    parts = [part.strip() for part in value.split(",", 2)]
    if len(parts) != 3 or any(not part for part in parts):
        raise argparse.ArgumentTypeError("edge must be SOURCE,TYPE,TARGET")
    return {"source": parts[0], "type": parts[1], "target": parts[2]}


def read_json(path: Path, default: Any) -> Any:
    if not path.exists():
        return default
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: Path, payload: Any) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return path


def relpath(path: Path, base: Path) -> str:
    try:
        return path.resolve().relative_to(base.resolve()).as_posix()
    except ValueError:
        return path.as_posix()


def stable_id(*parts: str) -> str:
    digest = hashlib.sha256("|".join(parts).encode("utf-8")).hexdigest()[:12]
    return digest


class Layout:
    def __init__(self, root: Path) -> None:
        root = root.expanduser().resolve()
        if root.name == "wiki" and (root / "memex.graph.json").exists():
            self.project_root = root.parent
            self.wiki_dir = root
        else:
            self.project_root = root
            self.wiki_dir = root / "wiki"
            if not self.wiki_dir.exists() and (root / "memex.graph.json").exists():
                self.wiki_dir = root
        self.llm_dir = self.project_root / ".llm-wiki"
        self.graph_path = self.wiki_dir / "memex.graph.json"
        self.recall_index_path = self.llm_dir / "recall-index.json"
        self.lint_path = self.llm_dir / "memex-lint.json"
        self.inbox_path = self.llm_dir / "inbox.json"
        self.visualization_path = self.llm_dir / "memex-visualization.json"

    def resolve_output(self, value: str | None, default: Path) -> Path:
        if not value:
            return default
        path = Path(value).expanduser()
        if path.is_absolute():
            return path
        return self.project_root / path


def load_graph(layout: Layout) -> dict[str, Any]:
    return read_json(layout.graph_path, {"schemaVersion": "paxl.memex.graph.v1", "nodes": [], "edges": []})


def load_traces(layout: Layout) -> list[tuple[Path, dict[str, Any]]]:
    traces: list[tuple[Path, dict[str, Any]]] = []
    recalls_dir = layout.llm_dir / "recalls"
    if not recalls_dir.exists():
        return traces
    for path in sorted(recalls_dir.glob("*.json")):
        try:
            payload = read_json(path, {})
        except json.JSONDecodeError:
            continue
        if payload.get("schemaVersion") == TRACE_SCHEMA:
            traces.append((path, payload))
    return traces


def edge_key(edge: dict[str, Any]) -> str:
    return f"{edge.get('source', '')}|{edge.get('type', '')}|{edge.get('target', '')}"


def split_edge_key(key: str) -> dict[str, str]:
    source, edge_type, target = key.split("|", 2)
    return {"source": source, "type": edge_type, "target": target}


def trace_hash(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def build_recall_index(layout: Layout) -> dict[str, Any]:
    graph = load_graph(layout)
    nodes_by_id = {node.get("id"): node for node in graph.get("nodes", []) if node.get("id")}
    node_counts: collections.Counter[str] = collections.Counter()
    edge_counts: collections.Counter[str] = collections.Counter()
    trail_counts: collections.Counter[str] = collections.Counter()
    source_counts: collections.Counter[str] = collections.Counter()
    fallback_queries: list[dict[str, Any]] = []
    maintenance_signals: list[dict[str, Any]] = []
    trace_records: list[dict[str, Any]] = []

    for path, trace in load_traces(layout):
        trace_path = relpath(path, layout.project_root)
        trace_records.append(
            {
                "path": trace_path,
                "createdAt": trace.get("createdAt", ""),
                "question": trace.get("question", ""),
                "hash": trace_hash(path),
            }
        )
        for node_id in trace.get("usedNodes", []) or []:
            if isinstance(node_id, str):
                node_counts[node_id] += 1
        for edge in trace.get("usedEdges", []) or []:
            if isinstance(edge, dict) and edge.get("source") and edge.get("target") and edge.get("type"):
                edge_counts[edge_key(edge)] += 1
        for trail in trace.get("usedTrails", []) or []:
            if isinstance(trail, str):
                trail_counts[trail] += 1
        for source in trace.get("answerSources", []) or []:
            if isinstance(source, str):
                source_counts[source.split("#", 1)[0]] += 1
        if trace.get("fallbackSessionSearch"):
            fallback_queries.append(
                {
                    "trace": trace_path,
                    "createdAt": trace.get("createdAt", ""),
                    "question": trace.get("question", ""),
                    "answerSummary": trace.get("answerSummary", ""),
                }
            )
        for signal in trace.get("maintenanceSignals", []) or []:
            if isinstance(signal, str) and signal.strip():
                maintenance_signals.append(
                    {
                        "trace": trace_path,
                        "createdAt": trace.get("createdAt", ""),
                        "question": trace.get("question", ""),
                        "signal": signal.strip(),
                    }
                )

    nodes = []
    for node_id, count in sorted(node_counts.items(), key=lambda item: (-item[1], item[0])):
        node = nodes_by_id.get(node_id, {})
        nodes.append(
            {
                "id": node_id,
                "recalls": count,
                "title": node.get("title", node_id),
                "path": node.get("path", ""),
                "type": node.get("type", ""),
                "topics": node.get("topics", []),
            }
        )

    edges = []
    for key, count in sorted(edge_counts.items(), key=lambda item: (-item[1], item[0])):
        edge = split_edge_key(key)
        edge["traversals"] = count
        edges.append(edge)

    return {
        "schemaVersion": RECALL_INDEX_SCHEMA,
        "generatedAt": utc_now(),
        "traceCount": len(trace_records),
        "traces": trace_records,
        "nodes": nodes,
        "edges": edges,
        "trails": counter_items(trail_counts, "path", "recalls"),
        "sources": counter_items(source_counts, "path", "recalls"),
        "fallbackQueries": fallback_queries,
        "maintenanceSignals": maintenance_signals,
    }


def counter_items(counter: collections.Counter[str], key_name: str, count_name: str) -> list[dict[str, Any]]:
    return [
        {key_name: key, count_name: count}
        for key, count in sorted(counter.items(), key=lambda item: (-item[1], item[0]))
    ]


def command_write_trace(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    created_at = utc_now()
    record = {
        "schemaVersion": TRACE_SCHEMA,
        "createdAt": created_at,
        "tool": "wiki-recall",
        "cwd": os.getcwd(),
        "question": args.question,
        "answerSummary": args.answer_summary,
        "usedNodes": args.used_node,
        "usedEdges": args.used_edge,
        "usedTrails": args.used_trail,
        "answerSources": args.answer_source,
        "fallbackSessionSearch": args.fallback_session_search,
        "maintenanceSignals": args.maintenance_signal,
    }
    default_path = layout.llm_dir / "recalls" / f"{created_at.replace(':', '-')}-{slugify(args.question, 48)}.json"
    path = layout.resolve_output(args.output, default_path)
    write_json(path, record)
    print(path)
    return 0


def command_aggregate(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    index = build_recall_index(layout)
    path = layout.resolve_output(args.output, layout.recall_index_path)
    write_json(path, index)
    print(path)
    return 0


def load_recall_index(layout: Layout) -> dict[str, Any]:
    if layout.recall_index_path.exists():
        return read_json(layout.recall_index_path, {})
    return build_recall_index(layout)


def command_rank(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    graph = load_graph(layout)
    recall_index = load_recall_index(layout)
    ranked = rank_nodes(args.query, graph, recall_index, args.limit)
    payload = {
        "schemaVersion": "paxl.memex.ranking.v1",
        "generatedAt": utc_now(),
        "query": args.query,
        "rankedNodes": ranked,
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))
    return 0


def rank_nodes(query: str, graph: dict[str, Any], recall_index: dict[str, Any], limit: int) -> list[dict[str, Any]]:
    terms = tokenize(query)
    usage = {item.get("id"): int(item.get("recalls", 0)) for item in recall_index.get("nodes", [])}
    edge_usage: collections.Counter[str] = collections.Counter()
    for edge in recall_index.get("edges", []):
        count = int(edge.get("traversals", 0))
        if edge.get("source"):
            edge_usage[edge["source"]] += count
        if edge.get("target"):
            edge_usage[edge["target"]] += count

    ranked: list[dict[str, Any]] = []
    for node in graph.get("nodes", []) or []:
        if node.get("status") == "archived":
            continue
        node_id = node.get("id", "")
        haystack = " ".join(
            [
                str(node.get("title", "")),
                str(node.get("summary", "")),
                " ".join(str(topic) for topic in node.get("topics", []) or []),
                str(node.get("type", "")),
                str(node.get("path", "")),
            ]
        ).lower()
        lexical = 0
        for term in terms:
            if term in haystack:
                lexical += 1
            if term in str(node.get("title", "")).lower():
                lexical += 2
            if term in [str(topic).lower() for topic in node.get("topics", []) or []]:
                lexical += 3
        recalls = usage.get(node_id, 0)
        traversals = edge_usage.get(node_id, 0)
        score = lexical * 10 + min(recalls, 20) * 2 + min(traversals, 20)
        if score > 0:
            ranked.append(
                {
                    "id": node_id,
                    "score": score,
                    "lexicalScore": lexical,
                    "recalls": recalls,
                    "edgeTraversals": traversals,
                    "title": node.get("title", node_id),
                    "path": node.get("path", ""),
                    "type": node.get("type", ""),
                    "topics": node.get("topics", []),
                    "summary": node.get("summary", ""),
                }
            )
    ranked.sort(key=lambda item: (-item["score"], item["title"]))
    return ranked[:limit]


FRONTMATTER_RE = re.compile(r"\A---\n(.*?)\n---\n", re.DOTALL)
WIKILINK_RE = re.compile(r"\[\[([^\]#|]+)")


def parse_frontmatter(content: str) -> dict[str, str]:
    match = FRONTMATTER_RE.match(content)
    if not match:
        return {}
    data: dict[str, str] = {}
    for line in match.group(1).splitlines():
        if ":" not in line or line.startswith(" "):
            continue
        key, value = line.split(":", 1)
        data[key.strip()] = value.strip().strip('"')
    return data


def qmd_pages(layout: Layout) -> list[Path]:
    if not layout.wiki_dir.exists():
        return []
    return sorted(
        path
        for path in layout.wiki_dir.rglob("*.qmd")
        if path.is_file() and INDEX_DIRNAME not in path.relative_to(layout.wiki_dir).parts
    )


def build_node_aliases(nodes: dict[str, dict[str, Any]]) -> dict[str, str]:
    aliases: dict[str, str] = {}
    for node_id, node in nodes.items():
        aliases[node_id] = node_id
        if node.get("title"):
            aliases[slugify(str(node["title"]))] = node_id
        if node.get("path"):
            path = Path(str(node["path"]))
            aliases[path.stem] = node_id
            aliases[slugify(path.stem)] = node_id
    return aliases


def lint_memex(layout: Layout) -> dict[str, Any]:
    graph = load_graph(layout)
    issues: list[dict[str, Any]] = []
    nodes = {node.get("id"): node for node in graph.get("nodes", []) or [] if node.get("id")}
    aliases = build_node_aliases(nodes)
    edge_pairs = {
        (edge.get("source"), edge.get("target"))
        for edge in graph.get("edges", []) or []
        if edge.get("source") and edge.get("target")
    }

    seen_ids: set[str] = set()
    for node in graph.get("nodes", []) or []:
        node_id = node.get("id")
        if not node_id:
            issues.append(issue("error", "graph_node_missing_id", "Graph node is missing id.", node=node))
            continue
        if node_id in seen_ids:
            issues.append(issue("error", "graph_node_duplicate_id", f"Duplicate graph node id {node_id}.", node=node_id))
        seen_ids.add(node_id)
        node_path = layout.project_root / str(node.get("path", ""))
        if node.get("path") and not node_path.exists():
            issues.append(
                issue(
                    "error",
                    "graph_node_missing_path",
                    f"Graph node {node_id} points to missing path {node.get('path')}.",
                    node=node_id,
                    path=node.get("path"),
                )
            )

    for edge in graph.get("edges", []) or []:
        source = edge.get("source")
        target = edge.get("target")
        if source not in nodes:
            issues.append(issue("error", "graph_edge_missing_source", f"Edge source {source} is missing.", edge=edge))
        if target not in nodes:
            issues.append(issue("error", "graph_edge_missing_target", f"Edge target {target} is missing.", edge=edge))

    pages_by_node: dict[str, Path] = {}
    for page in qmd_pages(layout):
        rel = relpath(page, layout.project_root)
        content = page.read_text(encoding="utf-8")
        fm = parse_frontmatter(content)
        node_id = ""
        for candidate_id, node in nodes.items():
            if node.get("path") == rel:
                node_id = candidate_id
                break
        if not node_id and fm.get("id") in nodes:
            node_id = fm["id"]
        if node_id:
            pages_by_node[node_id] = page
        elif page.name != "log.qmd":
            issues.append(
                issue(
                    "warning",
                    "qmd_page_missing_graph_node",
                    f"Qmd page {rel} is not represented in memex.graph.json.",
                    path=rel,
                )
            )

        if not node_id:
            continue
        for raw_target in WIKILINK_RE.findall(content):
            target = aliases.get(raw_target) or aliases.get(slugify(raw_target))
            if target and (node_id, target) not in edge_pairs and (target, node_id) not in edge_pairs:
                issues.append(
                    issue(
                        "warning",
                        "wikilink_missing_graph_edge",
                        f"Wikilink from {node_id} to {target} is missing from memex.graph.json.",
                        node=node_id,
                        target=target,
                        path=rel,
                    )
                )

    for edge in graph.get("edges", []) or []:
        if edge.get("type") == "mentions":
            continue
        source = edge.get("source")
        target = edge.get("target")
        page = pages_by_node.get(source)
        target_node = nodes.get(target, {})
        if not page or not target_node:
            continue
        content = page.read_text(encoding="utf-8").lower()
        target_slug = Path(str(target_node.get("path", ""))).stem.lower()
        target_title = slugify(str(target_node.get("title", "")))
        if target_slug not in content and target_title not in content:
            issues.append(
                issue(
                    "warning",
                    "graph_edge_missing_page_link",
                    f"Graph edge {source} -> {target} is not reflected in the source page.",
                    node=source,
                    target=target,
                    path=relpath(page, layout.project_root),
                )
            )

    incident_counts: collections.Counter[str] = collections.Counter()
    for edge in graph.get("edges", []) or []:
        if edge.get("source"):
            incident_counts[edge["source"]] += 1
        if edge.get("target"):
            incident_counts[edge["target"]] += 1
    for node_id, node in nodes.items():
        if node.get("type") != "index" and incident_counts[node_id] == 0:
            issues.append(issue("info", "graph_node_orphan", f"Graph node {node_id} has no edges.", node=node_id))

    issues = dedupe_issues(issues)
    return {
        "schemaVersion": LINT_SCHEMA,
        "generatedAt": utc_now(),
        "issueCount": len(issues),
        "errorCount": sum(1 for item in issues if item["severity"] == "error"),
        "warningCount": sum(1 for item in issues if item["severity"] == "warning"),
        "issues": issues,
    }


def issue(severity: str, code: str, message: str, **extra: Any) -> dict[str, Any]:
    item = {"severity": severity, "code": code, "message": message}
    item.update(extra)
    return item


def dedupe_issues(issues: list[dict[str, Any]]) -> list[dict[str, Any]]:
    seen: set[str] = set()
    deduped: list[dict[str, Any]] = []
    for item in issues:
        key = json.dumps(item, sort_keys=True, ensure_ascii=False)
        if key in seen:
            continue
        seen.add(key)
        deduped.append(item)
    return deduped


def command_lint(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    payload = lint_memex(layout)
    path = layout.resolve_output(args.output, layout.lint_path)
    write_json(path, payload)
    print(path)
    if args.strict and payload["errorCount"] > 0:
        return 1
    return 0


def build_inbox(layout: Layout) -> dict[str, Any]:
    recall_index = load_recall_index(layout)
    processed = processed_recall_keys(layout, recall_index)
    items: list[dict[str, Any]] = []
    seen: set[str] = set()
    for fallback in recall_index.get("fallbackQueries", []) or []:
        if fallback.get("trace") in processed:
            continue
        key = stable_id("fallback", fallback.get("trace", ""), fallback.get("question", ""))
        if key in seen:
            continue
        seen.add(key)
        items.append(
            {
                "id": f"inbox-{key}",
                "type": "weak-answer",
                "priority": "high",
                "status": "open",
                "question": fallback.get("question", ""),
                "sourceTrace": fallback.get("trace", ""),
                "reason": "Recall fell back to raw session search.",
                "suggestedAction": "Create or update a query trail or durable qmd page.",
            }
        )
    for signal in recall_index.get("maintenanceSignals", []) or []:
        if signal.get("trace") in processed:
            continue
        key = stable_id("signal", signal.get("trace", ""), signal.get("signal", ""))
        if key in seen:
            continue
        seen.add(key)
        items.append(
            {
                "id": f"inbox-{key}",
                "type": "maintenance-signal",
                "priority": "medium",
                "status": "open",
                "question": signal.get("question", ""),
                "sourceTrace": signal.get("trace", ""),
                "reason": signal.get("signal", ""),
                "suggestedAction": "Have session-condense promote, merge, relink, or explicitly no-op this signal.",
            }
        )
    return {
        "schemaVersion": INBOX_SCHEMA,
        "generatedAt": utc_now(),
        "itemCount": len(items),
        "items": items,
    }


def processed_recall_keys(layout: Layout, recall_index: dict[str, Any]) -> set[str]:
    state = read_json(layout.llm_dir / "session-condense-state.json", {})
    processed: set[str] = set()
    path_to_hash = {item.get("path"): item.get("hash") for item in recall_index.get("traces", []) or []}

    def collect(value: Any) -> None:
        if isinstance(value, dict):
            for path, meta in value.get("processedRecalls", {}).items():
                processed.add(path)
                if isinstance(meta, dict) and meta.get("hash"):
                    processed.add(str(meta["hash"]))
            for child in value.values():
                collect(child)
        elif isinstance(value, list):
            for child in value:
                collect(child)

    collect(state)
    for path, digest in path_to_hash.items():
        if digest in processed:
            processed.add(path)
    return processed


def command_inbox(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    payload = build_inbox(layout)
    path = layout.resolve_output(args.output, layout.inbox_path)
    write_json(path, payload)
    print(path)
    return 0


def command_mark_processed(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    trace_path = Path(args.trace)
    if not trace_path.is_absolute():
        trace_path = layout.project_root / trace_path
    rel_trace = relpath(trace_path, layout.project_root)
    if not trace_path.exists():
        raise FileNotFoundError(f"Recall trace not found: {trace_path}")
    state_path = layout.llm_dir / "session-condense-state.json"
    state = read_json(state_path, {})
    processed = state.setdefault("processedRecalls", {})
    processed[rel_trace] = {
        "hash": trace_hash(trace_path),
        "processedAt": utc_now(),
        "outcome": args.outcome,
        "outputs": args.output_path,
    }
    write_json(state_path, state)
    print(state_path)
    return 0


def visualization_data(layout: Layout) -> dict[str, Any]:
    graph = load_graph(layout)
    recall_index = load_recall_index(layout)
    lint = read_json(layout.lint_path, lint_memex(layout))
    node_usage = {item.get("id"): int(item.get("recalls", 0)) for item in recall_index.get("nodes", [])}
    edge_usage = {edge_key(edge): int(edge.get("traversals", 0)) for edge in recall_index.get("edges", [])}
    issue_nodes = {
        item.get("node")
        for item in lint.get("issues", []) or []
        if item.get("severity") in {"error", "warning"} and isinstance(item.get("node"), str)
    }
    return {
        "schemaVersion": VISUALIZATION_SCHEMA,
        "generatedAt": utc_now(),
        "nodes": [
            {
                **node,
                "recalls": node_usage.get(node.get("id"), 0),
                "hasIssue": node.get("id") in issue_nodes,
            }
            for node in graph.get("nodes", []) or []
        ],
        "edges": [
            {
                **edge,
                "traversals": edge_usage.get(edge_key(edge), 0),
            }
            for edge in graph.get("edges", []) or []
        ],
    }


def command_visualize(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    payload = visualization_data(layout)
    json_path = layout.resolve_output(args.output_json, layout.visualization_path)
    mmd_path = layout.resolve_output(args.output_mmd, layout.wiki_dir / "memex.graph.mmd")
    svg_path = layout.resolve_output(args.output_svg, layout.wiki_dir / "memex.graph.svg")
    write_json(json_path, payload)
    mmd_path.parent.mkdir(parents=True, exist_ok=True)
    mmd_path.write_text(render_mermaid(payload), encoding="utf-8")
    svg_path.parent.mkdir(parents=True, exist_ok=True)
    svg_path.write_text(render_svg(payload), encoding="utf-8")
    print(json.dumps({"json": str(json_path), "mermaid": str(mmd_path), "svg": str(svg_path)}, indent=2))
    return 0


def render_mermaid(payload: dict[str, Any]) -> str:
    lines = [
        "---",
        "title: Memex Graph",
        "---",
        "flowchart LR",
        "  classDef hot fill:#d7f5e8,stroke:#16784d,stroke-width:2px;",
        "  classDef warm fill:#e6f0ff,stroke:#3867d6,stroke-width:1.5px;",
        "  classDef stale fill:#ffe0e0,stroke:#b42318,stroke-width:2px;",
    ]
    for node in payload.get("nodes", []) or []:
        node_id = mermaid_id(str(node.get("id", "")))
        label = f"{node.get('title', node.get('id', ''))}\\nuses:{node.get('recalls', 0)}"
        lines.append(f"  {node_id}[\"{label}\"]")
        if node.get("hasIssue"):
            lines.append(f"  class {node_id} stale;")
        elif int(node.get("recalls", 0)) > 1:
            lines.append(f"  class {node_id} hot;")
        elif int(node.get("recalls", 0)) > 0:
            lines.append(f"  class {node_id} warm;")
    for edge in payload.get("edges", []) or []:
        source = mermaid_id(str(edge.get("source", "")))
        target = mermaid_id(str(edge.get("target", "")))
        label = f"{edge.get('type', '')} / {edge.get('traversals', 0)}"
        lines.append(f"  {source} -- \"{label}\" --> {target}")
    return "\n".join(lines) + "\n"


def mermaid_id(value: str) -> str:
    return "n_" + re.sub(r"[^a-zA-Z0-9_]", "_", value)


def render_svg(payload: dict[str, Any]) -> str:
    nodes = payload.get("nodes", []) or []
    edges = payload.get("edges", []) or []
    width, height = 1200, 800
    center_x, center_y = width / 2, height / 2
    radius = min(width, height) * 0.36
    positions: dict[str, tuple[float, float]] = {}
    count = max(len(nodes), 1)
    for index, node in enumerate(nodes):
        angle = 2 * math.pi * index / count - math.pi / 2
        positions[str(node.get("id", ""))] = (center_x + radius * math.cos(angle), center_y + radius * math.sin(angle))

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        "<style>text{font-family:Arial,sans-serif;font-size:12px}.edge{stroke:#8a8f98}.label{fill:#555}.node{stroke:#2f3437;stroke-width:1.5px}.hot{fill:#d7f5e8}.warm{fill:#e6f0ff}.cold{fill:#f7f7f7}.stale{fill:#ffe0e0;stroke:#b42318;stroke-width:2.5px}</style>",
        '<rect width="100%" height="100%" fill="#ffffff"/>',
    ]
    for edge in edges:
        source = str(edge.get("source", ""))
        target = str(edge.get("target", ""))
        if source not in positions or target not in positions:
            continue
        x1, y1 = positions[source]
        x2, y2 = positions[target]
        width_px = 1 + min(int(edge.get("traversals", 0)), 8)
        parts.append(
            f'<line class="edge" x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" stroke-width="{width_px}" opacity="0.75"/>'
        )
        parts.append(
            f'<text class="label" x="{(x1 + x2) / 2:.1f}" y="{(y1 + y2) / 2:.1f}">{xml_escape(str(edge.get("type", "")))}:{edge.get("traversals", 0)}</text>'
        )
    for node in nodes:
        node_id = str(node.get("id", ""))
        x, y = positions[node_id]
        recalls = int(node.get("recalls", 0))
        radius_px = 18 + min(recalls, 10) * 3
        cls = "stale" if node.get("hasIssue") else "hot" if recalls > 1 else "warm" if recalls else "cold"
        title = xml_escape(str(node.get("title", node_id)))
        parts.append(f'<circle class="node {cls}" cx="{x:.1f}" cy="{y:.1f}" r="{radius_px}"/>')
        parts.append(f'<text text-anchor="middle" x="{x:.1f}" y="{y - radius_px - 8:.1f}">{title}</text>')
        parts.append(f'<text text-anchor="middle" x="{x:.1f}" y="{y + 4:.1f}">uses:{recalls}</text>')
    parts.append("</svg>")
    return "\n".join(parts) + "\n"


def xml_escape(value: str) -> str:
    return xml.sax.saxutils.escape(value, {'"': "&quot;"})


HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$")


def parse_list_scalar(value: str) -> list[str]:
    value = value.strip()
    if not value:
        return []
    if value.startswith("[") and value.endswith("]"):
        value = value[1:-1]
    return [item.strip().strip('"').strip("'") for item in value.split(",") if item.strip()]


def strip_frontmatter(content: str) -> str:
    return FRONTMATTER_RE.sub("", content, count=1)


def extract_heading_title(path: Path, content: str) -> str:
    for line in strip_frontmatter(content).splitlines():
        match = HEADING_RE.match(line.strip())
        if match and len(match.group(1)) == 1:
            return match.group(2).strip()
    return path.stem.replace("-", " ").title()


def extract_preview(content: str) -> str:
    for line in strip_frontmatter(content).splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or stripped.startswith("- "):
            continue
        return collapse_ws(stripped)[:240]
    return ""


def collapse_ws(value: str) -> str:
    return " ".join(value.split())


def extract_sections(content: str) -> dict[str, str]:
    sections: dict[str, list[str]] = {}
    current = ""
    for line in strip_frontmatter(content).splitlines():
        match = HEADING_RE.match(line.strip())
        if match:
            current = normalize_section(match.group(2))
            sections.setdefault(current, [])
            continue
        if current:
            sections[current].append(line)
    return {key: "\n".join(lines).strip() for key, lines in sections.items()}


def normalize_section(value: str) -> str:
    return " ".join(value.lower().split())


def wikilinks(content: str) -> list[str]:
    links: list[str] = []
    for raw in WIKILINK_RE.findall(content):
        target = raw.split("|", 1)[0].strip()
        if target and target not in links:
            links.append(target)
    return links


def page_records(layout: Layout) -> list[dict[str, Any]]:
    graph = load_graph(layout)
    nodes_by_path = {
        str(node.get("path")): node
        for node in graph.get("nodes", []) or []
        if node.get("path")
    }
    nodes_by_id = {
        str(node.get("id")): node
        for node in graph.get("nodes", []) or []
        if node.get("id")
    }
    records: list[dict[str, Any]] = []
    for page in qmd_pages(layout):
        rel = relpath(page, layout.project_root)
        content = page.read_text(encoding="utf-8")
        fm = parse_frontmatter(content)
        node = nodes_by_path.get(rel) or nodes_by_id.get(fm.get("id", ""))
        page_type = fm.get("type") or (node or {}).get("type") or infer_page_type(rel)
        title = fm.get("title") or (node or {}).get("title") or extract_heading_title(page, content)
        aliases = parse_list_scalar(fm.get("aliases", ""))
        tags = parse_list_scalar(fm.get("tags", "")) or parse_list_scalar(fm.get("topics", ""))
        if not tags:
            tags = [str(topic) for topic in (node or {}).get("topics", []) or []]
        sections = extract_sections(content)
        records.append(
            {
                "id": fm.get("id") or (node or {}).get("id") or "",
                "type": page_type,
                "title": title,
                "aliases": aliases,
                "tags": tags,
                "summary": fm.get("summary") or (node or {}).get("summary") or extract_preview(content),
                "status": fm.get("status") or (node or {}).get("status") or "",
                "path": rel,
                "query": fm.get("query", ""),
                "links": wikilinks(content),
                "sections": sections,
                "node": node or {},
            }
        )
    return records


def infer_page_type(rel_path: str) -> str:
    parts = Path(rel_path).parts
    if "trails" in parts:
        return "query-trail"
    if "concepts" in parts:
        return "concept"
    if "decisions" in parts:
        return "decision"
    if "failures" in parts:
        return "failure"
    return "qmd"


def qmd_line(label: str, value: Any) -> str:
    if isinstance(value, list):
        rendered = ", ".join(str(item) for item in value if str(item))
    else:
        rendered = str(value or "")
    return f"{label}: {rendered}"


def trail_missing_sections(record: dict[str, Any]) -> list[str]:
    sections = record.get("sections", {})
    missing: list[str] = []
    for section in TRAIL_REQUIRED_SECTIONS:
        if not sections.get(normalize_section(section)):
            missing.append(section)
    return missing


def render_all_index(layout: Layout, records: list[dict[str, Any]]) -> str:
    lines = generated_header("All Memex Pages")
    lines.extend(
        [
            "This grep-native index materializes page metadata so agents can use `rg`, `grep`, and qmd reads without parsing JSON.",
            "",
        ]
    )
    for record in sorted(records, key=lambda item: (item["type"], item["title"], item["path"])):
        lines.extend(
            [
                f"## {record['title']}",
                qmd_line("id", record["id"]),
                qmd_line("type", record["type"]),
                qmd_line("title", record["title"]),
                qmd_line("aliases", record["aliases"]),
                qmd_line("tags", record["tags"]),
                qmd_line("status", record["status"]),
                qmd_line("summary", record["summary"]),
                qmd_line("path", record["path"]),
                qmd_line("related", record["links"]),
                "",
            ]
        )
    return "\n".join(lines).rstrip() + "\n"


def render_concepts_index(records: list[dict[str, Any]]) -> str:
    lines = generated_header("Concept And Knowledge Pages")
    lines.extend(["Use this index for broad `rg` entry points across non-trail qmd pages.", ""])
    for record in sorted(records, key=lambda item: (item["type"], item["title"])):
        if record["type"] == "query-trail":
            continue
        lines.extend(
            [
                f"## {record['title']}",
                qmd_line("id", record["id"]),
                qmd_line("type", record["type"]),
                qmd_line("tags", record["tags"]),
                qmd_line("aliases", record["aliases"]),
                qmd_line("summary", record["summary"]),
                qmd_line("path", record["path"]),
                "",
            ]
        )
    return "\n".join(lines).rstrip() + "\n"


def render_trails_index(records: list[dict[str, Any]]) -> str:
    lines = generated_header("Query Trails")
    lines.extend(["Query trails are reusable retrieval paths with grep-visible questions and reusable results.", ""])
    for record in sorted(records, key=lambda item: item["title"]):
        if record["type"] != "query-trail":
            continue
        sections = record.get("sections", {})
        missing = trail_missing_sections(record)
        lines.extend(
            [
                f"## {record['title']}",
                qmd_line("id", record["id"]),
                "type: query-trail",
                qmd_line("query", record["query"] or collapse_ws(sections.get("question", ""))),
                qmd_line("tags", record["tags"]),
                qmd_line("path", record["path"]),
                qmd_line("search", collapse_ws(sections.get("search trail", ""))),
                qmd_line("rationale", collapse_ws(sections.get("rationale summary", ""))),
                qmd_line("findings", collapse_ws(sections.get("findings", ""))),
                qmd_line("reusable", collapse_ws(sections.get("reusable result", ""))),
                qmd_line("missing", missing),
                "",
            ]
        )
    return "\n".join(lines).rstrip() + "\n"


def build_backlinks(layout: Layout, records: list[dict[str, Any]]) -> dict[str, dict[str, list[str]]]:
    graph = load_graph(layout)
    path_by_id = {
        record["id"]: record["path"]
        for record in records
        if record.get("id")
    }
    backlinks: dict[str, dict[str, list[str]]] = {
        record["path"]: {"inbound": [], "outbound": []}
        for record in records
    }
    for edge in graph.get("edges", []) or []:
        source = str(edge.get("source", ""))
        target = str(edge.get("target", ""))
        edge_type = str(edge.get("type", ""))
        source_path = path_by_id.get(source)
        target_path = path_by_id.get(target)
        if source_path:
            backlinks.setdefault(source_path, {"inbound": [], "outbound": []})["outbound"].append(
                f"{source} {edge_type} {target}"
            )
        if target_path:
            backlinks.setdefault(target_path, {"inbound": [], "outbound": []})["inbound"].append(
                f"{source} {edge_type} {target}"
            )
    return backlinks


def render_backlinks_index(layout: Layout, records: list[dict[str, Any]]) -> str:
    backlinks = build_backlinks(layout, records)
    lines = generated_header("Materialized Backlinks")
    lines.extend(["Backlinks are generated qmd so agents do not need to parse `memex.graph.json`.", ""])
    for record in sorted(records, key=lambda item: item["path"]):
        links = backlinks.get(record["path"], {"inbound": [], "outbound": []})
        lines.extend([f"## {record['title']}", qmd_line("page", record["path"])])
        for inbound in links["inbound"]:
            lines.append(qmd_line("inbound", inbound))
        for outbound in links["outbound"]:
            lines.append(qmd_line("outbound", outbound))
        if not links["inbound"] and not links["outbound"]:
            lines.append("links: none")
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def render_reasoning_paths_index(layout: Layout) -> str:
    lines = generated_header("Materialized Reasoning Paths")
    lines.extend(
        [
            "Recall traces are private raw input under `.llm-wiki/`; this qmd file is the grep-native consumption surface.",
            "",
        ]
    )
    traces = load_traces(layout)
    if not traces:
        lines.extend(["No recall traces found.", ""])
        return "\n".join(lines).rstrip() + "\n"
    for path, trace in traces:
        trace_path = relpath(path, layout.project_root)
        used_edges = [
            f"{edge.get('source', '')} {edge.get('type', '')} {edge.get('target', '')}"
            for edge in trace.get("usedEdges", []) or []
            if isinstance(edge, dict)
        ]
        lines.extend(
            [
                f"## {trace.get('question', trace_path)}",
                qmd_line("trace", trace_path),
                qmd_line("created_at", trace.get("createdAt", "")),
                qmd_line("Entry Question", trace.get("question", "")),
                qmd_line("Reused Trails", trace.get("usedTrails", []) or []),
                qmd_line("Used Nodes", trace.get("usedNodes", []) or []),
                qmd_line("Traversed Edges", used_edges),
                qmd_line("Answer Sources", trace.get("answerSources", []) or []),
                qmd_line("Answer Summary", trace.get("answerSummary", "")),
                qmd_line("fallback_session_search", trace.get("fallbackSessionSearch", False)),
                "",
            ]
        )
    return "\n".join(lines).rstrip() + "\n"


def render_maintenance_index(
    layout: Layout,
    records: list[dict[str, Any]],
    lint: dict[str, Any],
    inbox: dict[str, Any],
) -> str:
    lines = generated_header("Memex Maintenance")
    lines.extend(["Generated maintenance qmd for weak trails, lint findings, and recall-demand inbox items.", ""])
    lines.extend(["## Weak Trails", ""])
    weak_count = 0
    for record in sorted(records, key=lambda item: item["path"]):
        if record["type"] != "query-trail":
            continue
        missing = trail_missing_sections(record)
        if not missing:
            continue
        weak_count += 1
        lines.extend(
            [
                qmd_line("- weak trail", record["path"]),
                f"  {qmd_line('missing', missing)}",
                "",
            ]
        )
    if weak_count == 0:
        lines.extend(["No weak trails found.", ""])
    lines.extend(["## Lint Findings", ""])
    for item in lint.get("issues", []) or []:
        lines.append(
            f"- {item.get('severity', '')}: {item.get('code', '')}: {item.get('message', '')}"
        )
    if not lint.get("issues"):
        lines.append("No lint issues found.")
    lines.extend(["", "## Inbox", ""])
    for item in inbox.get("items", []) or []:
        lines.extend(
            [
                f"- {item.get('type', '')}: {item.get('question', '')}",
                f"  sourceTrace: {item.get('sourceTrace', '')}",
                f"  reason: {item.get('reason', '')}",
            ]
        )
    if not inbox.get("items"):
        lines.append("No inbox items found.")
    lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def generated_header(title: str) -> list[str]:
    return [
        "---",
        "type: generated-index",
        f"title: \"{title}\"",
        f"updated_at: \"{utc_now()}\"",
        "tags: [memex, grep-native, generated]",
        "---",
        "",
        f"# {title}",
        "",
    ]


def write_qmd_indexes(
    layout: Layout,
    lint: dict[str, Any],
    inbox: dict[str, Any],
) -> dict[str, Path]:
    records = page_records(layout)
    index_dir = layout.wiki_dir / INDEX_DIRNAME
    index_dir.mkdir(parents=True, exist_ok=True)
    outputs = {
        "all": index_dir / "all.qmd",
        "concepts": index_dir / "concepts.qmd",
        "trails": index_dir / "trails.qmd",
        "reasoningPaths": index_dir / "reasoning-paths.qmd",
        "backlinks": index_dir / "backlinks.qmd",
        "maintenance": index_dir / "maintenance.qmd",
    }
    rendered = {
        "all": render_all_index(layout, records),
        "concepts": render_concepts_index(records),
        "trails": render_trails_index(records),
        "reasoningPaths": render_reasoning_paths_index(layout),
        "backlinks": render_backlinks_index(layout, records),
        "maintenance": render_maintenance_index(layout, records, lint, inbox),
    }
    for name, path in outputs.items():
        path.write_text(rendered[name], encoding="utf-8")
    return outputs


def command_indexes(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    lint = read_json(layout.lint_path, lint_memex(layout))
    inbox = read_json(layout.inbox_path, build_inbox(layout))
    outputs = write_qmd_indexes(layout, lint, inbox)
    print(json.dumps({name: str(path) for name, path in outputs.items()}, indent=2))
    return 0


def command_refresh(args: argparse.Namespace) -> int:
    layout = Layout(Path(args.wiki_root))
    write_json(layout.recall_index_path, build_recall_index(layout))
    lint = lint_memex(layout)
    write_json(layout.lint_path, lint)
    inbox = build_inbox(layout)
    write_json(layout.inbox_path, inbox)
    index_outputs = write_qmd_indexes(layout, lint, inbox)
    payload = visualization_data(layout)
    write_json(layout.visualization_path, payload)
    (layout.wiki_dir / "memex.graph.mmd").write_text(render_mermaid(payload), encoding="utf-8")
    (layout.wiki_dir / "memex.graph.svg").write_text(render_svg(payload), encoding="utf-8")
    print(
        json.dumps(
            {
                "recallIndex": str(layout.recall_index_path),
                "lint": str(layout.lint_path),
                "inbox": str(layout.inbox_path),
                "visualization": str(layout.visualization_path),
                "mermaid": str(layout.wiki_dir / "memex.graph.mmd"),
                "svg": str(layout.wiki_dir / "memex.graph.svg"),
                "indexes": {name: str(path) for name, path in index_outputs.items()},
            },
            indent=2,
        )
    )
    return 0


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--wiki-root", default=".", help="Project root or wiki directory.")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Local qmd memex helper tools.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    write_trace = subparsers.add_parser("write-trace", help="Write a wiki-recall trace.")
    add_common(write_trace)
    write_trace.add_argument("--question", required=True, help="User question or recall task.")
    write_trace.add_argument("--answer-summary", default="", help="Short reusable answer summary.")
    write_trace.add_argument("--used-node", action="append", default=[], help="Graph node id used.")
    write_trace.add_argument("--used-edge", action="append", default=[], type=parse_edge, help="Graph edge used as SOURCE,TYPE,TARGET.")
    write_trace.add_argument("--used-trail", action="append", default=[], help="Query trail path used.")
    write_trace.add_argument("--answer-source", action="append", default=[], help="qmd source path or path#heading.")
    write_trace.add_argument("--maintenance-signal", action="append", default=[], help="Signal for later session-condense maintenance.")
    write_trace.add_argument("--fallback-session-search", action="store_true", help="Set when wiki recall fell back to raw session search.")
    write_trace.add_argument("--output", help="Optional output path. Relative paths are under the project root.")
    write_trace.set_defaults(func=command_write_trace)

    aggregate = subparsers.add_parser("aggregate", help="Build .llm-wiki/recall-index.json from recall traces.")
    add_common(aggregate)
    aggregate.add_argument("--output", help="Optional output path.")
    aggregate.set_defaults(func=command_aggregate)

    rank = subparsers.add_parser("rank", help="Rank graph nodes for a recall query using lexical and usage signals.")
    add_common(rank)
    rank.add_argument("--query", required=True, help="Recall query.")
    rank.add_argument("--limit", type=int, default=8, help="Maximum ranked nodes.")
    rank.set_defaults(func=command_rank)

    lint = subparsers.add_parser("lint", help="Lint graph, qmd backlinks, and page coverage.")
    add_common(lint)
    lint.add_argument("--output", help="Optional output path.")
    lint.add_argument("--strict", action="store_true", help="Exit non-zero when lint errors exist.")
    lint.set_defaults(func=command_lint)

    inbox = subparsers.add_parser("inbox", help="Build .llm-wiki/inbox.json from weak answers and maintenance signals.")
    add_common(inbox)
    inbox.add_argument("--output", help="Optional output path.")
    inbox.set_defaults(func=command_inbox)

    mark_processed = subparsers.add_parser("mark-processed", help="Mark a recall trace as processed in session-condense state.")
    add_common(mark_processed)
    mark_processed.add_argument("--trace", required=True, help="Recall trace path to mark processed.")
    mark_processed.add_argument("--outcome", required=True, help="Short outcome such as promoted, fixed, merged, or no-op.")
    mark_processed.add_argument("--output-path", action="append", default=[], help="Output qmd, graph, or trail path produced by processing.")
    mark_processed.set_defaults(func=command_mark_processed)

    visualize = subparsers.add_parser("visualize", help="Generate visual graph artifacts from graph and recall-index.")
    add_common(visualize)
    visualize.add_argument("--output-json", help="Optional visualization JSON output path.")
    visualize.add_argument("--output-mmd", help="Optional Mermaid output path.")
    visualize.add_argument("--output-svg", help="Optional SVG output path.")
    visualize.set_defaults(func=command_visualize)

    indexes = subparsers.add_parser("indexes", help="Generate grep-native qmd indexes.")
    add_common(indexes)
    indexes.set_defaults(func=command_indexes)

    refresh = subparsers.add_parser("refresh", help="Run aggregate, lint, inbox, qmd indexes, and visualize.")
    add_common(refresh)
    refresh.set_defaults(func=command_refresh)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())

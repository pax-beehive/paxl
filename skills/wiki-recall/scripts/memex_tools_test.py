#!/usr/bin/env python3
"""Tests for memex_tools.py."""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import memex_tools


class MemexToolsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / "wiki" / "concepts").mkdir(parents=True)
        (self.root / ".llm-wiki" / "recalls").mkdir(parents=True)
        (self.root / "wiki" / "index.qmd").write_text(
            """---
title: "Index"
---

# Index

- [Alpha](concepts/alpha.qmd)
""",
            encoding="utf-8",
        )
        (self.root / "wiki" / "concepts" / "alpha.qmd").write_text(
            """---
type: concept
id: concept-alpha
title: "Alpha"
updated_at: "2026-07-02T00:00:00Z"
---

# Alpha

Links to [[beta]].
""",
            encoding="utf-8",
        )
        (self.root / "wiki" / "concepts" / "beta.qmd").write_text(
            """---
type: concept
id: concept-beta
title: "Beta"
updated_at: "2026-07-02T00:00:00Z"
---

# Beta
""",
            encoding="utf-8",
        )
        graph = {
            "schemaVersion": "paxl.memex.graph.v1",
            "nodes": [
                {
                    "id": "index-test",
                    "type": "index",
                    "path": "wiki/index.qmd",
                    "title": "Index",
                    "summary": "Index",
                    "status": "active",
                    "topics": ["test"],
                },
                {
                    "id": "concept-alpha",
                    "type": "concept",
                    "path": "wiki/concepts/alpha.qmd",
                    "title": "Alpha",
                    "summary": "Alpha summary",
                    "status": "active",
                    "topics": ["alpha"],
                },
                {
                    "id": "concept-beta",
                    "type": "concept",
                    "path": "wiki/concepts/beta.qmd",
                    "title": "Beta",
                    "summary": "Beta summary",
                    "status": "active",
                    "topics": ["beta"],
                },
            ],
            "edges": [
                {"source": "index-test", "target": "concept-alpha", "type": "mentions"},
                {"source": "concept-alpha", "target": "concept-beta", "type": "relates_to"},
            ],
        }
        (self.root / "wiki" / "memex.graph.json").write_text(json.dumps(graph), encoding="utf-8")
        trace = {
            "schemaVersion": "paxl.memex.recall-trace.v1",
            "createdAt": "2026-07-02T00:00:00Z",
            "question": "alpha question",
            "answerSummary": "Alpha answer",
            "usedNodes": ["concept-alpha"],
            "usedEdges": [{"source": "concept-alpha", "type": "relates_to", "target": "concept-beta"}],
            "usedTrails": ["wiki/trails/alpha.qmd"],
            "answerSources": ["wiki/concepts/alpha.qmd#Alpha"],
            "fallbackSessionSearch": True,
            "maintenanceSignals": ["Promote alpha."],
        }
        (self.root / ".llm-wiki" / "recalls" / "trace.json").write_text(json.dumps(trace), encoding="utf-8")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_refresh_builds_index_inbox_lint_and_visuals(self) -> None:
        exit_code = memex_tools.main(["refresh", "--wiki-root", str(self.root)])

        self.assertEqual(exit_code, 0)
        recall_index = self.read_json(".llm-wiki/recall-index.json")
        self.assertEqual(recall_index["traceCount"], 1)
        self.assertEqual(recall_index["nodes"][0]["id"], "concept-alpha")
        self.assertEqual(recall_index["edges"][0]["traversals"], 1)

        inbox = self.read_json(".llm-wiki/inbox.json")
        self.assertEqual(inbox["itemCount"], 2)

        lint = self.read_json(".llm-wiki/memex-lint.json")
        self.assertEqual(lint["errorCount"], 0)

        self.assertTrue((self.root / ".llm-wiki" / "memex-visualization.json").exists())
        self.assertTrue((self.root / "wiki" / "memex.graph.mmd").exists())
        self.assertTrue((self.root / "wiki" / "memex.graph.svg").exists())

        memex_tools.main(
            [
                "mark-processed",
                "--wiki-root",
                str(self.root),
                "--trace",
                ".llm-wiki/recalls/trace.json",
                "--outcome",
                "promoted",
                "--output-path",
                "wiki/concepts/alpha.qmd",
            ]
        )
        memex_tools.main(["aggregate", "--wiki-root", str(self.root)])
        memex_tools.main(["inbox", "--wiki-root", str(self.root)])
        inbox = self.read_json(".llm-wiki/inbox.json")
        self.assertEqual(inbox["itemCount"], 0)

    def test_rank_combines_lexical_and_reuse_signals(self) -> None:
        memex_tools.main(["aggregate", "--wiki-root", str(self.root)])
        ranked = memex_tools.rank_nodes(
            "alpha",
            memex_tools.load_graph(memex_tools.Layout(self.root)),
            self.read_json(".llm-wiki/recall-index.json"),
            3,
        )

        self.assertEqual(ranked[0]["id"], "concept-alpha")
        self.assertGreater(ranked[0]["recalls"], 0)

    def read_json(self, path: str) -> dict:
        return json.loads((self.root / path).read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()

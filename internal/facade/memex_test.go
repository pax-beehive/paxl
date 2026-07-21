package facade_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/stretchr/testify/suite"
)

type MemexFacadeSuite struct {
	suite.Suite
}

func TestMemexFacadeSuite(t *testing.T) {
	suite.Run(t, new(MemexFacadeSuite))
}

func (s *MemexFacadeSuite) TestRenderHTMLReadsLocalMemexArtifacts() {
	root := s.T().TempDir()
	s.writeMemexFixture(root)
	memexFacade := facade.NewMemexFacade()

	resp, err := memexFacade.Render(context.Background(), &facade.RenderMemexRequest{
		WikiRoot: root,
		Format:   facade.MemexRenderFormatHTML,
	})

	s.Require().NoError(err)
	s.Contains(resp.HTML, "Paxl Memex")
	s.Contains(resp.HTML, "Session Condense Local Memex")
	s.Contains(resp.HTML, `/page/wiki%2Fconcepts%2Fsession-condense-local-memex.qmd`)
	s.Contains(resp.HTML, `class="tag type">concept`)
	s.Contains(resp.HTML, "Recalls")
	s.Contains(resp.HTML, "recall-index.json")
	s.Contains(resp.HTML, "/assets/memex.graph.svg")
	s.Require().Len(resp.Assets, 1)
	s.Equal("/assets/memex.graph.svg", resp.Assets[0].URLPath)
	pageHTML := resp.PageHTML["/page/wiki%2Fconcepts%2Fsession-condense-local-memex.qmd"]
	s.Contains(pageHTML, "Full body paragraph with durable memex context.")
	s.Contains(pageHTML, `href="/page/wiki%2Fconcepts%2Fmemex-recall-traces.qmd"`)
	s.Contains(pageHTML, `class="wikilink"`)
	s.Contains(pageHTML, "Related")
	s.Contains(pageHTML, "supports")
	s.Contains(pageHTML, "Memex Recall Traces")
}

func (s *MemexFacadeSuite) TestRenderHTMLPromotesTrailsAndReasoningPaths() {
	root := s.T().TempDir()
	s.writeMemexFixture(root)
	memexFacade := facade.NewMemexFacade()

	resp, err := memexFacade.Render(context.Background(), &facade.RenderMemexRequest{
		WikiRoot: root,
		Format:   facade.MemexRenderFormatHTML,
	})

	s.Require().NoError(err)
	s.Contains(resp.HTML, "Query Trails")
	s.Contains(resp.HTML, "Reasoning Paths")
	s.Contains(resp.HTML, "How does recall become explicit reasoning?")
	s.Contains(resp.HTML, "Use query trails as named retrieval paths")
	s.Contains(resp.HTML, "Entry Question")
	s.Contains(resp.HTML, "Reused Trail")
	s.Contains(resp.HTML, "Used Nodes")

	trailHTML := resp.PageHTML["/page/wiki%2Ftrails%2F2026-07-01-explicit-recall.qmd"]
	s.Contains(trailHTML, "Reasoning Path")
	s.Contains(trailHTML, "Search Trail")
	s.Contains(trailHTML, "Reusable Result")
	s.Contains(trailHTML, `href="/page/wiki%2Fconcepts%2Fsession-condense-local-memex.qmd"`)
}

func (s *MemexFacadeSuite) TestRenderHTMLRequiresWikiRoot() {
	memexFacade := facade.NewMemexFacade()

	_, err := memexFacade.Render(context.Background(), &facade.RenderMemexRequest{
		WikiRoot: filepath.Join(s.T().TempDir(), "missing"),
		Format:   facade.MemexRenderFormatHTML,
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "wiki root")
}

func (s *MemexFacadeSuite) TestRenderHTMLAcceptsDirectWikiRoot() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "wiki"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "index.qmd"),
		[]byte("Plain memex note without heading."),
		0o600,
	))
	memexFacade := facade.NewMemexFacade()

	resp, err := memexFacade.Render(context.Background(), &facade.RenderMemexRequest{
		WikiRoot: filepath.Join(root, "wiki"),
		Format:   facade.MemexRenderFormatHTML,
	})

	s.Require().NoError(err)
	s.Contains(resp.HTML, "index")
	s.Contains(resp.HTML, "Plain memex note without heading.")
	s.Empty(resp.Assets)
}

func (s *MemexFacadeSuite) TestRenderRejectsInvalidRequest() {
	memexFacade := facade.NewMemexFacade()

	_, err := memexFacade.Render(context.Background(), nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "request is required")

	_, err = memexFacade.Render(context.Background(), &facade.RenderMemexRequest{
		WikiRoot: s.T().TempDir(),
		Format:   facade.MemexRenderFormatUnknown,
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "unsupported memex render format")
}

func (s *MemexFacadeSuite) writeMemexFixture(root string) {
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "wiki", "concepts"), 0o700))
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "wiki", "trails"), 0o700))
	s.Require().NoError(os.MkdirAll(filepath.Join(root, ".llm-wiki"), 0o700))
	s.Require().NoError(os.MkdirAll(filepath.Join(root, ".llm-wiki", "recalls"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "index.qmd"),
		[]byte(
			"# Paxl LLM Wiki\n\n- [Session Condense](concepts/session-condense-local-memex.qmd)\n",
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "concepts", "session-condense-local-memex.qmd"),
		[]byte(
			"---\ntitle: \"Session Condense Local Memex\"\n---\n\n# Session Condense Local Memex\n\nFull body paragraph with durable memex context.\n\n## Related\n\n- [[memex-recall-traces]]\n",
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "concepts", "memex-recall-traces.qmd"),
		[]byte(
			"---\ntitle: \"Memex Recall Traces\"\n---\n\n# Memex Recall Traces\n\nRecall trace details.\n",
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "trails", "2026-07-01-explicit-recall.qmd"),
		[]byte(
			"---\ntype: query-trail\nquery: \"How does recall become explicit reasoning?\"\ntags: [memex, reasoning]\n---\n\n# Query Trail: Explicit Recall\n\n## Question\n\nHow should memex expose recall as reasoning?\n\n## Search Trail\n\n- Followed [[session-condense-local-memex]].\n\n## Rationale Summary\n\nExplicit graph edges make the retrieval path inspectable.\n\n## Findings\n\n- Trail pages are reusable recall paths.\n\n## Reusable Result\n\nUse query trails as named retrieval paths, not opaque ranked chunks.\n\n## Related\n\n- [[session-condense-local-memex]]\n",
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "memex.graph.json"),
		[]byte(
			`{"schemaVersion":"paxl.memex.graph.v1","nodes":[{"id":"concept-session-condense-local-memex","type":"concept","path":"wiki/concepts/session-condense-local-memex.qmd","title":"Session Condense Local Memex","summary":"Local memex maintainer.","status":"active","topics":["paxl","memex"]},{"id":"concept-memex-recall-traces","type":"concept","path":"wiki/concepts/memex-recall-traces.qmd","title":"Memex Recall Traces","summary":"Recall trace demand signals.","status":"active","topics":["paxl","recall"]},{"id":"trail-explicit-recall","type":"query-trail","path":"wiki/trails/2026-07-01-explicit-recall.qmd","title":"Explicit Recall Trail","summary":"Named retrieval path for explicit recall reasoning.","status":"active","topics":["memex","reasoning"]}],"edges":[{"source":"concept-memex-recall-traces","type":"supports","target":"concept-session-condense-local-memex"},{"source":"trail-explicit-recall","type":"supports","target":"concept-session-condense-local-memex"}]}`,
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, "wiki", "memex.graph.svg"),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>memex</text></svg>`),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".llm-wiki", "recall-index.json"),
		[]byte(
			`{"schemaVersion":"paxl.memex.recall-index.v1","traceCount":1,"traces":[{"path":".llm-wiki/recalls/explicit-recall.json","createdAt":"2026-07-01T00:00:00Z","question":"How does recall become explicit reasoning?"}],"nodes":[{"id":"concept-session-condense-local-memex","recalls":2},{"id":"trail-explicit-recall","recalls":1}],"edges":[{"source":"concept-memex-recall-traces","type":"supports","target":"concept-session-condense-local-memex","traversals":1},{"source":"trail-explicit-recall","type":"supports","target":"concept-session-condense-local-memex","traversals":2}]}`,
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".llm-wiki", "recalls", "explicit-recall.json"),
		[]byte(
			`{"schemaVersion":"paxl.memex.recall-trace.v1","createdAt":"2026-07-01T00:00:00Z","question":"How does recall become explicit reasoning?","answerSummary":"Use query trails as named retrieval paths and show the graph nodes, sources, and reusable result that produced the answer.","usedNodes":["trail-explicit-recall","concept-session-condense-local-memex"],"usedEdges":[{"source":"trail-explicit-recall","type":"supports","target":"concept-session-condense-local-memex"}],"usedTrails":["wiki/trails/2026-07-01-explicit-recall.qmd"],"answerSources":["wiki/trails/2026-07-01-explicit-recall.qmd#Reusable Result","wiki/concepts/session-condense-local-memex.qmd#Contract"],"fallbackSessionSearch":false}`,
		),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".llm-wiki", "inbox.json"),
		[]byte(`{"schemaVersion":"paxl.memex.inbox.v1","itemCount":0,"items":[]}`),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(root, ".llm-wiki", "memex-lint.json"),
		[]byte(
			`{"schemaVersion":"paxl.memex.lint.v1","issueCount":0,"errorCount":0,"warningCount":0}`,
		),
		0o600,
	))
}

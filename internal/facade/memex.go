package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxMemexArtifactBytes = 32 * 1024
	maxMemexPages         = 100
	maxMemexRecallTraces  = 20
)

type MemexRenderFormat string

const (
	MemexRenderFormatUnknown MemexRenderFormat = ""
	MemexRenderFormatHTML    MemexRenderFormat = "html"
)

type MemexFacade struct{}

type RenderMemexRequest struct {
	WikiRoot string
	Format   MemexRenderFormat
}

type RenderMemexResponse struct {
	HTML     string
	Assets   []*MemexRenderAsset
	PageHTML map[string]string
}

type MemexRenderAsset struct {
	URLPath     string
	FilePath    string
	ContentType string
}

type memexRoots struct {
	ProjectRoot string
	WikiRoot    string
	StateRoot   string
}

type memexPage struct {
	RelPath  string
	URLPath  string
	Title    string
	Preview  string
	Content  string
	NodeID   string
	NodeType string
	Summary  string
	Status   string
	Topics   []string
	Recalls  int
	HasIssue bool
	Trail    *memexTrail
	Outgoing []*memexPageEdge
	Incoming []*memexPageEdge
}

type memexTrail struct {
	Query            string
	Question         string
	SearchTrail      string
	RationaleSummary string
	Findings         string
	ReusableResult   string
}

type memexArtifact struct {
	Name    string
	RelPath string
	Pretty  string
	Counts  []string
}

type memexGraph struct {
	Nodes []*memexGraphNode `json:"nodes"`
	Edges []*memexGraphEdge `json:"edges"`
}

type memexGraphNode struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Status  string   `json:"status"`
	Topics  []string `json:"topics"`
}

type memexGraphEdge struct {
	Source string `json:"source"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

type memexRecallIndex struct {
	TraceCount int                 `json:"traceCount"`
	Nodes      []*memexRecallNode  `json:"nodes"`
	Edges      []*memexRecallEdge  `json:"edges"`
	Traces     []*memexRecallTrace `json:"traces"`
}

type memexRecallNode struct {
	ID      string `json:"id"`
	Recalls int    `json:"recalls"`
}

type memexRecallEdge struct {
	Source     string `json:"source"`
	Type       string `json:"type"`
	Target     string `json:"target"`
	Traversals int    `json:"traversals"`
}

type memexRecallTrace struct {
	Path                  string                  `json:"path"`
	Question              string                  `json:"question"`
	CreatedAt             string                  `json:"createdAt"`
	AnswerSummary         string                  `json:"answerSummary"`
	UsedNodes             []string                `json:"usedNodes"`
	UsedEdges             []*memexRecallTraceEdge `json:"usedEdges"`
	UsedTrails            []string                `json:"usedTrails"`
	AnswerSources         []string                `json:"answerSources"`
	FallbackSessionSearch bool                    `json:"fallbackSessionSearch"`
}

type memexRecallTraceEdge struct {
	Source string `json:"source"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

type memexPageEdge struct {
	Type       string
	Direction  string
	OtherTitle string
	OtherPath  string
	OtherURL   string
	Traversals int
}

func NewMemexFacade() *MemexFacade {
	return &MemexFacade{}
}

func (f *MemexFacade) Render(
	ctx context.Context,
	req *RenderMemexRequest,
	opts ...func(*Option),
) (*RenderMemexResponse, error) {
	_ = f
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("render memex request is required")
	}
	if req.Format != MemexRenderFormatHTML {
		return nil, fmt.Errorf("unsupported memex render format %q", req.Format)
	}
	roots, err := resolveMemexRoots(req.WikiRoot)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	graph := loadMemexGraph(roots)
	recallIndex := loadMemexRecallIndex(roots)
	recallTraces := collectMemexRecallTraces(roots, recallIndex)
	lintSignals := loadMemexLintSignals(roots)
	pages, err := collectMemexPages(ctx, roots, graph, recallIndex, lintSignals)
	if err != nil {
		return nil, fmt.Errorf("collect memex pages: %w", err)
	}
	attachMemexPageEdges(pages, graph, recallIndex)
	artifacts := collectMemexArtifacts(roots)
	assets := collectMemexAssets(roots)
	return &RenderMemexResponse{
		HTML: renderMemexHTML(
			roots,
			pages,
			artifacts,
			assets,
			graph,
			recallIndex,
			recallTraces,
		),
		Assets:   assets,
		PageHTML: renderMemexPageHTML(roots, pages),
	}, nil
}

func resolveMemexRoots(rawRoot string) (*memexRoots, error) {
	root := strings.TrimSpace(rawRoot)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve wiki root %q: %w", root, err)
	}
	stat, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat wiki root %q: %w", absRoot, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("wiki root %q is not a directory", absRoot)
	}

	projectWikiRoot := filepath.Join(absRoot, "wiki")
	if directoryExists(projectWikiRoot) {
		return &memexRoots{
			ProjectRoot: absRoot,
			WikiRoot:    projectWikiRoot,
			StateRoot:   filepath.Join(absRoot, ".llm-wiki"),
		}, nil
	}
	if looksLikeMemexWikiRoot(absRoot) {
		projectRoot := filepath.Dir(absRoot)
		return &memexRoots{
			ProjectRoot: projectRoot,
			WikiRoot:    absRoot,
			StateRoot:   filepath.Join(projectRoot, ".llm-wiki"),
		}, nil
	}
	return nil, fmt.Errorf("wiki root %q does not exist", projectWikiRoot)
}

func looksLikeMemexWikiRoot(root string) bool {
	return fileExists(filepath.Join(root, "index.qmd")) ||
		fileExists(filepath.Join(root, "memex.graph.json")) ||
		directoryExists(filepath.Join(root, "concepts"))
}

func loadMemexGraph(roots *memexRoots) *memexGraph {
	path := filepath.Join(roots.WikiRoot, "memex.graph.json")
	if !fileExists(path) {
		return &memexGraph{}
	}
	// #nosec G304 -- The graph path is a fixed name under the resolved local wiki root.
	raw, err := os.ReadFile(path)
	if err != nil {
		return &memexGraph{}
	}
	var graph memexGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return &memexGraph{}
	}
	return &graph
}

func loadMemexRecallIndex(roots *memexRoots) *memexRecallIndex {
	path := filepath.Join(roots.StateRoot, "recall-index.json")
	if !fileExists(path) {
		return &memexRecallIndex{}
	}
	// #nosec G304 -- The recall-index path is a fixed name under the resolved local state root.
	raw, err := os.ReadFile(path)
	if err != nil {
		return &memexRecallIndex{}
	}
	var index memexRecallIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return &memexRecallIndex{}
	}
	return &index
}

func collectMemexRecallTraces(
	roots *memexRoots,
	index *memexRecallIndex,
) []*memexRecallTrace {
	paths := memexRecallTracePaths(roots, index)
	traces := make([]*memexRecallTrace, 0, len(paths))
	byPath := memexRecallTracesByPath(index)
	for _, path := range paths {
		// #nosec G304 -- Trace paths are resolved under the local project root.
		raw, err := os.ReadFile(path)
		if err != nil {
			if trace := byPath[displayPath(roots, path)]; trace != nil {
				traces = append(traces, trace)
			}
			continue
		}
		var trace memexRecallTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			continue
		}
		relPath := displayPath(roots, path)
		indexTrace := byPath[relPath]
		if trace.Path == "" {
			trace.Path = relPath
		}
		if trace.Question == "" && indexTrace != nil {
			trace.Question = indexTrace.Question
		}
		if trace.CreatedAt == "" && indexTrace != nil {
			trace.CreatedAt = indexTrace.CreatedAt
		}
		traces = append(traces, &trace)
	}
	sort.SliceStable(traces, func(i int, j int) bool {
		return traces[i].CreatedAt > traces[j].CreatedAt
	})
	if len(traces) > maxMemexRecallTraces {
		return traces[:maxMemexRecallTraces]
	}
	return traces
}

func memexRecallTracePaths(roots *memexRoots, index *memexRecallIndex) []string {
	seen := map[string]bool{}
	var paths []string
	if index != nil {
		for _, trace := range index.Traces {
			if trace == nil || strings.TrimSpace(trace.Path) == "" {
				continue
			}
			path, ok := resolveMemexProjectPath(roots, trace.Path)
			if ok && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	if len(paths) > 0 {
		return paths
	}
	recallsRoot := filepath.Join(roots.StateRoot, "recalls")
	if !directoryExists(recallsRoot) {
		return nil
	}
	_ = filepath.WalkDir(recallsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

func memexRecallTracesByPath(index *memexRecallIndex) map[string]*memexRecallTrace {
	traces := map[string]*memexRecallTrace{}
	if index == nil {
		return traces
	}
	for _, trace := range index.Traces {
		if trace == nil || strings.TrimSpace(trace.Path) == "" {
			continue
		}
		traces[filepath.ToSlash(strings.TrimSpace(trace.Path))] = trace
	}
	return traces
}

func resolveMemexProjectPath(roots *memexRoots, rawPath string) (string, bool) {
	cleanPath := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rawPath)))
	if cleanPath == "." {
		return "", false
	}
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(roots.ProjectRoot, cleanPath)
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", false
	}
	relPath, err := filepath.Rel(roots.ProjectRoot, absPath)
	if err != nil || relPath == ".." ||
		strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", false
	}
	return absPath, true
}

func loadMemexLintSignals(roots *memexRoots) map[string]bool {
	path := filepath.Join(roots.StateRoot, "memex-lint.json")
	if !fileExists(path) {
		return map[string]bool{}
	}
	// #nosec G304 -- The lint path is a fixed name under the resolved local state root.
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]bool{}
	}
	signals := map[string]bool{}
	issues, ok := payload["issues"].([]any)
	if !ok {
		return signals
	}
	for _, item := range issues {
		issue, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"path", "nodeId", "nodeID", "node"} {
			value, ok := issue[key].(string)
			if ok && strings.TrimSpace(value) != "" {
				signals[filepath.ToSlash(strings.TrimSpace(value))] = true
			}
		}
	}
	return signals
}

func collectMemexPages(
	ctx context.Context,
	roots *memexRoots,
	graph *memexGraph,
	recallIndex *memexRecallIndex,
	lintSignals map[string]bool,
) ([]*memexPage, error) {
	pagePaths := make([]string, 0)
	err := filepath.WalkDir(
		roots.WikiRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				if path != roots.WikiRoot && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".qmd") {
				return nil
			}
			pagePaths = append(pagePaths, path)
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	sort.Strings(pagePaths)
	if len(pagePaths) > maxMemexPages {
		pagePaths = pagePaths[:maxMemexPages]
	}
	nodeByPath := memexGraphNodesByPath(graph)
	recallsByNodeID := memexRecallsByNodeID(recallIndex)
	pages := make([]*memexPage, 0, len(pagePaths))
	for _, path := range pagePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// #nosec G304 -- The path was yielded by WalkDir below the resolved local wiki root.
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read qmd page %q: %w", path, err)
		}
		relPath := displayPath(roots, path)
		node := nodeByPath[relPath]
		title := extractQMDTitle(relPath, string(raw))
		qmdType := extractQMDFrontMatterScalar(string(raw), "type")
		trail := extractMemexTrail(relPath, string(raw))
		summary := ""
		status := ""
		nodeType := ""
		nodeID := ""
		topics := extractQMDTopics(string(raw))
		if node != nil {
			nodeID = node.ID
			nodeType = node.Type
			if node.Title != "" {
				title = node.Title
			}
			summary = node.Summary
			status = node.Status
			if len(node.Topics) > 0 {
				topics = node.Topics
			}
		}
		if nodeType == "" {
			nodeType = qmdType
		}
		if trail != nil && nodeType == "" {
			nodeType = "query-trail"
		}
		pages = append(pages, &memexPage{
			RelPath:  relPath,
			URLPath:  memexPageURL(relPath),
			Title:    title,
			Preview:  extractQMDPreview(string(raw)),
			Content:  string(raw),
			NodeID:   nodeID,
			NodeType: nodeType,
			Summary:  summary,
			Status:   status,
			Topics:   topics,
			Recalls:  recallsByNodeID[nodeID],
			HasIssue: lintSignals[relPath] || lintSignals[nodeID],
			Trail:    trail,
		})
	}
	sort.SliceStable(pages, func(i int, j int) bool {
		return pages[i].RelPath < pages[j].RelPath
	})
	return pages, nil
}

func attachMemexPageEdges(
	pages []*memexPage,
	graph *memexGraph,
	recallIndex *memexRecallIndex,
) {
	pageByNodeID := map[string]*memexPage{}
	for _, page := range pages {
		if page.NodeID != "" {
			pageByNodeID[page.NodeID] = page
		}
	}
	traversalsByEdge := memexTraversalsByEdge(recallIndex)
	for _, edge := range graph.Edges {
		source := pageByNodeID[edge.Source]
		target := pageByNodeID[edge.Target]
		if source == nil || target == nil {
			continue
		}
		traversals := traversalsByEdge[memexEdgeKey(edge.Source, edge.Type, edge.Target)]
		source.Outgoing = append(source.Outgoing, &memexPageEdge{
			Type:       edge.Type,
			Direction:  "outgoing",
			OtherTitle: target.Title,
			OtherPath:  target.RelPath,
			OtherURL:   target.URLPath,
			Traversals: traversals,
		})
		target.Incoming = append(target.Incoming, &memexPageEdge{
			Type:       edge.Type,
			Direction:  "incoming",
			OtherTitle: source.Title,
			OtherPath:  source.RelPath,
			OtherURL:   source.URLPath,
			Traversals: traversals,
		})
	}
	for _, page := range pages {
		sortMemexPageEdges(page.Outgoing)
		sortMemexPageEdges(page.Incoming)
	}
}

func memexGraphNodesByPath(graph *memexGraph) map[string]*memexGraphNode {
	nodes := map[string]*memexGraphNode{}
	if graph == nil {
		return nodes
	}
	for _, node := range graph.Nodes {
		if node == nil || strings.TrimSpace(node.Path) == "" {
			continue
		}
		nodes[filepath.ToSlash(strings.TrimSpace(node.Path))] = node
	}
	return nodes
}

func memexRecallsByNodeID(index *memexRecallIndex) map[string]int {
	recalls := map[string]int{}
	if index == nil {
		return recalls
	}
	for _, node := range index.Nodes {
		if node == nil || node.ID == "" {
			continue
		}
		recalls[node.ID] = node.Recalls
	}
	return recalls
}

func memexTraversalsByEdge(index *memexRecallIndex) map[string]int {
	traversals := map[string]int{}
	if index == nil {
		return traversals
	}
	for _, edge := range index.Edges {
		if edge == nil {
			continue
		}
		traversals[memexEdgeKey(edge.Source, edge.Type, edge.Target)] = edge.Traversals
	}
	return traversals
}

func memexEdgeKey(source string, edgeType string, target string) string {
	return source + "\x00" + edgeType + "\x00" + target
}

func sortMemexPageEdges(edges []*memexPageEdge) {
	sort.SliceStable(edges, func(i int, j int) bool {
		if edges[i].Traversals != edges[j].Traversals {
			return edges[i].Traversals > edges[j].Traversals
		}
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		return edges[i].OtherTitle < edges[j].OtherTitle
	})
}

func memexPageURL(relPath string) string {
	return "/page/" + url.PathEscape(filepath.ToSlash(relPath))
}

func collectMemexArtifacts(roots *memexRoots) []*memexArtifact {
	specs := []struct {
		name string
		path string
	}{
		{name: "memex.graph.json", path: filepath.Join(roots.WikiRoot, "memex.graph.json")},
		{name: "recall-index.json", path: filepath.Join(roots.StateRoot, "recall-index.json")},
		{name: "inbox.json", path: filepath.Join(roots.StateRoot, "inbox.json")},
		{name: "memex-lint.json", path: filepath.Join(roots.StateRoot, "memex-lint.json")},
		{
			name: "memex-visualization.json",
			path: filepath.Join(roots.StateRoot, "memex-visualization.json"),
		},
	}
	artifacts := make([]*memexArtifact, 0, len(specs))
	for _, spec := range specs {
		if !fileExists(spec.path) {
			continue
		}
		// #nosec G304 -- Artifact paths are fixed names under resolved local memex roots.
		raw, err := os.ReadFile(spec.path)
		if err != nil {
			artifacts = append(artifacts, &memexArtifact{
				Name:    spec.name,
				RelPath: displayPath(roots, spec.path),
				Pretty:  fmt.Sprintf("Unable to read artifact: %v", err),
			})
			continue
		}
		artifacts = append(artifacts, &memexArtifact{
			Name:    spec.name,
			RelPath: displayPath(roots, spec.path),
			Pretty:  prettyMemexJSON(raw),
			Counts:  summarizeMemexJSON(spec.name, raw),
		})
	}
	return artifacts
}

func collectMemexAssets(roots *memexRoots) []*MemexRenderAsset {
	graphSVG := filepath.Join(roots.WikiRoot, "memex.graph.svg")
	if !fileExists(graphSVG) {
		return nil
	}
	return []*MemexRenderAsset{
		{
			URLPath:     "/assets/memex.graph.svg",
			FilePath:    graphSVG,
			ContentType: "image/svg+xml",
		},
	}
}

func extractQMDTitle(relPath string, content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, line := range lines[1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "---" {
				break
			}
			if strings.HasPrefix(trimmed, "title:") {
				return cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "title:")))
			}
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func extractQMDTopics(content string) []string {
	for _, key := range []string{"topics", "tags"} {
		topics := parseQMDStringList(extractQMDFrontMatterScalar(content, key))
		if len(topics) > 0 {
			return topics
		}
	}
	return nil
}

func extractQMDFrontMatterScalar(content string, key string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			return ""
		}
		prefix := key + ":"
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		return cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)))
	}
	return ""
}

func parseQMDStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.Trim(raw, "[]")
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := cleanYAMLScalar(strings.TrimSpace(part))
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func extractMemexTrail(relPath string, content string) *memexTrail {
	qmdType := extractQMDFrontMatterScalar(content, "type")
	if qmdType != "query-trail" && !strings.HasPrefix(relPath, "wiki/trails/") {
		return nil
	}
	sections := extractQMDSections(content)
	return &memexTrail{
		Query: firstNonEmptyString(
			extractQMDFrontMatterScalar(content, "query"),
			sections["question"],
		),
		Question:         sections["question"],
		SearchTrail:      sections["search trail"],
		RationaleSummary: sections["rationale summary"],
		Findings:         sections["findings"],
		ReusableResult:   sections["reusable result"],
	}
}

func extractQMDSections(content string) map[string]string {
	sections := map[string][]string{}
	current := ""
	lines := strings.Split(stripQMDFrontMatter(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if level := qmdHeadingLevel(trimmed); level > 0 && level <= 2 {
			current = normalizeQMDSectionTitle(strings.TrimSpace(trimmed[level:]))
			if _, ok := sections[current]; !ok {
				sections[current] = nil
			}
			continue
		}
		if current == "" {
			continue
		}
		sections[current] = append(sections[current], line)
	}
	output := make(map[string]string, len(sections))
	for key, lines := range sections {
		output[key] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return output
}

func normalizeQMDSectionTitle(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func extractQMDPreview(content string) string {
	lines := strings.Split(content, "\n")
	inFrontMatter := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if index == 0 && trimmed == "---" {
			inFrontMatter = true
			continue
		}
		if inFrontMatter {
			if trimmed == "---" {
				inFrontMatter = false
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return truncateRunes(trimmed, 220)
	}
	return ""
}

func cleanYAMLScalar(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), `"'`)
}

func prettyMemexJSON(raw []byte) string {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, raw, "", "  "); err == nil {
		return truncateRunes(buffer.String(), maxMemexArtifactBytes)
	}
	return truncateRunes(string(raw), maxMemexArtifactBytes)
}

func summarizeMemexJSON(name string, raw []byte) []string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	switch name {
	case "memex.graph.json":
		return countLabels(payload, "nodes", "edges")
	case "recall-index.json":
		return countLabels(payload, "traceCount", "queryCount", "trailCount")
	case "inbox.json":
		return countLabels(payload, "itemCount", "items")
	case "memex-lint.json":
		return countLabels(payload, "issueCount", "errorCount", "warningCount")
	default:
		return countLabels(payload, "nodeCount", "edgeCount", "itemCount")
	}
}

func countLabels(payload map[string]any, keys ...string) []string {
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		count, ok := readJSONCount(payload[key])
		if !ok {
			continue
		}
		labels = append(labels, fmt.Sprintf("%s: %d", key, count))
	}
	return labels
}

func readJSONCount(value any) (int, bool) {
	switch typed := value.(type) {
	case []any:
		return len(typed), true
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func renderMemexHTML(
	roots *memexRoots,
	pages []*memexPage,
	artifacts []*memexArtifact,
	assets []*MemexRenderAsset,
	graph *memexGraph,
	recallIndex *memexRecallIndex,
	recallTraces []*memexRecallTrace,
) string {
	trails := memexTrailPages(pages)
	var builder strings.Builder
	writeMemexHTMLHead(&builder, "Paxl Memex")
	builder.WriteString(`<body><header class="hero"><div class="hero-main"><div>`)
	builder.WriteString(`<p class="eyebrow">Local Knowledge Graph</p><h1>Paxl Memex</h1>`)
	builder.WriteString(
		`<p class="hero-copy">Browse the maintained qmd wiki, graph links, recall demand signals, and private maintenance artifacts from <code>`,
	)
	builder.WriteString(escapeHTML(displayPath(roots, roots.ProjectRoot)))
	builder.WriteString(`</code>.</p></div>`)
	builder.WriteString(
		`<nav class="hero-nav"><a href="#trails">Trails</a><a href="#reasoning">Reasoning</a><a href="#pages">Pages</a><a href="#graph">Graph</a><a href="#artifacts">Artifacts</a></nav>`,
	)
	builder.WriteString(`</div><div class="metric-grid">`)
	writeMemexMetric(&builder, "Pages", len(pages), "qmd documents")
	writeMemexMetric(&builder, "Query Trails", len(trails), "reusable paths")
	writeMemexMetric(&builder, "Graph Nodes", len(graph.Nodes), "reader-facing nodes")
	writeMemexMetric(&builder, "Edges", len(graph.Edges), "typed relationships")
	writeMemexMetric(&builder, "Recall Traces", recallIndex.TraceCount, "query demand")
	builder.WriteString(`</div></header><main class="shell">`)
	builder.WriteString(`<section id="graph" class="overview-grid">`)
	if len(assets) > 0 {
		builder.WriteString(
			`<div class="panel graph-panel"><div class="section-head"><h2>Graph</h2><p>Node size and edge weight come from recall usage.</p></div><img class="graph" src="`,
		)
		builder.WriteString(escapeHTML(assets[0].URLPath))
		builder.WriteString(`" alt="Memex graph"></div>`)
	} else {
		builder.WriteString(`<div class="panel graph-panel"><div class="section-head">`)
		builder.WriteString(`<h2>Graph</h2><p>No graph SVG found.</p></div></div>`)
	}
	builder.WriteString(
		`<div class="panel"><div class="section-head"><h2>Recall Hotspots</h2><p>Pages repeatedly used by recall should be kept sharp.</p></div>`,
	)
	writeMemexHotPages(&builder, pages)
	builder.WriteString(`</div></section>`)
	writeMemexTrailSection(&builder, trails)
	writeMemexReasoningPaths(&builder, pages, recallTraces)
	builder.WriteString(
		`<section id="pages"><div class="section-head"><h2>Pages</h2><p>Open a page to inspect full qmd content, backlinks, and graph edges.</p></div><div class="pages">`,
	)
	if len(pages) == 0 {
		builder.WriteString(`<p class="muted">No qmd pages found.</p>`)
	}
	for _, page := range pages {
		writeMemexPageCard(&builder, page)
	}
	builder.WriteString(`</div></section>`)
	builder.WriteString(
		`<section id="artifacts"><div class="section-head"><h2>Artifacts</h2><p>Private maintenance state is collapsed by default.</p></div>`,
	)
	if len(artifacts) == 0 {
		builder.WriteString(`<p class="muted">No memex JSON artifacts found.</p>`)
	}
	for _, artifact := range artifacts {
		writeMemexArtifact(&builder, artifact)
	}
	builder.WriteString(`</section></main></body></html>`)
	return builder.String()
}

func renderMemexPageHTML(roots *memexRoots, pages []*memexPage) map[string]string {
	output := make(map[string]string, len(pages))
	links := memexPageLinks(pages)
	for _, page := range pages {
		output[page.URLPath] = renderMemexPage(roots, page, links)
	}
	return output
}

func renderMemexPage(
	roots *memexRoots,
	page *memexPage,
	links map[string]string,
) string {
	var builder strings.Builder
	writeMemexHTMLHead(&builder, page.Title)
	builder.WriteString(
		`<body><header class="hero compact"><div class="hero-main"><div><p class="eyebrow"><a href="/">Paxl Memex</a> / Page</p><h1>`,
	)
	builder.WriteString(escapeHTML(page.Title))
	builder.WriteString(`</h1><p class="hero-copy"><code>`)
	builder.WriteString(escapeHTML(page.RelPath))
	builder.WriteString(`</code></p></div></div>`)
	builder.WriteString(`<div class="meta-row">`)
	writeMemexBadge(&builder, firstNonEmptyString(page.NodeType, "qmd"), "type")
	if page.Status != "" {
		writeMemexBadge(&builder, page.Status, "status")
	}
	if page.Recalls > 0 {
		writeMemexBadge(&builder, fmt.Sprintf("%d recalls", page.Recalls), "hot")
	}
	if page.HasIssue {
		writeMemexBadge(&builder, "lint issue", "warn")
	}
	builder.WriteString(`</div>`)
	if len(page.Topics) > 0 {
		writeMemexTags(&builder, page.Topics)
	}
	builder.WriteString(`</header><main class="shell detail-grid">`)
	builder.WriteString(`<article class="document panel">`)
	if page.Summary != "" {
		builder.WriteString(`<p class="summary">`)
		builder.WriteString(escapeHTML(page.Summary))
		builder.WriteString(`</p>`)
	}
	writeMemexTrailPath(&builder, page, links)
	builder.WriteString(renderQMDContent(page.Content, links))
	builder.WriteString(`</article><aside class="side">`)
	builder.WriteString(
		`<section class="panel"><div class="section-head"><h2>Related</h2><p>Graph edges and backlinks for this page.</p></div>`,
	)
	writeMemexEdgeList(&builder, "Outgoing", page.Outgoing)
	writeMemexEdgeList(&builder, "Backlinks", page.Incoming)
	builder.WriteString(`</section><details class="panel"><summary>Raw qmd</summary><pre>`)
	builder.WriteString(escapeHTML(page.Content))
	builder.WriteString(`</pre></details></aside></main></body></html>`)
	_ = roots
	return builder.String()
}

func memexPageLinks(pages []*memexPage) map[string]string {
	links := map[string]string{}
	for _, page := range pages {
		if page.URLPath == "" {
			continue
		}
		for _, value := range []string{
			page.RelPath,
			strings.TrimPrefix(page.RelPath, "wiki/"),
			strings.TrimSuffix(page.RelPath, filepath.Ext(page.RelPath)),
			strings.TrimSuffix(filepath.Base(page.RelPath), filepath.Ext(page.RelPath)),
			page.Title,
			slugifyMemexLink(page.Title),
		} {
			key := normalizeMemexLinkTarget(value)
			if key != "" {
				links[key] = page.URLPath
			}
		}
	}
	return links
}

func writeMemexHTMLHead(builder *strings.Builder, title string) {
	builder.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	builder.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	builder.WriteString(`<title>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</title><style>`)
	builder.WriteString(memexRenderCSS())
	builder.WriteString(`</style></head>`)
}

func writeMemexPageCard(builder *strings.Builder, page *memexPage) {
	builder.WriteString(`<article class="page-card">`)
	builder.WriteString(`<div class="card-top">`)
	writeMemexBadge(builder, firstNonEmptyString(page.NodeType, "qmd"), "type")
	if page.Status != "" {
		writeMemexBadge(builder, page.Status, "status")
	}
	if page.HasIssue {
		writeMemexBadge(builder, "lint", "warn")
	}
	builder.WriteString(`</div><h3><a href="`)
	builder.WriteString(escapeHTML(page.URLPath))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(page.Title))
	builder.WriteString(`</a></h3><p class="path"><code>`)
	builder.WriteString(escapeHTML(page.RelPath))
	builder.WriteString(`</code></p>`)
	summary := firstNonEmptyString(page.Summary, page.Preview)
	if summary != "" {
		builder.WriteString(`<p>`)
		builder.WriteString(escapeHTML(summary))
		builder.WriteString(`</p>`)
	}
	builder.WriteString(`<div class="mini-metrics">`)
	writeMemexMiniMetric(builder, "Recalls", page.Recalls)
	writeMemexMiniMetric(builder, "Links", len(page.Outgoing)+len(page.Incoming))
	builder.WriteString(`</div>`)
	writeMemexTags(builder, page.Topics)
	builder.WriteString(`</article>`)
}

func memexTrailPages(pages []*memexPage) []*memexPage {
	trails := make([]*memexPage, 0)
	for _, page := range pages {
		if page == nil || page.Trail == nil {
			continue
		}
		trails = append(trails, page)
	}
	sort.SliceStable(trails, func(i int, j int) bool {
		if trails[i].Recalls != trails[j].Recalls {
			return trails[i].Recalls > trails[j].Recalls
		}
		return trails[i].Title < trails[j].Title
	})
	return trails
}

func writeMemexTrailSection(builder *strings.Builder, trails []*memexPage) {
	builder.WriteString(
		`<section id="trails"><div class="section-head"><h2>Query Trails</h2><p>Named retrieval paths with reusable answers and explicit side links.</p></div>`,
	)
	if len(trails) == 0 {
		builder.WriteString(`<p class="muted">No query trails found.</p></section>`)
		return
	}
	builder.WriteString(`<div class="trail-list">`)
	for _, page := range trails {
		writeMemexTrailCard(builder, page)
	}
	builder.WriteString(`</div></section>`)
}

func writeMemexTrailCard(builder *strings.Builder, page *memexPage) {
	builder.WriteString(`<article class="trail-card">`)
	builder.WriteString(
		`<div class="path-flow"><span>Question</span><span>Search Trail</span><span>Reusable Result</span></div>`,
	)
	builder.WriteString(`<h3><a href="`)
	builder.WriteString(escapeHTML(page.URLPath))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(page.Title))
	builder.WriteString(`</a></h3>`)
	if page.Trail.Query != "" {
		builder.WriteString(`<p class="trail-query">`)
		builder.WriteString(escapeHTML(page.Trail.Query))
		builder.WriteString(`</p>`)
	}
	summary := firstNonEmptyString(page.Trail.ReusableResult, page.Summary, page.Preview)
	if summary != "" {
		builder.WriteString(`<p>`)
		builder.WriteString(escapeHTML(truncateRunes(collapseWhitespace(summary), 260)))
		builder.WriteString(`</p>`)
	}
	builder.WriteString(`<div class="mini-metrics">`)
	writeMemexMiniMetric(builder, "Recalls", page.Recalls)
	writeMemexMiniMetric(builder, "Graph Links", len(page.Outgoing)+len(page.Incoming))
	builder.WriteString(`</div>`)
	writeMemexTags(builder, page.Topics)
	builder.WriteString(`</article>`)
}

func writeMemexReasoningPaths(
	builder *strings.Builder,
	pages []*memexPage,
	traces []*memexRecallTrace,
) {
	builder.WriteString(
		`<section id="reasoning"><div class="section-head"><h2>Reasoning Paths</h2><p>Recall traces show the explicit retrieval path used to answer a query.</p></div>`,
	)
	if len(traces) == 0 {
		builder.WriteString(`<p class="muted">No recall traces found.</p></section>`)
		return
	}
	pageByNodeID := memexPagesByNodeID(pages)
	pageByPath := memexPagesByRelPath(pages)
	builder.WriteString(`<div class="reasoning-list">`)
	for _, trace := range traces {
		writeMemexReasoningPath(builder, trace, pageByNodeID, pageByPath)
	}
	builder.WriteString(`</div></section>`)
}

func writeMemexReasoningPath(
	builder *strings.Builder,
	trace *memexRecallTrace,
	pageByNodeID map[string]*memexPage,
	pageByPath map[string]*memexPage,
) {
	builder.WriteString(`<article class="recall-path">`)
	builder.WriteString(`<div class="card-top">`)
	writeMemexBadge(builder, "recall trace", "type")
	if trace.FallbackSessionSearch {
		writeMemexBadge(builder, "fallback session search", "warn")
	}
	if trace.CreatedAt != "" {
		writeMemexBadge(builder, trace.CreatedAt, "status")
	}
	builder.WriteString(`</div>`)
	writeMemexPathStepText(builder, "Entry Question", trace.Question)
	writeMemexPathStepLinks(builder, "Reused Trail", trace.UsedTrails, pageByNodeID, pageByPath)
	writeMemexPathStepLinks(builder, "Used Nodes", trace.UsedNodes, pageByNodeID, pageByPath)
	writeMemexPathStepEdges(builder, "Traversed Edges", trace.UsedEdges, pageByNodeID)
	writeMemexPathStepLinks(
		builder,
		"Answer Sources",
		trace.AnswerSources,
		pageByNodeID,
		pageByPath,
	)
	writeMemexPathStepText(builder, "Answer Summary", trace.AnswerSummary)
	builder.WriteString(`</article>`)
}

func writeMemexPathStepText(builder *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	builder.WriteString(`<div class="path-step"><strong>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</strong>`)
	if value == "" {
		builder.WriteString(`<p class="muted">None recorded.</p></div>`)
		return
	}
	builder.WriteString(`<p>`)
	builder.WriteString(escapeHTML(value))
	builder.WriteString(`</p></div>`)
}

func writeMemexPathStepLinks(
	builder *strings.Builder,
	label string,
	values []string,
	pageByNodeID map[string]*memexPage,
	pageByPath map[string]*memexPage,
) {
	builder.WriteString(`<div class="path-step"><strong>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</strong>`)
	if len(values) == 0 {
		builder.WriteString(`<p class="muted">None recorded.</p></div>`)
		return
	}
	builder.WriteString(`<ul>`)
	for _, value := range values {
		builder.WriteString(`<li>`)
		writeMemexResolvedLink(builder, value, pageByNodeID, pageByPath)
		builder.WriteString(`</li>`)
	}
	builder.WriteString(`</ul></div>`)
}

func writeMemexPathStepEdges(
	builder *strings.Builder,
	label string,
	edges []*memexRecallTraceEdge,
	pageByNodeID map[string]*memexPage,
) {
	builder.WriteString(`<div class="path-step"><strong>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</strong>`)
	if len(edges) == 0 {
		builder.WriteString(`<p class="muted">None recorded.</p></div>`)
		return
	}
	builder.WriteString(`<ul>`)
	for _, edge := range edges {
		source := memexNodeLabel(edge.Source, pageByNodeID)
		target := memexNodeLabel(edge.Target, pageByNodeID)
		builder.WriteString(`<li>`)
		builder.WriteString(source)
		builder.WriteString(` <span>`)
		builder.WriteString(escapeHTML(edge.Type))
		builder.WriteString(`</span> `)
		builder.WriteString(target)
		builder.WriteString(`</li>`)
	}
	builder.WriteString(`</ul></div>`)
}

func writeMemexResolvedLink(
	builder *strings.Builder,
	value string,
	pageByNodeID map[string]*memexPage,
	pageByPath map[string]*memexPage,
) {
	value = strings.TrimSpace(value)
	sourcePath, sourceAnchor, _ := strings.Cut(value, "#")
	page := pageByNodeID[value]
	if page == nil {
		page = pageByPath[filepath.ToSlash(sourcePath)]
	}
	if page == nil {
		builder.WriteString(`<code>`)
		builder.WriteString(escapeHTML(value))
		builder.WriteString(`</code>`)
		return
	}
	builder.WriteString(`<a href="`)
	builder.WriteString(escapeHTML(page.URLPath))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(page.Title))
	builder.WriteString(`</a>`)
	if sourceAnchor != "" {
		builder.WriteString(` <code>#`)
		builder.WriteString(escapeHTML(sourceAnchor))
		builder.WriteString(`</code>`)
	}
}

func memexNodeLabel(nodeID string, pageByNodeID map[string]*memexPage) string {
	page := pageByNodeID[nodeID]
	if page == nil {
		return `<code>` + escapeHTML(nodeID) + `</code>`
	}
	var builder strings.Builder
	builder.WriteString(`<a href="`)
	builder.WriteString(escapeHTML(page.URLPath))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(page.Title))
	builder.WriteString(`</a>`)
	return builder.String()
}

func writeMemexTrailPath(builder *strings.Builder, page *memexPage, links map[string]string) {
	if page.Trail == nil {
		return
	}
	builder.WriteString(
		`<section class="trail-path"><div class="section-head"><h2>Reasoning Path</h2>`,
	)
	builder.WriteString(
		`<p>This trail stores the visible retrieval path, not an opaque similarity result.</p></div>`,
	)
	builder.WriteString(`<ol class="path-steps">`)
	writeMemexTrailStep(
		builder,
		"Question",
		firstNonEmptyString(page.Trail.Question, page.Trail.Query),
		links,
	)
	writeMemexTrailStep(builder, "Search Trail", page.Trail.SearchTrail, links)
	writeMemexTrailStep(builder, "Rationale Summary", page.Trail.RationaleSummary, links)
	writeMemexTrailStep(builder, "Findings", page.Trail.Findings, links)
	writeMemexTrailStep(builder, "Reusable Result", page.Trail.ReusableResult, links)
	builder.WriteString(`</ol></section>`)
}

func writeMemexTrailStep(
	builder *strings.Builder,
	label string,
	content string,
	links map[string]string,
) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	builder.WriteString(`<li><strong>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</strong>`)
	builder.WriteString(renderQMDContent(content, links))
	builder.WriteString(`</li>`)
}

func memexPagesByNodeID(pages []*memexPage) map[string]*memexPage {
	output := map[string]*memexPage{}
	for _, page := range pages {
		if page.NodeID != "" {
			output[page.NodeID] = page
		}
	}
	return output
}

func memexPagesByRelPath(pages []*memexPage) map[string]*memexPage {
	output := map[string]*memexPage{}
	for _, page := range pages {
		output[page.RelPath] = page
	}
	return output
}

func writeMemexHotPages(builder *strings.Builder, pages []*memexPage) {
	hotPages := append([]*memexPage(nil), pages...)
	sort.SliceStable(hotPages, func(i int, j int) bool {
		if hotPages[i].Recalls != hotPages[j].Recalls {
			return hotPages[i].Recalls > hotPages[j].Recalls
		}
		return hotPages[i].Title < hotPages[j].Title
	})
	wrote := false
	builder.WriteString(`<div class="hot-list">`)
	for _, page := range hotPages {
		if page.Recalls == 0 {
			continue
		}
		wrote = true
		builder.WriteString(`<a class="hot-item" href="`)
		builder.WriteString(escapeHTML(page.URLPath))
		builder.WriteString(`"><span>`)
		builder.WriteString(escapeHTML(page.Title))
		builder.WriteString(`</span><strong>`)
		_, _ = fmt.Fprintf(builder, "%d", page.Recalls)
		builder.WriteString(`</strong></a>`)
	}
	if !wrote {
		builder.WriteString(`<p class="muted">No recall traces have targeted qmd pages yet.</p>`)
	}
	builder.WriteString(`</div>`)
}

func writeMemexMetric(builder *strings.Builder, label string, count int, detail string) {
	builder.WriteString(`<div class="metric"><span>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</span><strong>`)
	_, _ = fmt.Fprintf(builder, "%d", count)
	builder.WriteString(`</strong><em>`)
	builder.WriteString(escapeHTML(detail))
	builder.WriteString(`</em></div>`)
}

func writeMemexMiniMetric(builder *strings.Builder, label string, count int) {
	builder.WriteString(`<span><strong>`)
	_, _ = fmt.Fprintf(builder, "%d", count)
	builder.WriteString(`</strong> `)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</span>`)
}

func writeMemexBadge(builder *strings.Builder, value string, className string) {
	builder.WriteString(`<span class="tag `)
	builder.WriteString(escapeHTML(className))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(value))
	builder.WriteString(`</span>`)
}

func writeMemexTags(builder *strings.Builder, topics []string) {
	if len(topics) == 0 {
		return
	}
	builder.WriteString(`<div class="tag-row">`)
	for _, topic := range topics {
		writeMemexBadge(builder, topic, "topic")
	}
	builder.WriteString(`</div>`)
}

func writeMemexArtifact(builder *strings.Builder, artifact *memexArtifact) {
	builder.WriteString(`<details class="artifact"><summary><span>`)
	builder.WriteString(escapeHTML(artifact.Name))
	builder.WriteString(`</span><code>`)
	builder.WriteString(escapeHTML(artifact.RelPath))
	builder.WriteString(`</code>`)
	if len(artifact.Counts) > 0 {
		builder.WriteString(`<em>`)
		builder.WriteString(escapeHTML(strings.Join(artifact.Counts, ", ")))
		builder.WriteString(`</em>`)
	}
	builder.WriteString(`</summary><pre>`)
	builder.WriteString(escapeHTML(artifact.Pretty))
	builder.WriteString(`</pre></details>`)
}

func writeMemexEdgeList(builder *strings.Builder, title string, edges []*memexPageEdge) {
	builder.WriteString(`<h3>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</h3>`)
	if len(edges) == 0 {
		builder.WriteString(`<p class="muted">None.</p>`)
		return
	}
	builder.WriteString(`<ul class="edge-list">`)
	for _, edge := range edges {
		builder.WriteString(`<li><a href="`)
		builder.WriteString(escapeHTML(edge.OtherURL))
		builder.WriteString(`">`)
		builder.WriteString(escapeHTML(edge.OtherTitle))
		builder.WriteString(`</a><span>`)
		builder.WriteString(escapeHTML(edge.Type))
		if edge.Traversals > 0 {
			builder.WriteString(`, `)
			_, _ = fmt.Fprintf(builder, "%d traversals", edge.Traversals)
		}
		builder.WriteString(`</span></li>`)
	}
	builder.WriteString(`</ul>`)
}

func renderQMDContent(content string, links map[string]string) string {
	lines := strings.Split(stripQMDFrontMatter(content), "\n")
	var builder strings.Builder
	var paragraph []string
	inList := false
	inCode := false
	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		builder.WriteString(`<p>`)
		builder.WriteString(renderInlineQMD(strings.Join(paragraph, " "), links))
		builder.WriteString(`</p>`)
		paragraph = nil
	}
	closeList := func() {
		if inList {
			builder.WriteString(`</ul>`)
			inList = false
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flushParagraph()
			closeList()
			if inCode {
				builder.WriteString(`</code></pre>`)
				inCode = false
			} else {
				builder.WriteString(`<pre><code>`)
				inCode = true
			}
			continue
		}
		if inCode {
			builder.WriteString(escapeHTML(line))
			builder.WriteString("\n")
			continue
		}
		if trimmed == "" {
			flushParagraph()
			closeList()
			continue
		}
		if headingLevel := qmdHeadingLevel(trimmed); headingLevel > 0 {
			flushParagraph()
			closeList()
			text := strings.TrimSpace(trimmed[headingLevel:])
			if text != "" {
				_, _ = fmt.Fprintf(&builder, "<h%d>", headingLevel)
				builder.WriteString(renderInlineQMD(text, links))
				_, _ = fmt.Fprintf(&builder, "</h%d>", headingLevel)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			flushParagraph()
			if !inList {
				builder.WriteString(`<ul>`)
				inList = true
			}
			builder.WriteString(`<li>`)
			builder.WriteString(
				renderInlineQMD(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), links),
			)
			builder.WriteString(`</li>`)
			continue
		}
		closeList()
		paragraph = append(paragraph, trimmed)
	}
	flushParagraph()
	closeList()
	if inCode {
		builder.WriteString(`</code></pre>`)
	}
	return builder.String()
}

func stripQMDFrontMatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	for index, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return strings.Join(lines[index+2:], "\n")
		}
	}
	return content
}

func qmdHeadingLevel(trimmed string) int {
	level := 0
	for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0
	}
	return level
}

func renderInlineQMD(value string, links map[string]string) string {
	parts := strings.Split(value, "`")
	var builder strings.Builder
	for index, part := range parts {
		if index%2 == 1 {
			builder.WriteString(`<code>`)
			builder.WriteString(escapeHTML(part))
			builder.WriteString(`</code>`)
			continue
		}
		builder.WriteString(renderWikiLinks(part, links))
	}
	return builder.String()
}

func renderWikiLinks(value string, links map[string]string) string {
	var builder strings.Builder
	remaining := value
	for {
		start := strings.Index(remaining, "[[")
		if start < 0 {
			builder.WriteString(escapeHTML(remaining))
			return builder.String()
		}
		builder.WriteString(escapeHTML(remaining[:start]))
		afterStart := remaining[start+2:]
		end := strings.Index(afterStart, "]]")
		if end < 0 {
			builder.WriteString(escapeHTML(remaining[start:]))
			return builder.String()
		}
		rawTarget := afterStart[:end]
		target, label := splitMemexWikiLink(rawTarget)
		href := links[normalizeMemexLinkTarget(target)]
		if href == "" {
			builder.WriteString(`<span class="broken-wikilink">[[`)
			builder.WriteString(escapeHTML(label))
			builder.WriteString(`]]</span>`)
		} else {
			builder.WriteString(`<a class="wikilink" href="`)
			builder.WriteString(escapeHTML(href))
			builder.WriteString(`">`)
			builder.WriteString(escapeHTML(label))
			builder.WriteString(`</a>`)
		}
		remaining = afterStart[end+2:]
	}
}

func splitMemexWikiLink(raw string) (string, string) {
	parts := strings.SplitN(raw, "|", 2)
	target := strings.TrimSpace(parts[0])
	label := target
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		label = strings.TrimSpace(parts[1])
	}
	return target, label
}

func normalizeMemexLinkTarget(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	if before, _, ok := strings.Cut(value, "#"); ok {
		value = before
	}
	value = filepath.ToSlash(value)
	value = strings.TrimSuffix(value, ".qmd")
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	return strings.ToLower(value)
}

func slugifyMemexLink(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func memexRenderCSS() string {
	return `
:root{color-scheme:light;--bg:#f4f5f2;--panel:#ffffff;--ink:#18201c;--muted:#647067;--line:#d9dfd8;--accent:#0a6f74;--blue:#1f5f99;--amber:#9b6a00;--red:#a13d31}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.55 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}
code{background:#eef1ee;border:1px solid var(--line);border-radius:4px;padding:1px 4px}
.hero{background:linear-gradient(180deg,#ffffff 0%,#f9faf8 100%);border-bottom:1px solid var(--line);padding:26px 30px}
.hero.compact{padding-bottom:20px}
.hero-main{max-width:1240px;margin:0 auto;display:flex;align-items:flex-start;justify-content:space-between;gap:24px}
.hero>.meta-row,.hero>.tag-row{max-width:1240px;margin:12px auto 0}
.hero>.tag-row{margin-top:8px}
.eyebrow{margin:0 0 6px;color:var(--accent);font-weight:700;text-transform:uppercase;font-size:12px;letter-spacing:.04em}
h1{font-size:34px;line-height:1.1;margin:0 0 8px}
.hero-copy{max-width:760px;margin:0;color:var(--muted)}
.hero-nav{display:flex;gap:8px;flex-wrap:wrap}
.hero-nav a{border:1px solid var(--line);border-radius:6px;background:#fff;padding:7px 10px;color:var(--ink)}
.shell{max-width:1240px;margin:0 auto;padding:24px 30px 46px}
.metric-grid{max-width:1240px;margin:18px auto 0;display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:10px}
.metric,.panel,.page-card,.artifact,.trail-card,.recall-path{background:var(--panel);border:1px solid var(--line);border-radius:8px;box-shadow:0 1px 2px rgba(21,27,24,.04)}
.metric{padding:13px 14px}
.metric span,.metric em{display:block;color:var(--muted);font-style:normal}
.metric strong{display:block;font-size:26px;line-height:1.15}
.overview-grid{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(280px,.8fr);gap:14px}
.panel{padding:16px}
.section-head{margin-bottom:12px}
.section-head h2{font-size:20px;margin:0 0 4px}
.section-head p{margin:0;color:var(--muted)}
.graph{width:100%;max-height:560px;object-fit:contain;background:#fff;border:1px solid var(--line);border-radius:6px}
.pages{display:grid;grid-template-columns:repeat(auto-fit,minmax(310px,1fr));gap:12px}
.page-card,.trail-card,.recall-path{padding:14px;display:flex;flex-direction:column;gap:8px}
.card-top,.meta-row,.tag-row,.mini-metrics{display:flex;gap:6px;flex-wrap:wrap;align-items:center}
.page-card h3,.trail-card h3{font-size:17px;line-height:1.25;margin:0}
.page-card p,.trail-card p,.recall-path p{margin:0;color:#313a34}
.path{color:var(--muted)}
.trail-list,.reasoning-list{display:grid;grid-template-columns:repeat(auto-fit,minmax(330px,1fr));gap:12px;margin-bottom:24px}
.path-flow{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:6px}
.path-flow span{border:1px solid var(--line);border-radius:6px;background:#f6f8f5;color:var(--muted);font-size:12px;font-weight:700;text-align:center;padding:6px}
.trail-query{font-weight:700;color:var(--ink)}
.recall-path .path-step{border-top:1px solid var(--line);padding-top:8px}
.recall-path .path-step:first-of-type{border-top:0;padding-top:0}
.path-step strong{display:block;margin-bottom:4px}.path-step ul{margin:0;padding-left:18px}.path-step span{color:var(--muted)}
.tag{display:inline-flex;align-items:center;border-radius:999px;border:1px solid var(--line);background:#f7f8f6;color:#374039;font-size:12px;line-height:1;padding:5px 7px}
.tag.type{border-color:#c6ddd9;background:#eef8f6;color:var(--accent);font-weight:700}
.tag.status{border-color:#dce7c7;background:#f6faee;color:#4d6b1f}
.tag.topic{background:#f4f6fa;border-color:#d5deec;color:#2c4f7f}
.tag.hot{background:#fff5df;border-color:#ecd398;color:var(--amber)}
.tag.warn{background:#fff0ed;border-color:#e4bab3;color:var(--red)}
.mini-metrics span{border:1px solid var(--line);border-radius:6px;padding:5px 7px;color:var(--muted)}
.mini-metrics strong{color:var(--ink)}
.hot-list{display:flex;flex-direction:column;gap:8px}
.hot-item{display:flex;justify-content:space-between;gap:12px;border:1px solid var(--line);border-radius:7px;padding:9px 10px;background:#fbfcfb;color:var(--ink)}
.hot-item strong{color:var(--amber)}
.artifact{margin:8px 0}
.artifact summary{display:flex;gap:10px;align-items:center;cursor:pointer;padding:12px}
.artifact summary span{font-weight:700}
.artifact summary em{margin-left:auto;color:var(--muted);font-style:normal}
pre{white-space:pre-wrap;overflow:auto;max-height:520px;background:#101713;color:#edf2ec;border-radius:6px;padding:12px}
.detail-grid{display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:16px}
.document h1{font-size:28px}.document h2{font-size:22px;margin-top:24px}.document h3{font-size:17px;margin-top:18px}
.document p,.document li{font-size:15px}.document ul{padding-left:21px}
.summary{border-left:3px solid var(--accent);padding:8px 0 8px 12px;color:#33413a;background:#f7faf8}
.trail-path{border:1px solid var(--line);border-radius:8px;background:#fbfcfb;padding:14px;margin:0 0 18px}
.path-steps{display:flex;flex-direction:column;gap:10px;margin:0;padding-left:20px}.path-steps li{padding-left:4px}.path-steps strong{display:block;margin-bottom:4px}
.wikilink{font-weight:700;border-bottom:1px solid #b8c9df}
.broken-wikilink{color:var(--muted);border:1px dashed var(--line);border-radius:4px;padding:1px 4px;background:#fafafa}
.side{display:flex;flex-direction:column;gap:12px}
.edge-list{list-style:none;margin:0 0 16px;padding:0;display:flex;flex-direction:column;gap:8px}
.edge-list li{border:1px solid var(--line);border-radius:7px;padding:9px;background:#fbfcfb}
.edge-list a{display:block;font-weight:700}.edge-list span{display:block;color:var(--muted);font-size:12px;margin-top:2px}
.muted{color:var(--muted)}
@media (max-width:860px){.hero-main,.overview-grid,.detail-grid{display:block}.hero-nav{margin-top:16px}.side{margin-top:16px}.shell{padding:18px 16px 34px}.hero{padding:20px 16px}h1{font-size:28px}}
`
}

func displayPath(roots *memexRoots, path string) string {
	relPath, err := filepath.Rel(roots.ProjectRoot, path)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return filepath.ToSlash(path)
	}
	if relPath == "." {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(relPath)
}

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && !stat.IsDir()
}

func directoryExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.IsDir()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func escapeHTML(value string) string {
	return html.EscapeString(value)
}

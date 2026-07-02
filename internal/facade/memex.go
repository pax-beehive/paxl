package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxMemexArtifactBytes = 32 * 1024
	maxMemexPages         = 100
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
	HTML   string
	Assets []*MemexRenderAsset
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
	RelPath string
	Title   string
	Preview string
}

type memexArtifact struct {
	Name    string
	RelPath string
	Pretty  string
	Counts  []string
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
	pages, err := collectMemexPages(ctx, roots)
	if err != nil {
		return nil, fmt.Errorf("collect memex pages: %w", err)
	}
	artifacts := collectMemexArtifacts(roots)
	assets := collectMemexAssets(roots)
	return &RenderMemexResponse{
		HTML:   renderMemexHTML(roots, pages, artifacts, assets),
		Assets: assets,
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

func collectMemexPages(ctx context.Context, roots *memexRoots) ([]*memexPage, error) {
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
		pages = append(pages, &memexPage{
			RelPath: relPath,
			Title:   extractQMDTitle(relPath, string(raw)),
			Preview: extractQMDPreview(string(raw)),
		})
	}
	sort.SliceStable(pages, func(i int, j int) bool {
		return pages[i].RelPath < pages[j].RelPath
	})
	return pages, nil
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
) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	builder.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	builder.WriteString(`<title>Paxl Memex</title>`)
	builder.WriteString(`<style>
body{margin:0;background:#f6f7f8;color:#1b1f24;font:14px/1.5 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
header{background:#ffffff;border-bottom:1px solid #d7dce1;padding:22px 28px}
main{max-width:1180px;margin:0 auto;padding:24px 28px 44px}
h1{font-size:28px;line-height:1.15;margin:0 0 8px}
h2{font-size:18px;margin:28px 0 12px}
h3{font-size:15px;margin:0 0 4px}
p{margin:0 0 10px}
code{background:#edf0f2;border:1px solid #d7dce1;border-radius:4px;padding:1px 4px}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px;margin-top:16px}
.stat,.page,details{background:#ffffff;border:1px solid #d7dce1;border-radius:8px;padding:12px}
.stat strong{display:block;font-size:22px;line-height:1.2}
.muted{color:#59636e}
.pages{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:10px}
.graph{width:100%;max-height:560px;object-fit:contain;background:#ffffff;border:1px solid #d7dce1;border-radius:8px}
summary{cursor:pointer;font-weight:600}
pre{white-space:pre-wrap;overflow:auto;max-height:420px;background:#101418;color:#edf0f2;border-radius:6px;padding:12px}
</style></head><body>`)
	builder.WriteString(
		`<header><h1>Paxl Memex</h1><p class="muted">Local qmd wiki and recall trail artifacts from <code>`,
	)
	builder.WriteString(escapeHTML(displayPath(roots, roots.ProjectRoot)))
	builder.WriteString(`</code>.</p>`)
	builder.WriteString(`<div class="grid">`)
	writeMemexStat(&builder, "Pages", len(pages))
	writeMemexStat(&builder, "Artifacts", len(artifacts))
	writeMemexStat(&builder, "Assets", len(assets))
	builder.WriteString(`</div></header><main>`)
	if len(assets) > 0 {
		builder.WriteString(`<section><h2>Graph</h2><img class="graph" src="`)
		builder.WriteString(escapeHTML(assets[0].URLPath))
		builder.WriteString(`" alt="Memex graph"></section>`)
	}
	builder.WriteString(`<section><h2>Pages</h2><div class="pages">`)
	if len(pages) == 0 {
		builder.WriteString(`<p class="muted">No qmd pages found.</p>`)
	}
	for _, page := range pages {
		builder.WriteString(`<article class="page"><h3>`)
		builder.WriteString(escapeHTML(page.Title))
		builder.WriteString(`</h3><p><code>`)
		builder.WriteString(escapeHTML(page.RelPath))
		builder.WriteString(`</code></p>`)
		if page.Preview != "" {
			builder.WriteString(`<p>`)
			builder.WriteString(escapeHTML(page.Preview))
			builder.WriteString(`</p>`)
		}
		builder.WriteString(`</article>`)
	}
	builder.WriteString(`</div></section>`)
	builder.WriteString(`<section><h2>Artifacts</h2>`)
	if len(artifacts) == 0 {
		builder.WriteString(`<p class="muted">No memex JSON artifacts found.</p>`)
	}
	for _, artifact := range artifacts {
		builder.WriteString(`<details open><summary>`)
		builder.WriteString(escapeHTML(artifact.Name))
		builder.WriteString(` <span class="muted">`)
		builder.WriteString(escapeHTML(artifact.RelPath))
		if len(artifact.Counts) > 0 {
			builder.WriteString(` | `)
			builder.WriteString(escapeHTML(strings.Join(artifact.Counts, ", ")))
		}
		builder.WriteString(`</span></summary><pre>`)
		builder.WriteString(escapeHTML(artifact.Pretty))
		builder.WriteString(`</pre></details>`)
	}
	builder.WriteString(`</section></main></body></html>`)
	return builder.String()
}

func writeMemexStat(builder *strings.Builder, label string, count int) {
	builder.WriteString(`<div class="stat"><span class="muted">`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</span><strong>`)
	_, _ = fmt.Fprintf(builder, "%d", count)
	builder.WriteString(`</strong></div>`)
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

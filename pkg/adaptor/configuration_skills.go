package adaptor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"gopkg.in/yaml.v3"
)

const configurationSkillDirectoryLimit = 4096

type configurationSkillRoot struct {
	path     string
	scope    model.ConfigurationScope
	owner    string
	flat     bool
	maxDepth int
	origin   model.ConfigurationOrigin
	enabled  *bool
}

type configurationSkillWalk struct {
	reader  *configurationReader
	root    configurationSkillRoot
	visited map[string]bool
	count   int
}

func (r *configurationReader) readSkills() {
	seen := map[string]bool{}
	roots := append(r.skillRoots(), r.extraSkillRoots()...)
	for _, root := range roots {
		if r.ctx.Err() != nil {
			return
		}
		path := filepath.Clean(root.path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		r.recordSkillRoot(&root)
		if r.req.Agent == model.AgentNameDSH || r.req.Agent == model.AgentNameDSCode {
			root.maxDepth = 1
		}
		walk := configurationSkillWalk{reader: r, root: root, visited: map[string]bool{}}
		walk.scan(path, 0)
	}
	r.applySkillSettings()
	r.applySkillOrigins()
	r.result.Coverage[model.AgentConfigurationKindSkills] = model.ConfigurationCoverage{
		Status: "partial",
		Limitations: []string{
			"Passive file discovery does not verify native runtime visibility, trust, dependency gates or invocation.",
			"Bundled, cloud-synced and dynamically resolved remote package catalogs are not enumerated without an evidenced local installation.",
			"Same-named skills are retained as separate candidates; native version-specific precedence is not inferred.",
		},
	}
}

func (w *configurationSkillWalk) scan(path string, depth int) {
	maxDepth := w.root.maxDepth
	if maxDepth == 0 {
		maxDepth = 6
	}
	if w.reader.ctx.Err() != nil || depth > maxDepth {
		return
	}
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		w.reader.addDiagnostic(
			"skill_directory_unreadable",
			"A skill directory could not be resolved.",
			&model.ConfigurationSource{Path: path, Scope: w.root.scope},
		)
		return
	}
	if w.visited[resolved] {
		return
	}
	w.visited[resolved] = true
	w.count++
	if w.count > configurationSkillDirectoryLimit {
		w.reader.addDiagnostic(
			"skill_scan_limit",
			"Skill discovery reached its directory limit.",
			&model.ConfigurationSource{Path: w.root.path, Scope: w.root.scope},
		)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if w.root.flat && strings.EqualFold(filepath.Ext(path), ".md") {
			w.read(path, true)
		}
		return
	}
	if info, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil && !info.IsDir() {
		w.read(filepath.Join(path, "SKILL.md"), false)
		return
	}
	w.scanDirectory(path, depth)
}

func (w *configurationSkillWalk) scanDirectory(path string, depth int) {
	entries, err := os.ReadDir(path)
	if err != nil {
		w.reader.addDiagnostic(
			"skill_directory_unreadable",
			"A skill directory could not be read.",
			&model.ConfigurationSource{Path: path, Scope: w.root.scope},
		)
		return
	}
	for _, entry := range entries {
		if configurationSkipSkillEntry(entry.Name()) {
			continue
		}
		next := filepath.Join(path, entry.Name())
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			w.scan(next, depth+1)
		} else if depth == 0 && w.root.flat && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			w.read(next, true)
		}
	}
}

func configurationSkipSkillEntry(name string) bool {
	switch name {
	case ".git", "node_modules", ".hub", ".clawhub", ".clawdhub":
		return true
	default:
		return false
	}
}

func (w *configurationSkillWalk) read(path string, flat bool) {
	data, source, ok := w.reader.readFile(path, w.root.scope, "markdown")
	if !ok {
		return
	}
	source.Owner = w.root.owner
	frontmatter, body, err := configurationSkillFrontmatter(data)
	if err != nil {
		w.reader.addDiagnostic(
			"skill_frontmatter_invalid",
			"A skill has invalid YAML frontmatter.",
			&source,
		)
		return
	}
	name := configurationString(frontmatter["name"])
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
		if flat {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}
	description := configurationString(frontmatter["description"])
	if !flat && (configurationString(frontmatter["name"]) == "" || description == "") {
		w.reader.addDiagnostic(
			"skill_metadata_incomplete",
			"A skill is missing required name or description metadata; it is retained only as a file candidate.",
			&source,
		)
	}
	if description == "" && flat {
		description = strings.SplitN(strings.TrimSpace(body), "\n", 2)[0]
	}
	skill := model.SkillConfiguration{
		ID:             configurationID("skill", name, &source),
		Name:           name,
		Description:    description,
		Directory:      filepath.Dir(path),
		Source:         source,
		Frontmatter:    frontmatter,
		Origin:         w.root.origin,
		ModelInvocable: configurationSkillBool(true),
		UserInvocable:  configurationSkillBool(true),
		Enabled:        w.root.enabled,
	}
	if skill.Origin.Kind == "" {
		skill.Origin.Kind = "unknown"
	}
	if w.reader.req.Agent != model.AgentNameOpenCode &&
		w.reader.req.Agent != model.AgentNameCodex &&
		w.reader.req.Agent != model.AgentNameHermes &&
		w.reader.req.Agent != model.AgentNameKiro {
		configurationApplySkillInvocation(&skill)
	}
	if w.reader.req.Agent == model.AgentNameOpenCode {
		skill.UserInvocable = nil
	}
	if w.reader.req.Agent == model.AgentNameCodex {
		policyPath := filepath.Join(skill.Directory, "agents", "openai.yaml")
		if document, _, exists := w.reader.readDocument(policyPath, skill.Source.Scope); exists {
			if allow := configurationBool(
				configurationMap(document["policy"])["allow_implicit_invocation"],
			); allow != nil {
				skill.ModelInvocable = allow
			}
		}
	}
	if w.reader.req.IncludeContent {
		skill.Instructions = strings.TrimSpace(body)
	}
	w.reader.result.Skills = append(w.reader.result.Skills, skill)
}

func configurationSkillFrontmatter(data []byte) (map[string]any, string, error) {
	text := strings.ReplaceAll(
		string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})),
		"\r\n",
		"\n",
	)
	frontmatter := map[string]any{}
	if !strings.HasPrefix(text, "---\n") {
		return frontmatter, text, nil
	}
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &frontmatter); err != nil {
			return nil, "", err
		}
		if frontmatter == nil {
			frontmatter = map[string]any{}
		}
		if _, err := json.Marshal(frontmatter); err != nil {
			return nil, "", fmt.Errorf(
				"skill frontmatter contains values that cannot be represented in JSON",
			)
		}
		return frontmatter, strings.Join(lines[i+1:], "\n"), nil
	}
	return nil, "", fmt.Errorf("unclosed skill frontmatter")
}

func configurationApplySkillInvocation(skill *model.SkillConfiguration) {
	for _, key := range []string{"disable-model-invocation", "disable_model_invocation", "disableModelInvocation"} {
		if value := configurationBool(skill.Frontmatter[key]); value != nil {
			skill.ModelInvocable = configurationSkillBool(!*value)
		}
	}
	for _, key := range []string{"user-invocable", "user_invocable", "userInvocable"} {
		if value := configurationBool(skill.Frontmatter[key]); value != nil {
			skill.UserInvocable = value
		}
	}
	if configurationString(skill.Frontmatter["type"]) == "flow" {
		skill.ModelInvocable = configurationSkillBool(false)
	}
}

func configurationSkillBool(value bool) *bool { return &value }

func (r *configurationReader) recordSkillRoot(root *configurationSkillRoot) {
	source := model.ConfigurationSource{
		Path:   root.path,
		Scope:  root.scope,
		Owner:  root.owner,
		Format: "directory",
		Status: model.ConfigurationSourceStatusRead,
	}
	info, err := os.Stat(root.path)
	switch {
	case os.IsNotExist(err):
		source.Status = model.ConfigurationSourceStatusMissing
	case err != nil:
		source.Status = model.ConfigurationSourceStatusError
	case !info.IsDir():
		source.Format = "markdown"
	}
	if resolved, err := filepath.EvalSymlinks(root.path); err == nil {
		source.ResolvedPath = resolved
	}
	r.result.Sources = append(r.result.Sources, source)
}

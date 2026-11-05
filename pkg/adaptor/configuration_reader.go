package adaptor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func readConfiguration(
	ctx context.Context,
	req *ReadConfigurationRequest,
	opts ...func(*Option),
) (*model.AgentConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read agent configuration: %w", err)
	}
	if err := validateConfigurationRequest(req); err != nil {
		return nil, err
	}
	copyRequest := *req
	if copyRequest.Kind == "" {
		copyRequest.Kind = model.AgentConfigurationKindAll
	}
	r := &configurationReader{
		ctx: ctx,
		req: &copyRequest,
		env: configurationEnvironment(req.Environment),
	}
	if err := r.initializePaths(); err != nil {
		return nil, err
	}
	r.result = newAgentConfiguration(&copyRequest, r.cwd)
	option := applyOptions(opts)
	if option.VerboseWriter != nil {
		_, _ = fmt.Fprintf(
			option.VerboseWriter,
			"Reading native %s configuration for %s.\n",
			copyRequest.Kind,
			copyRequest.Agent,
		)
	}
	if copyRequest.Kind != model.AgentConfigurationKindMCP {
		r.readSkills()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read agent configuration: %w", err)
	}
	if copyRequest.Kind != model.AgentConfigurationKindSkills {
		r.readMCP()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read agent configuration: %w", err)
	}
	r.finishConfiguration()
	return r.result, nil
}

func validateConfigurationRequest(req *ReadConfigurationRequest) error {
	if req == nil {
		return fmt.Errorf("agent configuration request is required")
	}
	switch req.Agent {
	case model.AgentNameCodex,
		model.AgentNameClaude,
		model.AgentNamePi,
		model.AgentNameKiro,
		model.AgentNameOpenCode,
		model.AgentNameKimi,
		model.AgentNameHermes,
		model.AgentNameOpenClaw,
		model.AgentNameDSH,
		model.AgentNameDSCode:
	case model.AgentNameUnknown,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameAntigravity:
		return fmt.Errorf("unsupported configuration agent %q", req.Agent)
	default:
		return fmt.Errorf("unsupported configuration agent %q", req.Agent)
	}
	if req.Kind != "" {
		if _, err := model.ParseAgentConfigurationKind(string(req.Kind)); err != nil {
			return fmt.Errorf("parse configuration kind: %w", err)
		}
	}
	return nil
}

func configurationEnvironment(explicit map[string]string) map[string]string {
	result := map[string]string{}
	if explicit != nil {
		for name, value := range explicit {
			result[name] = value
		}
		return result
	}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			result[name] = value
		}
	}
	return result
}

func (r *configurationReader) initializePaths() error {
	cwd := r.req.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve configuration working directory: %w", err)
		}
	}
	var err error
	r.cwd, err = filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("resolve configuration working directory: %w", err)
	}
	info, err := os.Stat(r.cwd)
	if err != nil {
		return fmt.Errorf("inspect configuration working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("configuration working directory must be a directory")
	}
	r.home = r.req.Home
	if r.home == "" {
		r.home = r.env["HOME"]
	}
	if r.home == "" {
		r.home = r.env["USERPROFILE"]
	}
	if r.home == "" && r.req.Environment == nil {
		r.home, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve configuration home directory: %w", err)
		}
	}
	if r.home == "" {
		return fmt.Errorf("configuration home directory is required for an isolated environment")
	}
	if !filepath.IsAbs(r.home) {
		r.home = filepath.Join(r.cwd, r.home)
	}
	r.home = filepath.Clean(r.home)
	dirs := r.projectDirs()
	r.root = dirs[len(dirs)-1]
	return nil
}

func newAgentConfiguration(req *ReadConfigurationRequest, cwd string) *model.AgentConfiguration {
	result := &model.AgentConfiguration{
		SchemaVersion: 1,
		Agent:         req.Agent,
		CWD:           cwd,
		Kind:          req.Kind,
		Skills:        []model.SkillConfiguration{},
		MCPServers:    []model.MCPServerConfiguration{},
		Sources:       []model.ConfigurationSource{},
		Conflicts:     []model.ConfigurationConflict{},
		Diagnostics:   []model.ConfigurationDiagnostic{},
		Coverage:      map[model.AgentConfigurationKind]model.ConfigurationCoverage{},
	}
	for _, kind := range []model.AgentConfigurationKind{model.AgentConfigurationKindSkills, model.AgentConfigurationKindMCP} {
		coverage := model.ConfigurationCoverage{Status: "partial", Limitations: []string{
			"Passive file inventory; runtime loading, agent version, authentication, and connectivity are not verified.",
			"Managed policy, built-in resources, dynamically loaded plugins, packages, and runtime overrides may add or replace configuration.",
		}}
		if req.Kind != model.AgentConfigurationKindAll && req.Kind != kind {
			coverage.Status, coverage.Limitations = "not_requested", []string{}
		}
		result.Coverage[kind] = coverage
	}
	return result
}

func (r *configurationReader) envPath(name, fallback string) string {
	value := r.env[name]
	if value == "" {
		value = fallback
	}
	return r.expandPath(value, r.cwd)
}

func (r *configurationReader) expandPath(value, base string) string {
	if value == "" {
		return ""
	}
	if value == "~" {
		value = r.home
	} else if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "~\\") {
		value = filepath.Join(r.home, value[2:])
	}
	value = os.Expand(value, func(name string) string {
		if replacement, ok := r.env[name]; ok {
			return replacement
		}
		return "${" + name + "}"
	})
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value)
}

// Stop at the nearest repository boundary, including a worktree's .git file.
// Without a repository marker, probing ancestors could read unrelated projects.
func (r *configurationReader) projectDirs() []string {
	dirs := []string{}
	for dir := r.cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dirs
		}
		if filepath.Dir(dir) == dir {
			return []string{r.cwd}
		}
	}
}

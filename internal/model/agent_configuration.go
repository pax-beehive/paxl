package model

import "fmt"

type AgentConfigurationKind string

const (
	AgentConfigurationKindUnknown AgentConfigurationKind = "unknown"
	AgentConfigurationKindAll     AgentConfigurationKind = "all"
	AgentConfigurationKindSkills  AgentConfigurationKind = "skills"
	AgentConfigurationKindMCP     AgentConfigurationKind = "mcp"
)

func ParseAgentConfigurationKind(raw string) (AgentConfigurationKind, error) {
	switch AgentConfigurationKind(raw) {
	case AgentConfigurationKindAll, AgentConfigurationKindSkills, AgentConfigurationKindMCP:
		return AgentConfigurationKind(raw), nil
	case AgentConfigurationKindUnknown:
	}
	return AgentConfigurationKindUnknown, fmt.Errorf(
		"unsupported configuration kind %q; supported: all, skills, mcp",
		raw,
	)
}

type ConfigurationScope string

const (
	ConfigurationScopeUnknown ConfigurationScope = "unknown"
	ConfigurationScopeUser    ConfigurationScope = "user"
	ConfigurationScopeProject ConfigurationScope = "project"
	ConfigurationScopeSystem  ConfigurationScope = "system"
	ConfigurationScopeProfile ConfigurationScope = "profile"
	ConfigurationScopeAgent   ConfigurationScope = "agent"
	ConfigurationScopePlugin  ConfigurationScope = "plugin"
	ConfigurationScopePackage ConfigurationScope = "package"
)

type ConfigurationSourceStatus string

const (
	ConfigurationSourceStatusUnknown     ConfigurationSourceStatus = "unknown"
	ConfigurationSourceStatusRead        ConfigurationSourceStatus = "read"
	ConfigurationSourceStatusMissing     ConfigurationSourceStatus = "missing"
	ConfigurationSourceStatusError       ConfigurationSourceStatus = "error"
	ConfigurationSourceStatusUnsupported ConfigurationSourceStatus = "unsupported"
)

// ConfigurationSource records where a declaration was observed, independently
// of the repository or package from which it was originally installed.
type ConfigurationSource struct {
	Path         string                    `json:"path"`
	ResolvedPath string                    `json:"resolved_path,omitempty"`
	Scope        ConfigurationScope        `json:"scope"`
	Format       string                    `json:"format"`
	Pointer      string                    `json:"pointer,omitempty"`
	Owner        string                    `json:"owner,omitempty"`
	Status       ConfigurationSourceStatus `json:"status"`
	SHA256       string                    `json:"sha256,omitempty"`
}

type ConfigurationOrigin struct {
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Package  string `json:"package,omitempty"`
	Version  string `json:"version,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type SkillConfiguration struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Directory      string              `json:"directory"`
	Source         ConfigurationSource `json:"source"`
	Origin         ConfigurationOrigin `json:"origin"`
	Enabled        *bool               `json:"enabled"`
	ModelInvocable *bool               `json:"model_invocable"`
	UserInvocable  *bool               `json:"user_invocable"`
	Frontmatter    map[string]any      `json:"frontmatter"`
	Instructions   string              `json:"instructions,omitempty"`
}

type MCPServerConfiguration struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Transport   string              `json:"transport"`
	Command     string              `json:"command,omitempty"`
	Args        []string            `json:"args"`
	CWD         string              `json:"cwd,omitempty"`
	URL         string              `json:"url,omitempty"`
	Environment map[string]string   `json:"environment"`
	Headers     map[string]string   `json:"headers"`
	Enabled     *bool               `json:"enabled"`
	Source      ConfigurationSource `json:"source"`
	Origin      ConfigurationOrigin `json:"origin"`
	Native      map[string]any      `json:"native"`
	Redacted    bool                `json:"redacted"`
}

type ConfigurationCoverage struct {
	Status      string   `json:"status"`
	Limitations []string `json:"limitations"`
}

type ConfigurationDiagnostic struct {
	Severity string               `json:"severity"`
	Code     string               `json:"code"`
	Message  string               `json:"message"`
	Source   *ConfigurationSource `json:"source,omitempty"`
}

type ConfigurationConflict struct {
	Kind       AgentConfigurationKind `json:"kind"`
	Name       string                 `json:"name"`
	Candidates []ConfigurationSource  `json:"candidates"`
	Resolution string                 `json:"resolution"`
}

// AgentConfiguration is a versioned, passive file inventory. Presence and
// declared enablement never imply runtime visibility, authentication or connection.
type AgentConfiguration struct {
	SchemaVersion   int                                              `json:"schema_version"`
	Agent           AgentName                                        `json:"agent"`
	CWD             string                                           `json:"cwd"`
	Kind            AgentConfigurationKind                           `json:"kind"`
	RuntimeVerified bool                                             `json:"runtime_verified"`
	Skills          []SkillConfiguration                             `json:"skills"`
	MCPServers      []MCPServerConfiguration                         `json:"mcp_servers"`
	Sources         []ConfigurationSource                            `json:"sources"`
	Coverage        map[AgentConfigurationKind]ConfigurationCoverage `json:"coverage"`
	Conflicts       []ConfigurationConflict                          `json:"conflicts"`
	Diagnostics     []ConfigurationDiagnostic                        `json:"diagnostics"`
}

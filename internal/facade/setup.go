package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

const paxlHookAdapterSchemaVersion = "paxl.agent_hook_adapter.v1"

type SetupStatus string

const (
	SetupStatusUnknown   SetupStatus = "unknown"
	SetupStatusInstalled SetupStatus = "installed"
	SetupStatusPending   SetupStatus = "pending"
	SetupStatusSkipped   SetupStatus = "skipped"
)

type SetupFacade struct{}

type SetupRequest struct {
	Agents      []model.AgentName
	PaxlCommand string
	DryRun      bool
}

type SetupResponse struct {
	Adapters []*SetupAdapterResult
}

type SetupAdapterResult struct {
	Agent   model.AgentName
	Status  SetupStatus
	Path    string
	Message string
}

type hookAdapterDescriptor struct {
	SchemaVersion string          `json:"schema_version"`
	Agent         model.AgentName `json:"agent"`
	Event         string          `json:"event"`
	Command       string          `json:"command"`
	Status        string          `json:"status"`
	CreatedAt     string          `json:"created_at"`
}

func NewSetupFacade() *SetupFacade {
	return &SetupFacade{}
}

func (f *SetupFacade) Install(
	ctx context.Context,
	req *SetupRequest,
	opts ...func(*Option),
) (*SetupResponse, error) {
	_ = ctx
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("setup hooks: request is required")
	}
	agents := setupAgents(req.Agents)
	command := strings.TrimSpace(req.PaxlCommand)
	if command == "" {
		command = "paxl"
	}
	var results []*SetupAdapterResult
	if err := installHookShim(command, req.DryRun); err != nil {
		return nil, err
	}
	for _, agent := range agents {
		result, err := f.installAgentHook(agent, command, req.DryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return &SetupResponse{Adapters: results}, nil
}

func (f *SetupFacade) installAgentHook(
	agent model.AgentName,
	command string,
	dryRun bool,
) (*SetupAdapterResult, error) {
	switch agent {
	case model.AgentNameCodex:
		return installDescriptorHook(agent, codexHookDescriptorPath(), command, dryRun)
	case model.AgentNameClaude:
		return installClaudeHook(command, dryRun)
	case model.AgentNameHermes:
		return installDescriptorHook(agent, hermesHookDescriptorPath(), command, dryRun)
	case model.AgentNameUnknown,
		model.AgentNamePi,
		model.AgentNameKiro,
		model.AgentNameGemini,
		model.AgentNameOpenClaw:
		return &SetupAdapterResult{
			Agent:   agent,
			Status:  SetupStatusSkipped,
			Message: "Agent does not support hook setup.",
		}, nil
	}
	return &SetupAdapterResult{
		Agent:   agent,
		Status:  SetupStatusSkipped,
		Message: "Agent does not support hook setup.",
	}, nil
}

func setupAgents(agents []model.AgentName) []model.AgentName {
	if len(agents) == 0 {
		return []model.AgentName{
			model.AgentNameCodex,
			model.AgentNameClaude,
			model.AgentNameHermes,
		}
	}
	return agents
}

func installHookShim(command string, dryRun bool) error {
	path, err := hookShimPath()
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	script := "#!/bin/sh\nexec " + shellQuote(command) + " __agent-hook \"$@\"\n"
	if err := writeFile(path, []byte(script), 0o755); err != nil {
		return fmt.Errorf("install hook shim: %w", err)
	}
	return nil
}

func installDescriptorHook(
	agent model.AgentName,
	path string,
	command string,
	dryRun bool,
) (*SetupAdapterResult, error) {
	hookCommand := setupHookCommand(command, agent)
	result := &SetupAdapterResult{
		Agent:   agent,
		Status:  SetupStatusInstalled,
		Path:    path,
		Message: "Installed paxl hook descriptor.",
	}
	if dryRun {
		result.Status = SetupStatusPending
		result.Message = "Would install paxl hook descriptor."
		return result, nil
	}
	descriptor := &hookAdapterDescriptor{
		SchemaVersion: paxlHookAdapterSchemaVersion,
		Agent:         agent,
		Event:         "user_prompt",
		Command:       hookCommand,
		Status:        "host_activation_pending",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode hook descriptor: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFile(path, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write hook descriptor: %w", err)
	}
	return result, nil
}

func installClaudeHook(command string, dryRun bool) (*SetupAdapterResult, error) {
	path := claudeSettingsPath()
	result := &SetupAdapterResult{
		Agent:   model.AgentNameClaude,
		Status:  SetupStatusInstalled,
		Path:    path,
		Message: "Installed Claude Code UserPromptSubmit hook.",
	}
	if dryRun {
		result.Status = SetupStatusPending
		result.Message = "Would install Claude Code UserPromptSubmit hook."
		return result, nil
	}
	settings, err := readJSONMap(path)
	if err != nil {
		return nil, fmt.Errorf("read Claude settings: %w", err)
	}
	hooks := ensureMap(settings, "hooks")
	groups := ensureSlice(hooks, "UserPromptSubmit")
	group := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": setupHookCommand(command, model.AgentNameClaude),
				"async":   false,
			},
		},
	}
	hooks["UserPromptSubmit"] = upsertPaxlHook(groups, model.AgentNameClaude, group)
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Claude settings: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFile(path, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write Claude settings: %w", err)
	}
	return result, nil
}

func setupHookCommand(command string, agent model.AgentName) string {
	return strings.TrimSpace(command) +
		" __agent-hook --agent " +
		string(agent) +
		" --event user-prompt"
}

func upsertPaxlHook(groups []any, agent model.AgentName, next map[string]any) []any {
	needle := "__agent-hook --agent " + string(agent) + " --event user-prompt"
	for index, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		handlers, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, rawHandler := range handlers {
			handler, ok := rawHandler.(map[string]any)
			if !ok {
				continue
			}
			command, _ := handler["command"].(string)
			if strings.Contains(command, needle) {
				groups[index] = next
				return groups
			}
		}
	}
	return append(groups, next)
}

func readJSONMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path) // #nosec G304
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read json file: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse json file: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func ensureMap(parent map[string]any, key string) map[string]any {
	existing, ok := parent[key].(map[string]any)
	if ok {
		return existing
	}
	next := map[string]any{}
	parent[key] = next
	return next
}

func ensureSlice(parent map[string]any, key string) []any {
	existing, ok := parent[key].([]any)
	if ok {
		return existing
	}
	next := []any{}
	parent[key] = next
	return next
}

func writeFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func hookShimPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".pax", "paxl", "hooks", "agent-hook"), nil
}

func codexHookDescriptorPath() string {
	root := firstNonEmpty(os.Getenv("CODEX_HOME"), homePath(".codex"))
	return filepath.Join(root, "paxl", "hooks", "user-prompt.json")
}

func claudeSettingsPath() string {
	root := firstNonEmpty(os.Getenv("CLAUDE_HOME"), homePath(".claude"))
	return filepath.Join(root, "settings.json")
}

func hermesHookDescriptorPath() string {
	root := firstNonEmpty(os.Getenv("HERMES_HOME"), homePath(".hermes"))
	return filepath.Join(root, "paxl", "hooks", "user-prompt.json")
}

func homePath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(parts...)
	}
	all := append([]string{home}, parts...)
	return filepath.Join(all...)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
		return installCodexHook(command, dryRun)
	case model.AgentNameClaude:
		return installClaudeHook(command, dryRun)
	case model.AgentNamePi:
		return installPiHook(command, dryRun)
	case model.AgentNameKiro:
		return installKiroHook(command, dryRun)
	case model.AgentNameHermes:
		dbPath, err := defaultStorePath()
		if err != nil {
			return nil, err
		}
		return installDescriptorHook(agent, hermesHookDescriptorPath(), command, dbPath, dryRun)
	case model.AgentNameOpenClaw:
		dbPath, err := defaultStorePath()
		if err != nil {
			return nil, err
		}
		return installDescriptorHook(
			agent,
			genericHookDescriptorPath(agent),
			command,
			dbPath,
			dryRun,
		)
	case model.AgentNameUnknown,
		model.AgentNameGemini,
		model.AgentNamePaxl:
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
			model.AgentNamePi,
			model.AgentNameKiro,
			model.AgentNameHermes,
			model.AgentNameOpenClaw,
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
	dbPath string,
	dryRun bool,
) (*SetupAdapterResult, error) {
	hookCommand := setupHookCommand(command, agent, dbPath)
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

func installPiHook(command string, dryRun bool) (*SetupAdapterResult, error) {
	dbPath, err := defaultStorePath()
	if err != nil {
		return nil, err
	}
	if _, err := installDescriptorHook(
		model.AgentNamePi,
		genericHookDescriptorPath(model.AgentNamePi),
		command,
		dbPath,
		dryRun,
	); err != nil {
		return nil, err
	}
	path := piHookExtensionPath()
	result := &SetupAdapterResult{
		Agent:   model.AgentNamePi,
		Status:  SetupStatusInstalled,
		Path:    path,
		Message: "Installed Pi before_agent_start hook extension.",
	}
	if dryRun {
		result.Status = SetupStatusPending
		result.Message = "Would install Pi before_agent_start hook extension."
		return result, nil
	}
	if err := writeFile(path, []byte(renderPiHookExtension(command, dbPath)), 0o600); err != nil {
		return nil, fmt.Errorf("write Pi hook extension: %w", err)
	}
	return result, nil
}

func installKiroHook(command string, dryRun bool) (*SetupAdapterResult, error) {
	dbPath, err := defaultStorePath()
	if err != nil {
		return nil, err
	}
	if _, err := installDescriptorHook(
		model.AgentNameKiro,
		genericHookDescriptorPath(model.AgentNameKiro),
		command,
		dbPath,
		dryRun,
	); err != nil {
		return nil, err
	}
	path := kiroAgentConfigPath()
	result := &SetupAdapterResult{
		Agent:   model.AgentNameKiro,
		Status:  SetupStatusInstalled,
		Path:    path,
		Message: "Installed Kiro CLI userPromptSubmit hook agent and set it as default.",
	}
	if dryRun {
		result.Status = SetupStatusPending
		result.Message = "Would install Kiro CLI userPromptSubmit hook agent and set it as default."
		return result, nil
	}
	config, err := readJSONMap(path)
	if err != nil {
		return nil, fmt.Errorf("read Kiro agent config: %w", err)
	}
	ensureKiroAgentDefaults(config)
	hooks := ensureMap(config, "hooks")
	commands := ensureSlice(hooks, "userPromptSubmit")
	next := map[string]any{
		"command": setupHookCommand(command, model.AgentNameKiro, dbPath),
	}
	hooks["userPromptSubmit"] = upsertKiroUserPromptHook(commands, next)
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Kiro agent config: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFile(path, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write Kiro agent config: %w", err)
	}
	if err := setKiroDefaultAgent("paxl"); err != nil {
		return nil, err
	}
	return result, nil
}

func installCodexHook(command string, dryRun bool) (*SetupAdapterResult, error) {
	dbPath, err := defaultStorePath()
	if err != nil {
		return nil, err
	}
	descriptor, err := installDescriptorHook(
		model.AgentNameCodex,
		codexHookDescriptorPath(),
		command,
		dbPath,
		dryRun,
	)
	if err != nil {
		return nil, err
	}
	result := &SetupAdapterResult{
		Agent:   model.AgentNameCodex,
		Status:  SetupStatusInstalled,
		Path:    codexConfigPath(),
		Message: "Installed Codex UserPromptSubmit hook.",
	}
	if dryRun {
		result.Status = SetupStatusPending
		result.Message = "Would install Codex UserPromptSubmit hook."
		return result, nil
	}
	if err := upsertCodexConfigHook(
		codexConfigPath(),
		setupHookCommand(command, model.AgentNameCodex, dbPath),
	); err != nil {
		return nil, err
	}
	if descriptor != nil {
		result.Path = descriptor.Path
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
				"command": setupHookCommand(command, model.AgentNameClaude, ""),
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

func setupHookCommand(command string, agent model.AgentName, dbPath string) string {
	out := shellCommandToken(strings.TrimSpace(command))
	if strings.TrimSpace(dbPath) != "" {
		out += " --db " + shellQuote(dbPath)
	}
	return out +
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

func ensureKiroAgentDefaults(config map[string]any) {
	config["name"] = "paxl"
	setDefault(config, "description", "Kiro CLI agent with paxl knowledge injection hook.")
	setDefault(config, "prompt", nil)
	setDefault(config, "mcpServers", map[string]any{})
	setDefault(config, "tools", []any{"*"})
	setDefault(config, "toolAliases", map[string]any{})
	setDefault(config, "allowedTools", []any{})
	setDefault(config, "resources", []any{})
	setDefault(config, "toolsSettings", map[string]any{})
	setDefault(config, "includeMcpJson", true)
	setDefault(config, "model", nil)
}

func setDefault(config map[string]any, key string, value any) {
	if _, ok := config[key]; ok {
		return
	}
	config[key] = value
}

func upsertKiroUserPromptHook(commands []any, next map[string]any) []any {
	needle := "__agent-hook --agent kiro --event user-prompt"
	for index, rawCommand := range commands {
		command, ok := rawCommand.(map[string]any)
		if !ok {
			continue
		}
		value, _ := command["command"].(string)
		if strings.Contains(value, needle) {
			commands[index] = next
			return commands
		}
	}
	return append(commands, next)
}

func setKiroDefaultAgent(agentName string) error {
	path := kiroSettingsPath()
	settings, err := readJSONMap(path)
	if err != nil {
		return fmt.Errorf("read Kiro settings: %w", err)
	}
	settings["chat.defaultAgent"] = agentName
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Kiro settings: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write Kiro settings: %w", err)
	}
	return nil
}

func renderPiHookExtension(command string, dbPath string) string {
	return fmt.Sprintf(`import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { spawnSync } from "node:child_process";

const paxlCommand = %s;
const paxlDatabase = %s;

function currentSessionId(ctx: any): string {
  const sessionFile = ctx.sessionManager?.getSessionFile?.();
  if (typeof sessionFile !== "string") return "";
  const fileName = sessionFile.split(/[\\/]/).pop() ?? "";
  const timestamped = fileName.match(/^\d{4}-\d{2}-\d{2}T[^_]+_(.+)\.jsonl$/i);
  if (timestamped?.[1]) return timestamped[1];
  return fileName.replace(/\.jsonl$/i, "");
}

export default function (pi: ExtensionAPI) {
  pi.on("before_agent_start", async (event, ctx) => {
    const args = [];
    if (paxlDatabase.trim() !== "") {
      args.push("--db", paxlDatabase);
    }
    args.push("__agent-hook", "--agent", "pi", "--event", "user-prompt");

    const payload = JSON.stringify({
      schema_version: "paxl.hook.user_prompt.v1",
      agent: "pi",
      session_id: currentSessionId(ctx),
      cwd: ctx.cwd,
      prompt: event.prompt,
    }) + "\n";

    const result = spawnSync(paxlCommand, args, {
      input: payload,
      encoding: "utf8",
      maxBuffer: 1024 * 1024,
    });

    if (result.error) {
      ctx.ui.notify(`+"`paxl hook failed: ${result.error.message}`"+`, "warning");
      return;
    }
    if (result.status !== 0) {
      const detail = (result.stderr || result.stdout || "Unknown paxl hook failure.").trim();
      ctx.ui.notify(`+"`paxl hook failed: ${detail}`"+`, "warning");
      return;
    }

    const message = result.stdout.trim();
    if (message === "") return;

    return {
      message: {
        customType: "paxl-knowledge-injection",
        content: message,
        display: true,
        details: {
          source: "paxl",
          event: "user_prompt",
        },
      },
    };
  });
}
`, strconv.Quote(strings.TrimSpace(command)), strconv.Quote(strings.TrimSpace(dbPath)))
}

func upsertCodexConfigHook(path string, command string) error {
	raw, err := os.ReadFile(path) // #nosec G304
	if os.IsNotExist(err) {
		raw = nil
	} else if err != nil {
		return fmt.Errorf("read Codex config: %w", err)
	}
	next := upsertTOMLMultilineEntry(
		string(raw),
		"hooks",
		[]string{"UserPromptSubmit", "userPromptSubmit"},
		codexHookTOMLEntry(command),
	)
	if err := writeFile(path, []byte(next), 0o600); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

func codexHookTOMLEntry(command string) string {
	return "UserPromptSubmit = [{ hooks = [{ type = \"command\", command = " +
		strconv.Quote(command) +
		", async = false }] }]"
}

func upsertTOMLMultilineEntry(
	content string,
	section string,
	keys []string,
	entry string,
) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	header := "[" + section + "]"
	inSection := false
	replaced := false
	out := make([]string, 0, len(lines)+2)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == header {
			inSection = true
			out = append(out, line)
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if !replaced {
				out = append(out, entry, "")
			}
			out = append(out, lines[index:]...)
			return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
		}
		if inSection && tomlLineStartsWithAnyKey(trimmed, keys) {
			if !replaced {
				out = append(out, entry)
				replaced = true
			}
			continue
		}
		out = append(out, line)
	}
	if inSection {
		if !replaced {
			out = append(out, entry)
		}
		return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	}
	trimmedContent := strings.TrimRight(content, "\n")
	if strings.TrimSpace(trimmedContent) == "" {
		return header + "\n" + entry + "\n"
	}
	return trimmedContent + "\n\n" + header + "\n" + entry + "\n"
}

func tomlLineStartsWithAnyKey(line string, keys []string) bool {
	for _, key := range keys {
		if strings.HasPrefix(line, key+" ") {
			return true
		}
	}
	return false
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
	if err := os.WriteFile(path, content, mode); err != nil { // #nosec G304,G703
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

func codexConfigPath() string {
	root := firstNonEmpty(os.Getenv("CODEX_HOME"), homePath(".codex"))
	return filepath.Join(root, "config.toml")
}

func claudeSettingsPath() string {
	root := firstNonEmpty(os.Getenv("CLAUDE_HOME"), homePath(".claude"))
	return filepath.Join(root, "settings.json")
}

func hermesHookDescriptorPath() string {
	root := firstNonEmpty(
		os.Getenv("PAXL_HERMES_HOME"),
		os.Getenv("HERMES_HOME"),
		homePath(".hermes"),
	)
	return filepath.Join(root, "paxl", "hooks", "user-prompt.json")
}

func piHookExtensionPath() string {
	root := firstNonEmpty(
		os.Getenv("PI_CODING_AGENT_DIR"),
		filepath.Join(genericAgentRoot(model.AgentNamePi), "agent"),
	)
	return filepath.Join(root, "extensions", "paxl-hook", "index.ts")
}

func kiroAgentConfigPath() string {
	return filepath.Join(genericAgentRoot(model.AgentNameKiro), "agents", "paxl.json")
}

func kiroSettingsPath() string {
	return filepath.Join(genericAgentRoot(model.AgentNameKiro), "settings", "cli.json")
}

func genericHookDescriptorPath(agent model.AgentName) string {
	root := genericAgentRoot(agent)
	return filepath.Join(root, "paxl", "hooks", "user-prompt.json")
}

func genericAgentRoot(agent model.AgentName) string {
	switch agent {
	case model.AgentNamePi:
		return firstNonEmpty(os.Getenv("PI_HOME"), homePath(".pi"))
	case model.AgentNameKiro:
		return firstNonEmpty(os.Getenv("KIRO_HOME"), homePath(".kiro"))
	case model.AgentNameOpenClaw:
		return firstNonEmpty(os.Getenv("OPENCLAW_HOME"), homePath(".openclaw"))
	case model.AgentNameUnknown,
		model.AgentNameCodex,
		model.AgentNameClaude,
		model.AgentNameGemini,
		model.AgentNameHermes,
		model.AgentNamePaxl:
		return homePath("." + string(agent))
	default:
		return homePath("." + string(agent))
	}
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

func shellCommandToken(value string) string {
	if value == "" {
		return "paxl"
	}
	if strings.ContainsAny(value, " \t\n'\"\\$`") {
		return shellQuote(value)
	}
	return value
}

func defaultStorePath() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "paxl", "paxl.sqlite"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "paxl", "paxl.sqlite"), nil
}

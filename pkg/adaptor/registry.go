package adaptor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

type Adapter interface {
	Info(ctx context.Context, req *InfoRequest, opts ...func(*Option)) (*InfoResponse, error)
	ListSessions(
		ctx context.Context,
		req *ListSessionsRequest,
		opts ...func(*Option),
	) (*ListSessionsResponse, error)
	GetSession(
		ctx context.Context,
		req *GetSessionRequest,
		opts ...func(*Option),
	) (*GetSessionResponse, error)
	Prompt(ctx context.Context, req *PromptRequest, opts ...func(*Option)) (*PromptResponse, error)
	StartSession(
		ctx context.Context,
		req *StartSessionRequest,
		opts ...func(*Option),
	) (*StartSessionResponse, error)
}

type InfoRequest struct{}

type InfoResponse struct {
	Agent *model.AgentInfo
}

type ListRequest struct{}

type ListResponse struct {
	Agents []*model.AgentInfo
}

type LookupRequest struct {
	Name model.AgentName
}

type LookupResponse struct {
	Adapter Adapter
}

type ListSessionsRequest struct {
	Limit int
}

type ListSessionsResponse struct {
	Sessions []*model.Session
}

type GetSessionRequest struct {
	NativeID string
}

type GetSessionResponse struct {
	Elements []*model.Element
}

type PromptRequest struct {
	NativeID string
	Text     string
}

type PromptResponse struct{}

type StartSessionRequest struct {
	Text string
}

type StartSessionResponse struct{}

type Registry struct {
	adapters []Adapter
}

func NewDefaultRegistry() *Registry {
	return &Registry{
		adapters: []Adapter{
			NewCodexAdapter(),
			NewClaudeAdapter(),
		},
	}
}

func (r *Registry) List(
	ctx context.Context,
	req *ListRequest,
	opts ...func(*Option),
) (*ListResponse, error) {
	_ = req
	option := applyOptions(opts)
	agents := make([]*model.AgentInfo, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		resp, err := adapter.Info(ctx, &InfoRequest{}, func(next *Option) {
			next.VerboseWriter = option.VerboseWriter
		})
		if err != nil {
			return nil, fmt.Errorf("load adapter info: %w", err)
		}
		agents = append(agents, resp.Agent)
	}
	return &ListResponse{Agents: agents}, nil
}

func (r *Registry) Lookup(
	ctx context.Context,
	req *LookupRequest,
	opts ...func(*Option),
) (*LookupResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("lookup adapter: request is required")
	}
	parsed, err := model.ParseAgentName(string(req.Name))
	if err != nil {
		return nil, fmt.Errorf("lookup adapter: %w", err)
	}
	resp, err := r.List(ctx, &ListRequest{}, opts...)
	if err != nil {
		return nil, fmt.Errorf("lookup adapter: %w", err)
	}
	for i, agent := range resp.Agents {
		if agent.Name == parsed {
			return &LookupResponse{Adapter: r.adapters[i]}, nil
		}
	}
	return nil, fmt.Errorf("lookup adapter %q: not registered", parsed)
}

type staticAdapter struct {
	agent        *model.AgentInfo
	probe        func() bool
	listSessions func(ctx context.Context, req *ListSessionsRequest) (*ListSessionsResponse, error)
	getSession   func(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error)
	prompt       func(ctx context.Context, req *PromptRequest, option *Option) (*PromptResponse, error)
	startSession func(ctx context.Context, req *StartSessionRequest, option *Option) (*StartSessionResponse, error)
}

func (a *staticAdapter) Info(
	ctx context.Context,
	req *InfoRequest,
	opts ...func(*Option),
) (*InfoResponse, error) {
	_ = ctx
	_ = req
	_ = applyOptions(opts)
	agent := *a.agent
	agent.Available = a.probe()
	if !agent.Available && agent.Reason == "" {
		agent.Reason = "Agent command or local session directory is unavailable."
	}
	return &InfoResponse{Agent: &agent}, nil
}

func (a *staticAdapter) ListSessions(
	ctx context.Context,
	req *ListSessionsRequest,
	opts ...func(*Option),
) (*ListSessionsResponse, error) {
	_ = applyOptions(opts)
	if a.listSessions == nil {
		return nil, fmt.Errorf("agent %s does not support session listing", a.agent.Name)
	}
	resp, err := a.listSessions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("list %s sessions: %w", a.agent.Name, err)
	}
	return resp, nil
}

func (a *staticAdapter) GetSession(
	ctx context.Context,
	req *GetSessionRequest,
	opts ...func(*Option),
) (*GetSessionResponse, error) {
	_ = applyOptions(opts)
	if a.getSession == nil {
		return nil, fmt.Errorf("agent %s does not support session transcripts", a.agent.Name)
	}
	resp, err := a.getSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get %s session: %w", a.agent.Name, err)
	}
	return resp, nil
}

func (a *staticAdapter) Prompt(
	ctx context.Context,
	req *PromptRequest,
	opts ...func(*Option),
) (*PromptResponse, error) {
	option := applyOptions(opts)
	if a.prompt == nil {
		return nil, fmt.Errorf("agent %s does not support prompt delivery", a.agent.Name)
	}
	resp, err := a.prompt(ctx, req, option)
	if err != nil {
		return nil, fmt.Errorf("prompt %s session: %w", a.agent.Name, err)
	}
	return resp, nil
}

func (a *staticAdapter) StartSession(
	ctx context.Context,
	req *StartSessionRequest,
	opts ...func(*Option),
) (*StartSessionResponse, error) {
	option := applyOptions(opts)
	if a.startSession == nil {
		return nil, fmt.Errorf("agent %s does not support new sessions", a.agent.Name)
	}
	resp, err := a.startSession(ctx, req, option)
	if err != nil {
		return nil, fmt.Errorf("start %s session: %w", a.agent.Name, err)
	}
	return resp, nil
}

func NewCodexAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameCodex,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"codex"},
			Reason:     "Run Codex locally so ~/.codex/sessions exists and install the codex CLI.",
		},
		probe: func() bool {
			return commandExists("codex") ||
				pathExists(filepath.Join(homeDir(), ".codex", "sessions"))
		},
		listSessions: listCodexSessions,
		getSession:   getCodexSession,
		prompt:       promptCodexSession,
		startSession: startCodexSession,
	}
}

func NewClaudeAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameClaude,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"claude"},
			Reason:     "Run Claude Code locally so ~/.claude/projects exists and install the claude CLI.",
		},
		probe: func() bool {
			return commandExists("claude") ||
				pathExists(filepath.Join(homeDir(), ".claude", "projects"))
		},
		listSessions: listClaudeSessions,
		getSession:   getClaudeSession,
		prompt:       promptClaudeSession,
		startSession: startClaudeSession,
	}
}

func promptCodexSession(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	if req == nil || req.NativeID == "" || req.Text == "" {
		return nil, fmt.Errorf("native session id and prompt text are required")
	}
	if err := validateNativeSessionID(req.NativeID); err != nil {
		return nil, err
	}
	return runPromptCommand(
		ctx,
		[]string{"codex", "exec", "resume", "--all", req.NativeID, "-"},
		req.Text,
		option,
	)
}

func promptClaudeSession(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	if req == nil || req.NativeID == "" || req.Text == "" {
		return nil, fmt.Errorf("native session id and prompt text are required")
	}
	if err := validateNativeSessionID(req.NativeID); err != nil {
		return nil, err
	}
	return runPromptCommand(
		ctx,
		[]string{"claude", "--print", "--resume", req.NativeID},
		req.Text,
		option,
	)
}

func startCodexSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runPromptCommand(
		ctx,
		[]string{"codex", "exec", "-"},
		req.Text,
		option,
	); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func startClaudeSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runPromptCommand(
		ctx,
		[]string{"claude", "--print"},
		req.Text,
		option,
	); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func runPromptCommand(
	ctx context.Context,
	argv []string,
	text string,
	option *Option,
) (*PromptResponse, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("prompt command is required")
	}
	// The command name is selected by built-in adapters; external session ids are only CLI args.
	command := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204
	command.Stdin = strings.NewReader(text)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("run %s: %w: %s", strings.Join(argv, " "), err, stderr.String())
	}
	writeCommandOutput(option, "stdout", stdout.String())
	writeCommandOutput(option, "stderr", stderr.String())
	return &PromptResponse{}, nil
}

func validateNativeSessionID(nativeID string) error {
	if strings.HasPrefix(nativeID, "-") {
		return fmt.Errorf("native session id must not start with '-'")
	}
	return nil
}

func writeCommandOutput(option *Option, stream string, output string) {
	if option == nil || option.VerboseWriter == nil || strings.TrimSpace(output) == "" {
		return
	}
	if _, err := fmt.Fprintf(
		option.VerboseWriter,
		"Command %s: %s\n",
		stream,
		ensureTerminalPeriod(strings.TrimSpace(output)),
	); err != nil {
		return
	}
}

func ensureTerminalPeriod(value string) string {
	if strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

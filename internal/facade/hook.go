package facade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

type AgentHookFacade struct {
	client        AuthHTTPClient
	store         *store.Store
	sessionFacade *SessionFacade
}

const hookAcceptedInboxReconcileLimit = 20

var hookChannelPollTimeout = 2 * time.Second

type AgentHookRequest struct {
	Agent       model.AgentName
	Event       string
	SessionID   string
	ProjectPath string
	Prompt      string
}

type AgentHookResponse struct {
	Injection *model.KnowledgeInjection
	Message   string
}

type CompleteAgentHookRequest struct {
	InjectionID string
}

type CompleteAgentHookResponse struct {
	Injection *model.KnowledgeInjection
}

type DeliverAgentHookRequest struct {
	Agent       model.AgentName
	SessionID   string
	InjectionID string
	Message     string
}

type DeliverAgentHookResponse struct {
	DeliveryMethod string
	Message        string
}

func NewAgentHookFacade(sessionStore *store.Store) *AgentHookFacade {
	return &AgentHookFacade{store: sessionStore}
}

// NewAgentHookFacadeWithSession creates an AgentHookFacade with a SessionFacade
// for turn-end async sync support.
func NewAgentHookFacadeWithSession(sessionStore *store.Store, sf *SessionFacade) *AgentHookFacade {
	return &AgentHookFacade{store: sessionStore, sessionFacade: sf}
}

func (f *AgentHookFacade) Run(
	ctx context.Context,
	req *AgentHookRequest,
	opts ...func(*Option),
) (*AgentHookResponse, error) {
	option := applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("run agent hook: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("run agent hook: store is required")
	}
	event := normalizeHookEvent(req.Event)
	switch event {
	case "user_prompt":
		return f.handleUserPromptHook(ctx, req, option)
	case "turn_end":
		return f.handleTurnEndHook(ctx, req, option)
	default:
		return nil, fmt.Errorf("unsupported hook event %q", req.Event)
	}
}

func (f *AgentHookFacade) handleUserPromptHook(
	ctx context.Context,
	req *AgentHookRequest,
	option *Option,
) (*AgentHookResponse, error) {
	f.acceptPendingInboxRoutes(ctx, option.VerboseWriter)
	claimed, err := f.store.ClaimHookKnowledgeInjection(
		ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:       req.Agent,
			SessionID:   req.SessionID,
			ProjectPath: req.ProjectPath,
			Prompt:      req.Prompt,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return &AgentHookResponse{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim hook knowledge injection: %w", err)
	}
	message := renderKnowledgeHandoff(
		claimed.Capsule,
		claimed.Injection,
		actionItemsFromJSON(claimed.Injection.ActionItemsJSON),
	)
	return &AgentHookResponse{Injection: claimed.Injection, Message: message}, nil
}

// handleTurnEndHook fires an async session sync for the turn-end event.
// It returns immediately; the agent is never blocked. If the session is not
// in the store or the session facade is not configured, it is a silent no-op.
func (f *AgentHookFacade) handleTurnEndHook(
	ctx context.Context,
	req *AgentHookRequest,
	option *Option,
) (*AgentHookResponse, error) {
	if req.SessionID == "" || req.Agent == model.AgentNameUnknown {
		return &AgentHookResponse{}, nil
	}
	if f.sessionFacade == nil {
		return &AgentHookResponse{}, nil
	}
	session, err := f.store.FindSession(ctx,
		&store.FindSessionRequest{ID: req.SessionID, Agent: req.Agent})
	if errors.Is(err, sql.ErrNoRows) || session == nil {
		return &AgentHookResponse{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find turn-end session: %w", err)
	}
	writeHookVerbose(option.VerboseWriter,
		"Turn-end hook: firing async sync for %s session %s.",
		req.Agent, req.SessionID)
	f.sessionFacade.SyncSessionAsync(ctx, req.Agent, req.SessionID)
	return &AgentHookResponse{}, nil
}

func (f *AgentHookFacade) acceptPendingInboxRoutes(ctx context.Context, verbose io.Writer) {
	channels := []string{"manager"}
	listed, err := f.store.ListChannelProfiles(ctx, &store.ListChannelProfilesRequest{
		EnabledOnly: true, AutoReceiveOnly: true,
	})
	if err != nil {
		writeHookVerbose(verbose, "Skip auto-receive channel discovery: %v.", err)
	} else {
		for _, profile := range listed.Profiles {
			channels = append(channels, profile.Name)
		}
	}
	for _, channel := range channels {
		channelContext, cancel := context.WithTimeout(ctx, hookChannelPollTimeout)
		f.acceptPendingChannelRoutes(channelContext, channel, verbose)
		cancel()
	}
}

func (f *AgentHookFacade) acceptPendingChannelRoutes(
	ctx context.Context,
	channel string,
	verbose io.Writer,
) {
	envelopeFacade := NewEnvelopeFacade(f.client, f.store)
	resp, err := envelopeFacade.AcceptAll(ctx, &AcceptAllEnvelopesRequest{
		Channel:         channel,
		Status:          "pending",
		ContinueOnError: true,
	})
	if err != nil {
		writeHookVerbose(
			verbose,
			"Skip %s inbox accept-all before hook injection: %v.",
			channel,
			err,
		)
		return
	}
	if len(resp.Accepted) > 0 || len(resp.Failures) > 0 {
		writeHookVerbose(
			verbose,
			"Accepted %d %s inbox envelopes before hook injection with %d failures.",
			len(resp.Accepted),
			channel,
			len(resp.Failures),
		)
	}
	synced, err := envelopeFacade.AcceptAll(ctx, &AcceptAllEnvelopesRequest{
		Channel:         channel,
		Status:          "accepted",
		Limit:           hookAcceptedInboxReconcileLimit,
		ContinueOnError: true,
	})
	if err != nil {
		writeHookVerbose(
			verbose,
			"Skip accepted %s inbox sync before hook injection: %v.",
			channel,
			err,
		)
		return
	}
	if len(synced.Accepted) > 0 || len(synced.Failures) > 0 {
		writeHookVerbose(
			verbose,
			"Synced %d accepted %s inbox envelopes before hook injection with %d failures.",
			len(synced.Accepted),
			channel,
			len(synced.Failures),
		)
	}
}

func writeHookVerbose(writer io.Writer, format string, args ...any) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, format+"\n", args...)
}

func normalizeHookEvent(event string) string {
	switch strings.TrimSpace(event) {
	case "user-prompt", "user_prompt", "UserPromptSubmit":
		return "user_prompt"
	case "pre_llm_call":
		return "user_prompt"
	case "turn-end", "turn_end", "Stop", "stop", "SessionEnd", "session_end",
		"on_session_finalize", "session_finalize":
		return "turn_end"
	default:
		return strings.TrimSpace(event)
	}
}

func (f *AgentHookFacade) Deliver(
	ctx context.Context,
	req *DeliverAgentHookRequest,
	opts ...func(*Option),
) (*DeliverAgentHookResponse, error) {
	_ = ctx
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("deliver agent hook: request is required")
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("deliver agent hook: message is required")
	}
	switch req.Agent {
	case model.AgentNameCodex:
		message, err := renderCodexUserPromptSubmitHookOutput(req.Message)
		if err != nil {
			return nil, err
		}
		return &DeliverAgentHookResponse{
			DeliveryMethod: "stdout",
			Message:        message,
		}, nil
	case model.AgentNameUnknown,
		model.AgentNameClaude,
		model.AgentNamePi,
		model.AgentNameKiro,
		model.AgentNameOpenCode,
		model.AgentNameKimi,
		model.AgentNameGemini,
		model.AgentNameOpenClaw,
		model.AgentNamePaxl:
		return &DeliverAgentHookResponse{DeliveryMethod: "stdout", Message: req.Message}, nil
	case model.AgentNameHermes:
		message, err := renderHermesPreLLMCallHookOutput(req.Message)
		if err != nil {
			return nil, err
		}
		return &DeliverAgentHookResponse{DeliveryMethod: "stdout", Message: message}, nil
	}
	return &DeliverAgentHookResponse{DeliveryMethod: "stdout", Message: req.Message}, nil
}

func (f *AgentHookFacade) Complete(
	ctx context.Context,
	req *CompleteAgentHookRequest,
	opts ...func(*Option),
) (*CompleteAgentHookResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("complete agent hook: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("complete agent hook: store is required")
	}
	consumed, err := f.store.MarkKnowledgeInjectionConsumed(
		ctx,
		&store.MarkKnowledgeInjectionConsumedRequest{InjectionID: req.InjectionID},
	)
	if err != nil {
		return nil, fmt.Errorf("mark hook knowledge injection consumed: %w", err)
	}
	return &CompleteAgentHookResponse{Injection: consumed.Injection}, nil
}

type codexUserPromptSubmitHookOutput struct {
	Continue           bool                              `json:"continue"`
	SuppressOutput     bool                              `json:"suppressOutput"`
	HookSpecificOutput codexUserPromptSubmitHookSpecific `json:"hookSpecificOutput"`
}

type codexUserPromptSubmitHookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func renderCodexUserPromptSubmitHookOutput(message string) (string, error) {
	output := codexUserPromptSubmitHookOutput{
		Continue:       true,
		SuppressOutput: true,
		HookSpecificOutput: codexUserPromptSubmitHookSpecific{
			HookEventName:     "UserPromptSubmit",
			AdditionalContext: message,
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("render codex hook output: %w", err)
	}
	return string(raw), nil
}

func renderHermesPreLLMCallHookOutput(message string) (string, error) {
	raw, err := json.Marshal(map[string]string{"context": message})
	if err != nil {
		return "", fmt.Errorf("render Hermes hook output: %w", err)
	}
	return string(raw), nil
}

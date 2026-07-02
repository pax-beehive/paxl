package facade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type CapsuleFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestCapsuleFacadeSuite(t *testing.T) {
	suite.Run(t, new(CapsuleFacadeSuite))
}

func (s *CapsuleFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedSyncedSession()
}

func (s *CapsuleFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *CapsuleFacadeSuite) TestLocalCapsuleLifecycle() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	s.Equal("bridge", created.Capsule.Keyword)
	s.Contains(created.Capsule.Content, "Bridge content")

	listed, err := capsuleFacade.List(s.ctx, &facade.ListCapsulesRequest{Status: "active"})
	s.Require().NoError(err)
	s.Require().Len(listed.Capsules, 1)

	got, err := capsuleFacade.Get(
		s.ctx,
		&facade.GetCapsuleRequest{CapsuleID: created.Capsule.CapsuleID},
	)
	s.Require().NoError(err)
	s.Equal(created.Capsule.CapsuleID, got.Capsule.CapsuleID)

	archived, err := capsuleFacade.Archive(s.ctx, &facade.ArchiveCapsuleRequest{
		CapsuleID: created.Capsule.CapsuleID,
	})
	s.Require().NoError(err)
	s.Equal("archived", archived.Capsule.Status)
}

func (s *CapsuleFacadeSuite) TestLocalCapsuleExtractionKeepsLargerContextWindow() {
	elements := make([]*model.Element, 0, 80)
	for index := 1; index <= 80; index++ {
		elements = append(elements, &model.Element{
			Seq:         int64(index),
			Type:        "message",
			Role:        "assistant",
			ContentText: fmt.Sprintf("Bridge detail %03d", index),
		})
	}
	_, err := s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess",
		Elements:  elements,
	})
	s.Require().NoError(err)
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})

	s.Require().NoError(err)
	s.Contains(resp.Capsule.Content, "Bridge detail 080")
	s.False(resp.Capsule.Truncated)
}

func (s *CapsuleFacadeSuite) TestCreateLoadsUncachedSourceSession() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-uncached-source.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"uncached-source","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Bridge detail from uncached source"}]}}`+"\n",
		),
		0o600,
	))
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:uncached-source",
		Keyword:         "bridge",
		Local:           true,
	})

	s.Require().NoError(err)
	s.Equal("codex:uncached-source", created.Capsule.SourceSessionID)
	s.Contains(created.Capsule.Content, "Bridge detail from uncached source")
}

func (s *CapsuleFacadeSuite) TestCreateUsesSourceAgentGenerationByDefault() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexCapsuleGenerator(rolloutPath)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Require().NoError(err)
	s.Equal("Generated bridge", resp.Capsule.Title)
	s.Contains(resp.Capsule.Content, "Generated bridge content")
	s.Contains(resp.Capsule.Content, "Action items:\n1. Run generated tests")
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationMarksTruncatedContent() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexLargeCapsuleGenerator(rolloutPath)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Require().NoError(err)
	s.True(resp.Capsule.Truncated)
	s.Less(len(resp.Capsule.Content), int(resp.Capsule.OriginalEstimatedChars))
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationAcceptsModelRole() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexModelCapsuleGenerator(rolloutPath)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Require().NoError(err)
	s.Equal("Generated bridge", resp.Capsule.Title)
	s.Contains(resp.Capsule.Content, "Generated bridge content")
	s.Contains(resp.Capsule.Content, "Action items:\n1. Run generated tests")
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationUsesLatestAgentResult() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexTwoCapsuleGenerator(rolloutPath)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Require().NoError(err)
	s.Equal("Fresh bridge", resp.Capsule.Title)
	s.Contains(resp.Capsule.Content, "Fresh generated bridge content")
	s.NotContains(resp.Capsule.Content, "Stale generated bridge content")
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationFailsWhenMarkersAreMissing() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	s.seedCodexRollout()
	s.installFakeCodex("/dev/null")

	_, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationIgnoresUserMarkerEcho() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexUserMarkerGenerator(rolloutPath)

	_, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestCreateSourceGenerationIgnoresToolResultMarkerEcho() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	rolloutPath := s.seedCodexRollout()
	s.installFakeCodexToolResultMarkerGenerator(rolloutPath)

	_, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestCreateManualCapsuleDoesNotLoadSourceSession() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		Manual:  true,
		Keyword: "routed envelopes",
		Content: "Pax-manager must preserve route metadata for recipient-side hooks.",
	})

	s.Require().NoError(err)
	s.Equal("manual", resp.Capsule.SourceSessionID)
	s.Equal(model.AgentNamePaxl, resp.Capsule.SourceAgent)
	s.Equal("Knowledge capsule: routed envelopes", resp.Capsule.Title)
	s.Contains(resp.Capsule.Content, "recipient-side hooks")
}

func (s *CapsuleFacadeSuite) TestCreateManualCapsuleRejectsSourceSession() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	_, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		Manual:          true,
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Content:         "Bridge content",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestCreateProvidedCapsuleLoadsSourceSession() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	resp, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "handoff",
		Title:           "Prepared handoff",
		Summary:         "Prepared summary",
		Content:         "Prepared context for the target session.",
	})

	s.Require().NoError(err)
	s.Equal("codex:sess", resp.Capsule.SourceSessionID)
	s.Equal(model.AgentNameCodex, resp.Capsule.SourceAgent)
	s.Equal("Prepared handoff", resp.Capsule.Title)
	s.Equal("Prepared summary", resp.Capsule.Summary)
	s.Equal("Prepared context for the target session.", resp.Capsule.Content)
}

func (s *CapsuleFacadeSuite) TestInjectQueuesTargetSessionForHookDelivery() {
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	s.seedTargetSession()

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
	})

	s.Require().NoError(err)
	s.Equal(created.Capsule.CapsuleID, injected.Injection.CapsuleID)
	s.Equal("codex:target", injected.Injection.TargetSessionID)
	s.Equal("pending", injected.Injection.Status)
	s.Equal("hook", injected.Injection.DeliveryMethod)
	s.Equal("session", injected.Injection.RouteMatchType)
	s.Equal("codex:target", injected.Injection.RouteMatchValue)
	s.Equal("local-node", injected.Injection.SourceNodeID)
	s.Equal(model.AgentNameCodex, injected.Injection.SourceAgent)
	s.Equal("codex:sess", injected.Injection.SourceSessionID)
	s.Equal("local-node", injected.Injection.TargetNodeID)

	listed, err := capsuleFacade.ListInjections(s.ctx, &facade.ListInjectionsRequest{
		TargetSessionID: "codex:target",
	})
	s.Require().NoError(err)
	s.Require().Len(listed.Injections, 1)
	s.Equal(injected.Injection.InjectionID, listed.Injections[0].InjectionID)

	hookFacade := facade.NewAgentHookFacade(s.store)
	noop, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "other",
		Prompt:    "next prompt",
	})
	s.Require().NoError(err)
	s.Empty(noop.Message)

	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "target",
		Prompt:    "next prompt",
	})
	s.Require().NoError(err)
	s.Contains(consumed.Message, "system_handoff")
	s.Contains(consumed.Message, "NO ACTIONABLE ITEMS")
	s.Contains(consumed.Message, "From:\nNode: local-node\nAgent: codex\nSession: codex:sess")
	s.Contains(consumed.Message, "To:\nNode: local-node\nAgent: codex\nSession: codex:target")
	s.Contains(consumed.Message, "Bridge content")
}

func (s *CapsuleFacadeSuite) TestQueuedTargetSessionInjectionCanIncludeActionItems() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	s.seedTargetSession()

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
		ActionItems: []string{
			"run go test ./...",
			"open a PR",
		},
	})

	s.Require().NoError(err)
	s.Equal("pending", injected.Injection.Status)

	hookFacade := facade.NewAgentHookFacade(s.store)
	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "target",
		Prompt:    "next prompt",
	})
	s.Require().NoError(err)
	s.Contains(consumed.Message, "ACTION ITEMS")
	s.Contains(consumed.Message, "1. run go test ./...")
	s.Contains(consumed.Message, "2. open a PR")
	s.NotContains(consumed.Message, "NO ACTIONABLE ITEMS")
}

func (s *CapsuleFacadeSuite) TestQueueHookInjectionAndConsumeFromMatchingPrompt() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	queued, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:  created.Capsule.CapsuleID,
		Agent:      model.AgentNameClaude,
		MatchType:  "keyword",
		MatchValue: "handoff",
		ActionItems: []string{
			"run hook tests",
			"open the hook PR",
		},
	})
	s.Require().NoError(err)
	s.Equal("pending", queued.Injection.Status)
	s.Equal("hook", queued.Injection.DeliveryMethod)
	s.JSONEq(`["run hook tests","open the hook PR"]`, queued.Injection.ActionItemsJSON)

	hookFacade := facade.NewAgentHookFacade(s.store)
	noop, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "claude-session",
		Prompt:    "unrelated prompt",
	})
	s.Require().NoError(err)
	s.Empty(noop.Message)

	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user_prompt",
		SessionID: "claude-session",
		Prompt:    "please use the handoff",
	})
	s.Require().NoError(err)
	s.Contains(consumed.Message, "system_handoff")
	s.Contains(consumed.Message, "Bridge content")
	s.Contains(consumed.Message, "1. run hook tests")
	s.Contains(consumed.Message, "2. open the hook PR")
	s.Equal("claimed", consumed.Injection.Status)
	s.Equal("claude:claude-session", consumed.Injection.TargetSessionID)

	completed, err := hookFacade.Complete(s.ctx, &facade.CompleteAgentHookRequest{
		InjectionID: consumed.Injection.InjectionID,
	})
	s.Require().NoError(err)
	s.Equal("consumed", completed.Injection.Status)

	again, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "claude-session",
		Prompt:    "please use the handoff",
	})
	s.Require().NoError(err)
	s.Empty(again.Message)
}

func (s *CapsuleFacadeSuite) TestAgentHookFacadeHandlesNoMatchAndInvalidRequests() {
	hookFacade := facade.NewAgentHookFacade(s.store)

	_, err := hookFacade.Run(s.ctx, nil)
	s.Error(err)

	_, err = facade.NewAgentHookFacade(nil).Run(s.ctx, &facade.AgentHookRequest{
		Agent: model.AgentNameClaude,
		Event: "user-prompt",
	})
	s.Error(err)

	_, err = hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent: model.AgentNameClaude,
		Event: "post-prompt",
	})
	s.Error(err)

	resp, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "claude-session",
		Prompt:    "nothing matches",
	})
	s.Require().NoError(err)
	s.Empty(resp.Message)

	_, err = hookFacade.Complete(s.ctx, nil)
	s.Error(err)

	_, err = facade.NewAgentHookFacade(nil).Complete(
		s.ctx,
		&facade.CompleteAgentHookRequest{InjectionID: "kci_missing"},
	)
	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestAgentHookFacadeDeliversCodexAdditionalContextJSON() {
	hookFacade := facade.NewAgentHookFacade(s.store)

	delivered, err := hookFacade.Deliver(s.ctx, &facade.DeliverAgentHookRequest{
		Agent:   model.AgentNameCodex,
		Message: "system_handoff content",
	})

	s.Require().NoError(err)
	s.Equal("stdout", delivered.DeliveryMethod)
	var output map[string]any
	s.Require().NoError(json.Unmarshal([]byte(delivered.Message), &output))
	s.Equal(true, output["continue"])
	s.Equal(true, output["suppressOutput"])
	specific, ok := output["hookSpecificOutput"].(map[string]any)
	s.Require().True(ok)
	s.Equal("UserPromptSubmit", specific["hookEventName"])
	s.Equal("system_handoff content", specific["additionalContext"])

	fallback, err := hookFacade.Deliver(s.ctx, &facade.DeliverAgentHookRequest{
		Agent:   model.AgentNameClaude,
		Message: "plain handoff",
	})
	s.Require().NoError(err)
	s.Equal("stdout", fallback.DeliveryMethod)
	s.Equal("plain handoff", fallback.Message)
}

func (s *CapsuleFacadeSuite) TestInjectStartsNewTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeClaude(capturePath)

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:  created.Capsule.CapsuleID,
		Agent:      model.AgentNameClaude,
		NewSession: true,
	})

	s.Require().NoError(err)
	s.Equal(created.Capsule.CapsuleID, injected.Injection.CapsuleID)
	s.Equal(model.AgentNameClaude, injected.Injection.TargetAgent)
	s.Equal("cli_new_session", injected.Injection.DeliveryMethod)
	s.Contains(injected.Injection.TargetSessionID, "new claude session")
	s.Contains(injected.Message, "system_handoff")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal(injected.Message, string(rawPrompt))
	s.Contains(
		injected.Message,
		"NO ACTIONABLE ITEMS: This is knowledge transfer only.",
	)
	s.Contains(
		injected.Message,
		"Acknowledge receipt only; do not start implementation or run tools.",
	)
}

func (s *CapsuleFacadeSuite) TestInjectRejectsMissingTargetAgentForNewSession() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)

	_, err = capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:  created.Capsule.CapsuleID,
		NewSession: true,
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "target agent is required")
}

func (s *CapsuleFacadeSuite) TestInjectQueuesUncachedTargetSessionForHookDelivery() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-target.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"target","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Target exists"}]}}`+"\n",
		),
		0o600,
	))

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
	})

	s.Require().NoError(err)
	s.Equal("codex:target", injected.Injection.TargetSessionID)
	s.Equal("hook", injected.Injection.DeliveryMethod)
	s.Equal("pending", injected.Injection.Status)
	s.Equal("session", injected.Injection.RouteMatchType)

	hookFacade := facade.NewAgentHookFacade(s.store)
	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "target",
		Prompt:    "next prompt",
	})
	s.Require().NoError(err)
	s.Contains(
		consumed.Message,
		"NO ACTIONABLE ITEMS: This is knowledge transfer only.",
	)
	s.Contains(
		consumed.Message,
		"Acknowledge receipt only; do not start implementation or run tools.",
	)
}

func (s *CapsuleFacadeSuite) TestInjectRejectsArchivedCapsule() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	_, err = capsuleFacade.Archive(s.ctx, &facade.ArchiveCapsuleRequest{
		CapsuleID: created.Capsule.CapsuleID,
	})
	s.Require().NoError(err)

	_, err = capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestMirrorSessionDeliversToExistingSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	s.seedTargetSession()
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	resp, err := capsuleFacade.MirrorSession(s.ctx, &facade.MirrorSessionRequest{
		SourceSessionID: "codex:sess",
		TargetSessionID: "codex:target",
	})

	s.Require().NoError(err)
	s.Equal("cli_resume", resp.Injection.DeliveryMethod)
	s.Equal("codex:target", resp.Injection.TargetSessionID)
	s.Equal("local-node", resp.Injection.SourceNodeID)
	s.Equal(model.AgentNameCodex, resp.Injection.SourceAgent)
	s.Equal("codex:sess", resp.Injection.SourceSessionID)
	s.Equal("local-node", resp.Injection.TargetNodeID)
	s.Contains(resp.Capsule.Title, "Session mirror")
	s.Contains(resp.Capsule.Summary, "without asking the source agent to summarize")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "This context was mirrored by paxl")
	s.Contains(string(rawPrompt), "From:\nNode: local-node\nAgent: codex\nSession: codex:sess")
	s.Contains(string(rawPrompt), "To:\nNode: local-node\nAgent: codex\nSession: codex:target")
	s.Contains(string(rawPrompt), "Bridge content")
	s.Contains(string(rawPrompt), "Assistant context")
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationMovesSessionContextThroughUnifiedHandoff() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)
	s.seedTargetSession()
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	resp, err := collaborationFacade.MoveSessionContext(
		s.ctx,
		&facade.MoveSessionContextRequest{
			SourceSessionID: "codex:sess",
			TargetSessionID: "codex:target",
		},
	)

	s.Require().NoError(err)
	s.Equal(facade.LocalCommunicationKindSessionHandoff, resp.Communication.Kind)
	s.Equal("codex:sess", resp.Communication.SourceSessionID)
	s.Equal("codex:target", resp.Communication.TargetSessionID)
	s.Equal(model.AgentNameCodex, resp.Communication.SourceAgent)
	s.Equal(model.AgentNameCodex, resp.Communication.TargetAgent)
	s.Equal("cli_resume", resp.Communication.DeliveryMethod)
	s.Contains(resp.Message, "system_handoff")

	injections, err := s.store.ListKnowledgeInjections(
		s.ctx,
		&store.ListKnowledgeInjectionsRequest{TargetSessionID: "codex:target"},
	)
	s.Require().NoError(err)
	s.Require().Len(injections.Injections, 1)
	s.Equal("delivered", injections.Injections[0].Status)

	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "This context was mirrored by paxl")
	s.Contains(string(rawPrompt), "From:\nNode: local-node\nAgent: codex\nSession: codex:sess")
	s.Contains(string(rawPrompt), "To:\nNode: local-node\nAgent: codex\nSession: codex:target")
	s.Contains(string(rawPrompt), "Bridge content")
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationSharesSessionMemoryThroughReusableCapsule() {
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)
	s.seedTargetSession()

	resp, err := collaborationFacade.ShareSessionMemory(
		s.ctx,
		&facade.ShareSessionMemoryRequest{
			SourceSessionID: "codex:sess",
			Keyword:         "bridge",
			Local:           true,
			Route: &facade.LocalMemoryRoute{
				Kind:      facade.LocalMemoryRouteKindSession,
				SessionID: "codex:target",
			},
			ActionItems: []string{"review the bridge memory"},
		},
	)

	s.Require().NoError(err)
	s.Equal(facade.LocalCommunicationKindMemoryHandoff, resp.Communication.Kind)
	s.Equal("bridge", resp.Capsule.Keyword)
	s.Contains(resp.Capsule.Content, "Bridge content")
	s.Equal("pending", resp.Injection.Status)
	s.Equal("hook", resp.Injection.DeliveryMethod)
	s.Equal("session", resp.Injection.RouteMatchType)
	s.Equal("codex:target", resp.Injection.TargetSessionID)
	s.Equal(model.AgentNameCodex, resp.Injection.TargetAgent)
	s.Empty(resp.Message)

	hookFacade := facade.NewAgentHookFacade(s.store)
	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "target",
		Prompt:    "continue",
	})
	s.Require().NoError(err)
	s.Contains(consumed.Message, "system_handoff")
	s.Contains(consumed.Message, "Bridge content")
	s.Contains(consumed.Message, "1. review the bridge memory")
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationCreatesMemoryWithoutDelivery() {
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)

	resp, err := collaborationFacade.ShareSessionMemory(
		s.ctx,
		&facade.ShareSessionMemoryRequest{
			SourceSessionID: "codex:sess",
			Keyword:         "bridge",
			Local:           true,
		},
	)

	s.Require().NoError(err)
	s.Equal(facade.LocalCommunicationKindMemoryHandoff, resp.Communication.Kind)
	s.Equal("codex:sess", resp.Communication.SourceSessionID)
	s.Equal(model.AgentNameCodex, resp.Communication.SourceAgent)
	s.Equal("bridge", resp.Capsule.Keyword)
	s.Nil(resp.Injection)
	s.Empty(resp.Message)
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationQueuesExistingMemoryByKeywordRoute() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)

	resp, err := collaborationFacade.QueueMemoryDelivery(
		s.ctx,
		&facade.QueueMemoryDeliveryRequest{
			CapsuleID: created.Capsule.CapsuleID,
			Route: &facade.LocalMemoryRoute{
				Kind:    facade.LocalMemoryRouteKindKeyword,
				Agent:   model.AgentNameClaude,
				Keyword: "handoff",
			},
			ActionItems: []string{"apply the queued memory"},
		},
	)

	s.Require().NoError(err)
	s.Equal(facade.LocalCommunicationKindMemoryHandoff, resp.Communication.Kind)
	s.Equal("pending", resp.Injection.Status)
	s.Equal("hook", resp.Injection.DeliveryMethod)
	s.Equal("keyword", resp.Injection.RouteMatchType)
	s.Equal("handoff", resp.Injection.RouteMatchValue)
	s.Equal(model.AgentNameClaude, resp.Injection.TargetAgent)

	hookFacade := facade.NewAgentHookFacade(s.store)
	noop, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "claude-session",
		Prompt:    "unrelated",
	})
	s.Require().NoError(err)
	s.Empty(noop.Message)

	consumed, err := hookFacade.Run(s.ctx, &facade.AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "claude-session",
		Prompt:    "please use this handoff",
	})
	s.Require().NoError(err)
	s.Contains(consumed.Message, "system_handoff")
	s.Contains(consumed.Message, "Bridge content")
	s.Contains(consumed.Message, "1. apply the queued memory")
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationQueuesExistingMemoryByBroadRoutes() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	created, err := capsuleFacade.Create(s.ctx, &facade.CreateCapsuleRequest{
		SourceSessionID: "codex:sess",
		Keyword:         "bridge",
		Local:           true,
	})
	s.Require().NoError(err)
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)
	cases := []struct {
		name       string
		route      *facade.LocalMemoryRoute
		matchType  string
		matchValue string
	}{
		{
			name:      "any",
			route:     &facade.LocalMemoryRoute{Kind: facade.LocalMemoryRouteKindAny},
			matchType: "any",
		},
		{
			name: "project",
			route: &facade.LocalMemoryRoute{
				Kind:    facade.LocalMemoryRouteKindProject,
				Agent:   model.AgentNameClaude,
				Project: "paxl",
			},
			matchType:  "project",
			matchValue: "paxl",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			resp, err := collaborationFacade.QueueMemoryDelivery(
				s.ctx,
				&facade.QueueMemoryDeliveryRequest{
					CapsuleID: created.Capsule.CapsuleID,
					Route:     tc.route,
				},
			)

			s.Require().NoError(err)
			s.Equal("pending", resp.Injection.Status)
			s.Equal("hook", resp.Injection.DeliveryMethod)
			s.Equal(tc.matchType, resp.Injection.RouteMatchType)
			s.Equal(tc.matchValue, resp.Injection.RouteMatchValue)
		})
	}
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationSharesSessionMemoryToNewSessionRoute() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeClaude(capturePath)

	resp, err := collaborationFacade.ShareSessionMemory(
		s.ctx,
		&facade.ShareSessionMemoryRequest{
			SourceSessionID: "codex:sess",
			Keyword:         "bridge",
			Local:           true,
			Route: &facade.LocalMemoryRoute{
				Kind:  facade.LocalMemoryRouteKindNewSession,
				Agent: model.AgentNameClaude,
			},
		},
	)

	s.Require().NoError(err)
	s.Equal(facade.LocalCommunicationKindMemoryHandoff, resp.Communication.Kind)
	s.Equal("delivered", resp.Injection.Status)
	s.Equal("cli_new_session", resp.Injection.DeliveryMethod)
	s.Equal(model.AgentNameClaude, resp.Injection.TargetAgent)
	s.Contains(resp.Message, "system_handoff")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal(resp.Message, string(rawPrompt))
}

func (s *CapsuleFacadeSuite) TestLocalCollaborationRejectsInvalidMemoryRoutes() {
	collaborationFacade := facade.NewLocalCollaborationFacade(nil, s.store)
	cases := []struct {
		name string
		req  *facade.QueueMemoryDeliveryRequest
	}{
		{name: "nil route", req: &facade.QueueMemoryDeliveryRequest{CapsuleID: "kcap_1"}},
		{
			name: "unknown route",
			req: &facade.QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route:     &facade.LocalMemoryRoute{Kind: facade.LocalMemoryRouteKindUnknown},
			},
		},
		{
			name: "unsupported route",
			req: &facade.QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route:     &facade.LocalMemoryRoute{Kind: "agent"},
			},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := collaborationFacade.QueueMemoryDelivery(s.ctx, tc.req)
			s.Error(err)
		})
	}
}

func (s *CapsuleFacadeSuite) TestMirrorSessionStartsNewTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeClaude(capturePath)

	resp, err := capsuleFacade.MirrorSession(s.ctx, &facade.MirrorSessionRequest{
		SourceSessionID: "codex:sess",
		TargetAgent:     model.AgentNameClaude,
	})

	s.Require().NoError(err)
	s.Equal("cli_new_session", resp.Injection.DeliveryMethod)
	s.Equal(model.AgentNameClaude, resp.Injection.TargetAgent)
	s.Contains(resp.Injection.TargetSessionID, "new claude session")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(
		string(rawPrompt),
		"To:\nNode: local-node\nAgent: claude\nSession: (new claude session)",
	)
	s.Contains(string(rawPrompt), "Bridge content")
	s.Contains(string(rawPrompt), "Assistant context")
}

func (s *CapsuleFacadeSuite) TestMirrorSessionRejectsMissingTargetAgentForNewSession() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)

	_, err := capsuleFacade.MirrorSession(s.ctx, &facade.MirrorSessionRequest{
		SourceSessionID: "codex:sess",
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestMirrorSessionRequiresStore() {
	capsuleFacade := facade.NewCapsuleFacade(nil, nil)

	_, err := capsuleFacade.MirrorSession(s.ctx, &facade.MirrorSessionRequest{
		SourceSessionID: "codex:sess",
		TargetAgent:     model.AgentNameClaude,
	})

	s.Error(err)
}

func (s *CapsuleFacadeSuite) TestRejectsNilRequests() {
	capsuleFacade := facade.NewCapsuleFacade(nil, s.store)
	cases := []struct {
		name string
		run  func() error
	}{
		{name: "create", run: func() error {
			_, err := capsuleFacade.Create(s.ctx, nil)
			return err
		}},
		{name: "list", run: func() error {
			_, err := capsuleFacade.List(s.ctx, nil)
			return err
		}},
		{name: "get", run: func() error {
			_, err := capsuleFacade.Get(s.ctx, nil)
			return err
		}},
		{name: "archive", run: func() error {
			_, err := capsuleFacade.Archive(s.ctx, nil)
			return err
		}},
		{name: "inject", run: func() error {
			_, err := capsuleFacade.Inject(s.ctx, nil)
			return err
		}},
		{name: "list injections", run: func() error {
			_, err := capsuleFacade.ListInjections(s.ctx, nil)
			return err
		}},
		{name: "mirror", run: func() error {
			_, err := capsuleFacade.MirrorSession(s.ctx, nil)
			return err
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Error(tc.run())
		})
	}
}

func (s *CapsuleFacadeSuite) seedSyncedSession() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess", Title: "Session"},
		},
	})
	s.Require().NoError(err)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess",
		Elements: []*model.Element{
			{
				Seq:         1,
				Type:        "message",
				Role:        "user",
				CompletedAt: "2026-06-20T01:00:00Z",
				ContentText: "Bridge content for transfer",
			},
			{
				Seq:         2,
				Type:        "message",
				Role:        "assistant",
				CompletedAt: "2026-06-20T01:01:00Z",
				ContentText: "Assistant context without the keyword",
			},
		},
	})
	s.Require().NoError(err)
}

func (s *CapsuleFacadeSuite) seedTargetSession() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "target", Title: "Target"},
		},
	})
	s.Require().NoError(err)
}

func (s *CapsuleFacadeSuite) installFakeCodex(capturePath string) {
	s.installFakeCommand("codex", capturePath)
}

func (s *CapsuleFacadeSuite) installFakeClaude(capturePath string) {
	s.installFakeCommand("claude", capturePath)
}

func (s *CapsuleFacadeSuite) installFakeCommand(name string, capturePath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte("#!/bin/sh\ncat > "+capturePath+"\n"),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CapsuleFacadeSuite) seedCodexRollout() string {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	rolloutPath := filepath.Join(rolloutDir, "rollout-test-sess.jsonl")
	s.Require().NoError(os.WriteFile(
		rolloutPath,
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Bridge content for source generation"}]}}`+"\n",
		),
		0o600,
	))
	return rolloutPath
}

func (s *CapsuleFacadeSuite) installFakeCodexCapsuleGenerator(rolloutPath string) {
	s.installFakeCodexRoleCapsuleGenerator(rolloutPath, "assistant")
}

func (s *CapsuleFacadeSuite) installFakeCodexModelCapsuleGenerator(rolloutPath string) {
	s.installFakeCodexRoleCapsuleGenerator(rolloutPath, "model")
}

func (s *CapsuleFacadeSuite) installFakeCodexTwoCapsuleGenerator(rolloutPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"assistant\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Stale bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Stale summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Stale generated bridge content\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:04:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"assistant\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Fresh bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Fresh summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Fresh generated bridge content\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CapsuleFacadeSuite) installFakeCodexRoleCapsuleGenerator(rolloutPath string, role string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"" + role + "\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Generated bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Generated summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Generated bridge content\\\\\\\",\\\\\\\"action_items\\\\\\\":[\\\\\\\"Run generated tests\\\\\\\"]}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CapsuleFacadeSuite) installFakeCodexUserMarkerGenerator(rolloutPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"user\\\",\\\"content\\\":[{\\\"type\\\":\\\"input_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Echo\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Echo\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Echo\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CapsuleFacadeSuite) installFakeCodexToolResultMarkerGenerator(rolloutPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"toolResult\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Tool echo\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Tool echo\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Tool echo\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CapsuleFacadeSuite) installFakeCodexLargeCapsuleGenerator(rolloutPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"content=$(printf 'x%.0s' $(seq 1 33000))\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"assistant\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Large bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Generated summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"${content}\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

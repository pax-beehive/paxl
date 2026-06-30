package store

import (
	"testing"

	"github.com/pax-oss/paxl/internal/model"
)

func TestHookInjectionMatchesSessionRoute(t *testing.T) {
	injection := &model.KnowledgeInjection{
		TargetAgent:     model.AgentNameCodex,
		RouteMatchType:  "session",
		RouteMatchValue: "codex:target",
	}

	if !hookInjectionMatches(&ClaimHookKnowledgeInjectionRequest{
		Agent:     model.AgentNameCodex,
		SessionID: "target",
	}, injection) {
		t.Fatal("hookInjectionMatches() = false, want true")
	}
	if hookInjectionMatches(&ClaimHookKnowledgeInjectionRequest{
		Agent:     model.AgentNameCodex,
		SessionID: "other",
	}, injection) {
		t.Fatal("hookInjectionMatches() = true for wrong session")
	}
}

func TestHookInjectionMatchesRejectsWrongAgent(t *testing.T) {
	injection := &model.KnowledgeInjection{
		TargetAgent:     model.AgentNameClaude,
		RouteMatchType:  "session",
		RouteMatchValue: "claude:target",
	}

	if hookInjectionMatches(&ClaimHookKnowledgeInjectionRequest{
		Agent:     model.AgentNameCodex,
		SessionID: "target",
	}, injection) {
		t.Fatal("hookInjectionMatches() = true for wrong agent")
	}
}

package facade

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

func TestInjectionHookRouteDerivesSessionRouteFromTypedID(t *testing.T) {
	route, err := injectionHookRoute(&InjectCapsuleRequest{
		TargetSessionID: "codex:target",
	})

	if err != nil {
		t.Fatalf("injectionHookRoute() error = %v", err)
	}
	if route.Agent != model.AgentNameCodex {
		t.Fatalf("Agent = %q, want codex", route.Agent)
	}
	if route.TargetSessionID != "codex:target" {
		t.Fatalf("TargetSessionID = %q", route.TargetSessionID)
	}
	if route.MatchType != "session" || route.MatchValue != "codex:target" {
		t.Fatalf("route = %#v", route)
	}
}

func TestInjectionHookRouteDerivesSessionRouteFromBareIDAndAgent(t *testing.T) {
	route, err := injectionHookRoute(&InjectCapsuleRequest{
		Agent:           model.AgentNameClaude,
		TargetSessionID: "target",
	})

	if err != nil {
		t.Fatalf("injectionHookRoute() error = %v", err)
	}
	if route.TargetSessionID != "claude:target" || route.MatchValue != "claude:target" {
		t.Fatalf("route = %#v", route)
	}
}

func TestInjectionHookRoutePreservesExplicitConditionalRoute(t *testing.T) {
	route, err := injectionHookRoute(&InjectCapsuleRequest{
		Agent:      model.AgentNameHermes,
		MatchType:  "keyword",
		MatchValue: "paxl",
	})

	if err != nil {
		t.Fatalf("injectionHookRoute() error = %v", err)
	}
	if route.Agent != model.AgentNameHermes ||
		route.TargetSessionID != "" ||
		route.MatchType != "keyword" ||
		route.MatchValue != "paxl" {
		t.Fatalf("route = %#v", route)
	}
}

func TestInjectionHookRouteRejectsAmbiguousBareSession(t *testing.T) {
	_, err := injectionHookRoute(&InjectCapsuleRequest{TargetSessionID: "target"})

	if err == nil {
		t.Fatal("injectionHookRoute() error = nil, want error")
	}
}

func TestSortSessionsNewestFirstUsesFallbackTimestamps(t *testing.T) {
	sessions := []*model.Session{
		nil,
		{ID: "listed", LastListedAt: "2026-06-20T03:00:00Z"},
		{ID: "active", LastActive: "2026-06-20T04:00:00Z"},
		{ID: "updated", UpdatedAt: "2026-06-20T05:00:00Z"},
	}

	sortSessionsNewestFirst(sessions)

	got := []string{sessions[0].ID, sessions[1].ID, sessions[2].ID}
	want := []string{"updated", "active", "listed"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted ids = %#v, want %#v", got, want)
		}
	}
}

func TestSearchSyncLimitUsesMinimumWindow(t *testing.T) {
	if got := searchSyncLimit(5); got != defaultSearchSyncLimit {
		t.Fatalf("searchSyncLimit(5) = %d, want %d", got, defaultSearchSyncLimit)
	}
	if got := searchSyncLimit(75); got != 75 {
		t.Fatalf("searchSyncLimit(75) = %d, want 75", got)
	}
}

func TestSanitizeFTSQueryPreservesPhrasesAndTrimsOperators(t *testing.T) {
	got := sanitizeFTSQuery(`AND docker+(deploy) OR "exact phrase" NOT`)
	want := `docker  deploy  OR "exact phrase"`
	if got != want {
		t.Fatalf("sanitizeFTSQuery() = %q, want %q", got, want)
	}
}

func TestRecentlySynced(t *testing.T) {
	if !recentlySynced(time.Now().UTC().Format(time.RFC3339)) {
		t.Fatal("recentlySynced(now) = false, want true")
	}
	if recentlySynced(time.Now().Add(-2 * syncThrottleDuration).UTC().Format(time.RFC3339)) {
		t.Fatal("recentlySynced(old) = true, want false")
	}
	if recentlySynced("not-a-time") {
		t.Fatal("recentlySynced(invalid) = true, want false")
	}
	if recentlySynced("") {
		t.Fatal("recentlySynced(empty) = true, want false")
	}
}

func TestLoadTargetSessionUsesStoredSession(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(ctx, &store.OpenRequest{
		Path: filepath.Join(t.TempDir(), "paxl.sqlite"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := opened.Store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}()
	_, err = opened.Store.UpsertSessions(ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "target", Title: "Target"},
		},
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	facade := NewCapsuleFacade(nil, opened.Store)

	target, err := facade.loadTargetSession(ctx, &InjectCapsuleRequest{
		TargetSessionID: "codex:target",
	})

	if err != nil {
		t.Fatalf("loadTargetSession() error = %v", err)
	}
	if target.ID != "codex:target" {
		t.Fatalf("target.ID = %q", target.ID)
	}
}

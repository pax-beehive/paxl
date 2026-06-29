package facade_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type SessionFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestSessionFacadeSuite(t *testing.T) {
	suite.Run(t, new(SessionFacadeSuite))
}

func (s *SessionFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
}

func (s *SessionFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *SessionFacadeSuite) TestListSyncsCodexSessionsIntoStore() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(`{"id":"sess-1","thread_name":"Codex","updated_at":"2026-06-20T01:00:00Z"}`+"\n"),
		0o600,
	))
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.List(s.ctx, &facade.ListSessionsRequest{
		Agents: []model.AgentName{model.AgentNameCodex},
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("codex:sess-1", resp.Sessions[0].ID)
}

func (s *SessionFacadeSuite) TestListCanUseCachedStoreWithoutSyncing() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameClaude,
		Sessions: []*model.Session{
			{NativeID: "cached", Title: "Cached"},
		},
	})
	s.Require().NoError(err)
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.List(s.ctx, &facade.ListSessionsRequest{NoSync: true})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("claude:cached", resp.Sessions[0].ID)
}

func (s *SessionFacadeSuite) TestListFiltersCachedSessionsByUpdatedSince() {
	now := time.Now().UTC()
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{
				NativeID:  "fresh",
				Title:     "Fresh",
				UpdatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
			},
			{
				NativeID:  "old",
				Title:     "Old",
				UpdatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
			},
		},
	})
	s.Require().NoError(err)
	cutoff := now.Add(-24 * time.Hour)
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.List(s.ctx, &facade.ListSessionsRequest{
		UpdatedSince: &cutoff,
		NoSync:       true,
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("codex:fresh", resp.Sessions[0].ID)
}

func (s *SessionFacadeSuite) TestGetSyncsSessionElementsWhenNeeded() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-get.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-20T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}}`+"\n",
		),
		0o600,
	))
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-get", Title: "Get"},
		},
	})
	s.Require().NoError(err)
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:sess-get"})

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Hello", resp.Elements[0].ContentText)
}

func (s *SessionFacadeSuite) TestGetRefreshesCachedSessionElements() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-refresh.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-20T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Old cached"}]}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"New live"}]}}`+"\n",
		),
		0o600,
	))
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-refresh", Title: "Refresh"},
		},
	})
	s.Require().NoError(err)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-refresh",
		Elements: []*model.Element{
			{
				SessionID:   "codex:sess-refresh",
				Seq:         1,
				Type:        "message",
				Role:        "user",
				ContentText: "Old cached",
			},
		},
	})
	s.Require().NoError(err)
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:sess-refresh"})

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("Old cached", resp.Elements[0].ContentText)
	s.Equal("New live", resp.Elements[1].ContentText)
}

func (s *SessionFacadeSuite) TestGetReturnsCachedSessionElementsWhenRefreshFails() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "cached-only", Title: "Cached only"},
		},
	})
	s.Require().NoError(err)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:cached-only",
		Elements: []*model.Element{
			{
				SessionID:   "codex:cached-only",
				Seq:         1,
				Type:        "message",
				Role:        "user",
				ContentText: "Cached content",
			},
		},
	})
	s.Require().NoError(err)
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:cached-only"})

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Cached content", resp.Elements[0].ContentText)
}

func (s *SessionFacadeSuite) TestGetLoadsUncachedSessionFromAgentLogs() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-uncached.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"uncached","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Uncached content"}]}}`+"\n",
		),
		0o600,
	))
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:uncached"})

	s.Require().NoError(err)
	s.Equal("codex:uncached", resp.Session.ID)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Uncached content", resp.Elements[0].ContentText)

	cached, err := s.store.FindSession(
		s.ctx,
		&store.FindSessionRequest{ID: "codex:uncached"},
	)
	s.Require().NoError(err)
	s.NotZero(cached.Session.CurrentSyncVersion)
}

func (s *SessionFacadeSuite) TestGetLoadsBareNativeIDWhenAgentIsProvided() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-bare-native.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"bare-native","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Bare native content"}]}}`+"\n",
		),
		0o600,
	))
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{
		ID:    "bare-native",
		Agent: model.AgentNameCodex,
	})

	s.Require().NoError(err)
	s.Equal("codex:bare-native", resp.Session.ID)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Bare native content", resp.Elements[0].ContentText)
}

func (s *SessionFacadeSuite) TestGetBareNativeIDRequiresAgentForUncachedLookup() {
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	_, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "native-only"})

	s.ErrorIs(err, sql.ErrNoRows)
}

func (s *SessionFacadeSuite) TestListRequiresRequestAndStore() {
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	_, err := sessionFacade.List(s.ctx, nil)
	s.Error(err)

	withoutStore := facade.NewSessionFacade(nil, nil)
	_, err = withoutStore.List(s.ctx, &facade.ListSessionsRequest{})
	s.Error(err)
}

func (s *SessionFacadeSuite) TestGetRequiresRequestAndStore() {
	sessionFacade := facade.NewSessionFacade(nil, s.store)

	_, err := sessionFacade.Get(s.ctx, nil)
	s.Error(err)

	withoutStore := facade.NewSessionFacade(nil, nil)
	_, err = withoutStore.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:sess"})
	s.Error(err)
}

func (s *SessionFacadeSuite) TestSearchReturnsResultsFromStore() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-search", Title: "Docker deploy"},
		},
	})
	s.Require().NoError(err)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-search",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "docker deployment config"},
		},
	})
	s.Require().NoError(err)

	sessionFacade := facade.NewSessionFacade(nil, s.store)
	resp, err := sessionFacade.Search(s.ctx, &facade.SearchRequest{
		Query: "docker",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Require().Len(resp.Results, 1)
	s.Equal("codex:sess-search", resp.Results[0].SessionID)
	s.Equal(model.AgentNameCodex, resp.Results[0].Agent)
	s.Equal("Docker deploy", resp.Results[0].Title)
}

func (s *SessionFacadeSuite) TestSearchReturnsEmptyForNoMatches() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameClaude,
		Sessions: []*model.Session{
			{NativeID: "sess-no-search", Title: "No match"},
		},
	})
	s.Require().NoError(err)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "claude:sess-no-search",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Hello world"},
		},
	})
	s.Require().NoError(err)

	sessionFacade := facade.NewSessionFacade(nil, s.store)
	resp, err := sessionFacade.Search(s.ctx, &facade.SearchRequest{
		Query: "nonexistent",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Empty(resp.Results)
}

func (s *SessionFacadeSuite) TestSearchRequiresRequest() {
	sessionFacade := facade.NewSessionFacade(nil, s.store)
	_, err := sessionFacade.Search(s.ctx, nil)
	s.Error(err)
}

func (s *SessionFacadeSuite) TestGetSkipsSyncWhenRecentlySynced() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	// Write a session with "Old" content
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-throttle.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-20T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Original"}]}}`+"\n",
		),
		0o600,
	))
	// Insert session with a recent last_synced_at
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "test-throttle", Title: "Throttle", UpdatedAt: "2026-06-20T01:00:00Z"},
		},
	})
	s.Require().NoError(err)
	// Pre-sync elements and set last_synced_at to now
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:test-throttle",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Pre-synced content"},
		},
	})
	s.Require().NoError(err)
	// Set last_synced_at to now via direct DB access
	_, err = s.store.UpdateSessionLastSyncedAt(s.ctx, &store.UpdateSessionLastSyncedAtRequest{
		SessionID: "codex:test-throttle",
	})
	s.Require().NoError(err)

	sessionFacade := facade.NewSessionFacade(nil, s.store)
	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:test-throttle"})
	s.Require().NoError(err)
	// Should return cached content, not re-sync from adapter
	s.Require().Len(resp.Elements, 1)
	s.Equal("Pre-synced content", resp.Elements[0].ContentText)
}

func (s *SessionFacadeSuite) TestGetSyncsWhenThrottleExpired() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-throttle-expired.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-20T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fresh from adapter"}]}}`+"\n",
		),
		0o600,
	))
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "test-throttle-expired", Title: "Throttle expired", UpdatedAt: "2026-06-20T01:00:00Z"},
		},
	})
	s.Require().NoError(err)
	// Pre-sync with old timestamp
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:test-throttle-expired",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Old cached"},
		},
	})
	s.Require().NoError(err)
	// Set last_synced_at to 31 minutes ago — just past the 30min throttle
	oldTime := time.Now().UTC().Add(-31 * time.Minute).Format(time.RFC3339)
	_, err = s.store.UpdateSessionLastSyncedAt(s.ctx, &store.UpdateSessionLastSyncedAtRequest{
		SessionID:    "codex:test-throttle-expired",
		LastSyncedAt: oldTime,
	})
	s.Require().NoError(err)

	sessionFacade := facade.NewSessionFacade(nil, s.store)
	resp, err := sessionFacade.Get(s.ctx, &facade.GetSessionRequest{ID: "codex:test-throttle-expired"})
	s.Require().NoError(err)
	// Should re-sync from adapter, replacing cached content
	s.Require().Len(resp.Elements, 1)
	s.Equal("Fresh from adapter", resp.Elements[0].ContentText)
}

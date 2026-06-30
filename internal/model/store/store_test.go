package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type StoreSuite struct {
	suite.Suite
	ctx   context.Context
	path  string
	store *store.Store
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	s.ctx = context.Background()
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.path = dbPath
	opened, err := store.Open(s.ctx, &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	s.store = opened.Store
}

func (s *StoreSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *StoreSuite) TestOpenUsesXDGDataHomeWhenPathIsEmpty() {
	dataHome := s.T().TempDir()
	s.T().Setenv("XDG_DATA_HOME", dataHome)

	opened, err := store.Open(s.ctx, &store.OpenRequest{})
	s.Require().NoError(err)
	defer func() {
		s.Require().NoError(opened.Store.Close())
	}()

	_, err = os.Stat(filepath.Join(dataHome, "paxl", "paxl.sqlite"))
	s.NoError(err)
}

func (s *StoreSuite) TestOpenRequiresRequest() {
	_, err := store.Open(s.ctx, nil)

	s.Error(err)
}

func (s *StoreSuite) TestNilStoreCloseIsNoop() {
	var nilStore *store.Store

	s.NoError(nilStore.Close())
}

func (s *StoreSuite) TestAuthCredentialLifecycle() {
	initial, err := s.store.GetAuthCredential(s.ctx)
	s.Require().NoError(err)
	s.Nil(initial.Credential)

	_, err = s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:   "https://manager.example",
			APIKey:       "paxu_test",
			UserAPIKeyID: "key-1",
			NodeID:       "node_paxl",
			UserID:       "usr_1",
			Email:        "cli@example.com",
			DisplayName:  "CLI",
			Role:         "user",
		},
	})
	s.Require().NoError(err)

	got, err := s.store.GetAuthCredential(s.ctx)
	s.Require().NoError(err)
	s.Require().NotNil(got.Credential)
	s.Equal("https://manager.example", got.Credential.ManagerURL)
	s.Equal("paxu_test", got.Credential.APIKey)
	s.Equal("node_paxl", got.Credential.NodeID)

	_, err = s.store.DeleteAuthCredential(s.ctx)
	s.Require().NoError(err)
	got, err = s.store.GetAuthCredential(s.ctx)
	s.Require().NoError(err)
	s.Nil(got.Credential)
}

func (s *StoreSuite) TestGetAuthCredentialScansNullableOptionalFields() {
	db, err := sql.Open("sqlite", s.path)
	s.Require().NoError(err)
	defer func() {
		s.Require().NoError(db.Close())
	}()
	_, err = db.ExecContext(s.ctx, `
		INSERT INTO auth_credentials (
			id, manager_url, api_key, user_id, email, created_at, updated_at
		)
		VALUES ('default', 'https://manager.example', 'paxu_test', 'usr_1',
			'cli@example.com', '2026-06-22T00:00:00Z', '2026-06-22T00:00:00Z')
	`)
	s.Require().NoError(err)

	got, err := s.store.GetAuthCredential(s.ctx)

	s.Require().NoError(err)
	s.Require().NotNil(got.Credential)
	s.Empty(got.Credential.UserAPIKeyID)
	s.Empty(got.Credential.NodeID)
	s.Empty(got.Credential.DisplayName)
	s.Empty(got.Credential.Role)
}

func (s *StoreSuite) TestSaveAuthCredentialPreservesExistingCreatedAtWhenUpdating() {
	createdAt := "2026-06-22T00:00:00Z"
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_first",
			UserID:     "usr_1",
			Email:      "cli@example.com",
			CreatedAt:  createdAt,
		},
	})
	s.Require().NoError(err)

	updated, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_second",
			UserID:     "usr_1",
			Email:      "cli@example.com",
		},
	})

	s.Require().NoError(err)
	s.Equal(createdAt, updated.Credential.CreatedAt)
	got, err := s.store.GetAuthCredential(s.ctx)
	s.Require().NoError(err)
	s.Equal(createdAt, got.Credential.CreatedAt)
	s.Equal("paxu_second", got.Credential.APIKey)
}

func (s *StoreSuite) TestUpsertAndListSessions() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{
				NativeID:  "sess-1",
				Title:     "First session",
				Status:    "available",
				UpdatedAt: "2026-06-20T01:00:00Z",
			},
		},
	})
	s.Require().NoError(err)

	resp, err := s.store.ListSessions(s.ctx, &store.ListSessionsRequest{})
	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("codex:sess-1", resp.Sessions[0].ID)
	s.Equal(model.AgentNameCodex, resp.Sessions[0].Agent)
}

func (s *StoreSuite) TestListSessionsFiltersByAgentAndLimit() {
	for _, agent := range []model.AgentName{model.AgentNameCodex, model.AgentNameClaude} {
		_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
			Agent: agent,
			Sessions: []*model.Session{
				{NativeID: string(agent) + "-1", Title: string(agent)},
			},
		})
		s.Require().NoError(err)
	}

	resp, err := s.store.ListSessions(s.ctx, &store.ListSessionsRequest{
		Agents: []model.AgentName{model.AgentNameClaude},
		Limit:  1,
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal(model.AgentNameClaude, resp.Sessions[0].Agent)
}

func (s *StoreSuite) TestFindSessionAcceptsAgentQualifiedBareID() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-2", Title: "Second session"},
		},
	})
	s.Require().NoError(err)

	resp, err := s.store.FindSession(s.ctx, &store.FindSessionRequest{
		ID:    "sess-2",
		Agent: model.AgentNameCodex,
	})

	s.Require().NoError(err)
	s.Equal("codex:sess-2", resp.Session.ID)
}

func (s *StoreSuite) TestReplaceAndReadSessionElements() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-elements", Title: "Elements"},
		},
	})
	s.Require().NoError(err)

	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-elements",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Hello"},
		},
	})
	s.Require().NoError(err)

	found, err := s.store.FindSession(s.ctx, &store.FindSessionRequest{ID: "codex:sess-elements"})
	s.Require().NoError(err)
	elements, err := s.store.Elements(s.ctx, &store.ElementsRequest{Session: found.Session})

	s.Require().NoError(err)
	s.Require().Len(elements.Elements, 1)
	s.Equal("Hello", elements.Elements[0].ContentText)
}

func (s *StoreSuite) TestElementsReturnsEmptyForUnsyncedSession() {
	resp, err := s.store.Elements(
		s.ctx,
		&store.ElementsRequest{Session: &model.Session{ID: "codex:none"}},
	)

	s.Require().NoError(err)
	s.Empty(resp.Elements)
}

func (s *StoreSuite) TestFindSessionRejectsAmbiguousBareNativeID() {
	for _, agent := range []model.AgentName{model.AgentNameCodex, model.AgentNameClaude} {
		_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
			Agent: agent,
			Sessions: []*model.Session{
				{NativeID: "same-native-id", Title: string(agent)},
			},
		})
		s.Require().NoError(err)
	}

	_, err := s.store.FindSession(s.ctx, &store.FindSessionRequest{ID: "same-native-id"})

	s.Error(err)
}

func (s *StoreSuite) TestArchiveKnowledgeCapsuleReturnsNoRowsForMissingCapsule() {
	_, err := s.store.ArchiveKnowledgeCapsule(s.ctx, &store.ArchiveKnowledgeCapsuleRequest{
		CapsuleID: "missing",
	})

	s.Error(err)
}

func (s *StoreSuite) TestRejectsNilRequests() {
	cases := []struct {
		name string
		run  func() error
	}{
		{name: "upsert", run: func() error {
			_, err := s.store.UpsertSessions(s.ctx, nil)
			return err
		}},
		{name: "list", run: func() error {
			_, err := s.store.ListSessions(s.ctx, nil)
			return err
		}},
		{name: "find", run: func() error {
			_, err := s.store.FindSession(s.ctx, nil)
			return err
		}},
		{name: "replace elements", run: func() error {
			_, err := s.store.ReplaceSessionElements(s.ctx, nil)
			return err
		}},
		{name: "elements", run: func() error {
			_, err := s.store.Elements(s.ctx, nil)
			return err
		}},
		{name: "capsule", run: func() error {
			_, err := s.store.CreateKnowledgeCapsule(s.ctx, nil)
			return err
		}},
		{name: "list capsules", run: func() error {
			_, err := s.store.ListKnowledgeCapsules(s.ctx, nil)
			return err
		}},
		{name: "get capsule", run: func() error {
			_, err := s.store.GetKnowledgeCapsule(s.ctx, nil)
			return err
		}},
		{name: "injection", run: func() error {
			_, err := s.store.CreateKnowledgeInjection(s.ctx, nil)
			return err
		}},
		{name: "list injections", run: func() error {
			_, err := s.store.ListKnowledgeInjections(s.ctx, nil)
			return err
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.Error(tc.run())
		})
	}
}

func (s *StoreSuite) TestRejectsInvalidAgentFiltersBeforeQuerying() {
	_, err := s.store.ListSessions(s.ctx, &store.ListSessionsRequest{
		Agents: []model.AgentName{model.AgentName("qwen")},
	})

	s.Error(err)
}

func (s *StoreSuite) TestRejectsInvalidUpsertAgentBeforeWriting() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentName("qwen"),
		Sessions: []*model.Session{
			{NativeID: "sess"},
		},
	})

	s.Error(err)
}

func (s *StoreSuite) TestCapsuleLifecycleAndInjectionList() {
	created, err := s.store.CreateKnowledgeCapsule(s.ctx, &store.CreateKnowledgeCapsuleRequest{
		Capsule: &model.KnowledgeCapsule{
			CapsuleID:       "kcap_1",
			SourceNodeID:    "source-node",
			SourceSessionID: "codex:sess-1",
			SourceAgent:     model.AgentNameCodex,
			Keyword:         "bridge",
			Title:           "Bridge",
			Summary:         "Summary",
			Content:         "Content",
		},
	})
	s.Require().NoError(err)
	s.Equal("active", created.Capsule.Status)

	listed, err := s.store.ListKnowledgeCapsules(s.ctx, &store.ListKnowledgeCapsulesRequest{
		Status:          "active",
		Keyword:         "bridge",
		SourceSessionID: "codex:sess-1",
		Limit:           1,
	})
	s.Require().NoError(err)
	s.Require().Len(listed.Capsules, 1)
	s.Equal("kcap_1", listed.Capsules[0].CapsuleID)
	s.Equal("source-node", listed.Capsules[0].SourceNodeID)

	got, err := s.store.GetKnowledgeCapsule(s.ctx, &store.GetKnowledgeCapsuleRequest{
		CapsuleID: "kcap_1",
	})
	s.Require().NoError(err)
	s.Equal("Bridge", got.Capsule.Title)
	s.Equal("source-node", got.Capsule.SourceNodeID)

	archived, err := s.store.ArchiveKnowledgeCapsule(s.ctx, &store.ArchiveKnowledgeCapsuleRequest{
		CapsuleID: "kcap_1",
	})
	s.Require().NoError(err)
	s.Equal("archived", archived.Capsule.Status)

	_, err = s.store.CreateKnowledgeInjection(s.ctx, &store.CreateKnowledgeInjectionRequest{
		Injection: &model.KnowledgeInjection{
			InjectionID:     "kci_1",
			CapsuleID:       "kcap_1",
			SourceNodeID:    "source-node",
			SourceAgent:     model.AgentNameCodex,
			SourceSessionID: "codex:sess-1",
			TargetNodeID:    "target-node",
			TargetSessionID: "claude:target",
			TargetAgent:     model.AgentNameClaude,
			DeliveryMethod:  "cli_resume",
			ActionItemsJSON: `["run tests","open PR"]`,
		},
	})
	s.Require().NoError(err)

	injections, err := s.store.ListKnowledgeInjections(s.ctx, &store.ListKnowledgeInjectionsRequest{
		TargetSessionID: "claude:target",
	})
	s.Require().NoError(err)
	s.Require().Len(injections.Injections, 1)
	s.Equal("kci_1", injections.Injections[0].InjectionID)
	s.Equal("source-node", injections.Injections[0].SourceNodeID)
	s.Equal(model.AgentNameCodex, injections.Injections[0].SourceAgent)
	s.Equal("codex:sess-1", injections.Injections[0].SourceSessionID)
	s.Equal("target-node", injections.Injections[0].TargetNodeID)
	s.Equal("system_handoff", injections.Injections[0].DeliveryMessageType)
	s.Equal(`["run tests","open PR"]`, injections.Injections[0].ActionItemsJSON)
	s.Equal("rendered", injections.Injections[0].Status)
}

func (s *StoreSuite) TestClaimHookKnowledgeInjectionMatchesAndConsumesOnce() {
	_, err := s.store.CreateKnowledgeCapsule(s.ctx, &store.CreateKnowledgeCapsuleRequest{
		Capsule: &model.KnowledgeCapsule{
			CapsuleID:       "kcap_hook",
			SourceNodeID:    "source-node",
			SourceSessionID: "codex:sess-1",
			SourceAgent:     model.AgentNameCodex,
			Keyword:         "bridge",
			Title:           "Bridge",
			Summary:         "Summary",
			Content:         "Content",
		},
	})
	s.Require().NoError(err)
	_, err = s.store.CreateKnowledgeInjection(s.ctx, &store.CreateKnowledgeInjectionRequest{
		Injection: &model.KnowledgeInjection{
			InjectionID:         "kci_hook",
			CapsuleID:           "kcap_hook",
			SourceNodeID:        "source-node",
			SourceAgent:         model.AgentNameCodex,
			SourceSessionID:     "codex:sess-1",
			TargetNodeID:        "target-node",
			TargetAgent:         model.AgentNameClaude,
			DeliveryMethod:      "hook",
			DeliveryMessageType: "system_handoff",
			Status:              "pending",
			RouteMatchType:      "project",
			RouteMatchValue:     "paxl",
			ActionItemsJSON:     `["run hook tests"]`,
		},
	})
	s.Require().NoError(err)

	claimed, err := s.store.ClaimHookKnowledgeInjection(
		s.ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:       model.AgentNameClaude,
			SessionID:   "claude-session",
			ProjectPath: "/tmp/paxl",
			Prompt:      "hello",
		},
	)
	s.Require().NoError(err)
	s.Equal("kci_hook", claimed.Injection.InjectionID)
	s.Equal("claude:claude-session", claimed.Injection.TargetSessionID)
	s.Equal(`["run hook tests"]`, claimed.Injection.ActionItemsJSON)
	s.Equal("claimed", claimed.Injection.Status)
	s.Equal("Bridge", claimed.Capsule.Title)

	consumed, err := s.store.MarkKnowledgeInjectionConsumed(
		s.ctx,
		&store.MarkKnowledgeInjectionConsumedRequest{InjectionID: "kci_hook"},
	)
	s.Require().NoError(err)
	s.Equal("consumed", consumed.Injection.Status)

	_, err = s.store.ClaimHookKnowledgeInjection(
		s.ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:       model.AgentNameClaude,
			SessionID:   "claude-session",
			ProjectPath: "/tmp/paxl",
			Prompt:      "hello",
		},
	)
	s.ErrorIs(err, sql.ErrNoRows)
}

func (s *StoreSuite) TestClaimHookKnowledgeInjectionSupportsAnyAgentAndKeywordRoutes() {
	_, err := s.store.CreateKnowledgeCapsule(s.ctx, &store.CreateKnowledgeCapsuleRequest{
		Capsule: &model.KnowledgeCapsule{
			CapsuleID:       "kcap_routes",
			SourceNodeID:    "source-node",
			SourceSessionID: "codex:sess-1",
			SourceAgent:     model.AgentNameCodex,
			Keyword:         "bridge",
			Title:           "Bridge",
			Summary:         "Summary",
			Content:         "Content",
		},
	})
	s.Require().NoError(err)
	_, err = s.store.CreateKnowledgeInjection(s.ctx, &store.CreateKnowledgeInjectionRequest{
		Injection: &model.KnowledgeInjection{
			InjectionID:         "kci_any",
			CapsuleID:           "kcap_routes",
			SourceNodeID:        "source-node",
			SourceAgent:         model.AgentNameCodex,
			SourceSessionID:     "codex:sess-1",
			TargetNodeID:        "target-node",
			DeliveryMethod:      "hook",
			DeliveryMessageType: "system_handoff",
			Status:              "pending",
			RouteMatchType:      "any",
		},
	})
	s.Require().NoError(err)
	_, err = s.store.CreateKnowledgeInjection(s.ctx, &store.CreateKnowledgeInjectionRequest{
		Injection: &model.KnowledgeInjection{
			InjectionID:         "kci_keyword",
			CapsuleID:           "kcap_routes",
			SourceNodeID:        "source-node",
			SourceAgent:         model.AgentNameCodex,
			SourceSessionID:     "codex:sess-1",
			TargetNodeID:        "target-node",
			TargetAgent:         model.AgentNameClaude,
			DeliveryMethod:      "hook",
			DeliveryMessageType: "system_handoff",
			Status:              "pending",
			RouteMatchType:      "keyword",
			RouteMatchValue:     "handoff",
		},
	})
	s.Require().NoError(err)

	claimedAny, err := s.store.ClaimHookKnowledgeInjection(
		s.ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:     model.AgentNameHermes,
			SessionID: "hermes:prefixed-session",
			Prompt:    "anything",
		},
	)
	s.Require().NoError(err)
	s.Equal("kci_any", claimedAny.Injection.InjectionID)
	s.Equal("hermes:prefixed-session", claimedAny.Injection.TargetSessionID)

	claimedKeyword, err := s.store.ClaimHookKnowledgeInjection(
		s.ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:     model.AgentNameClaude,
			SessionID: "claude-session",
			Prompt:    "please load the handoff",
		},
	)
	s.Require().NoError(err)
	s.Equal("kci_keyword", claimedKeyword.Injection.InjectionID)
	s.Equal("claude:claude-session", claimedKeyword.Injection.TargetSessionID)
}

// ---------------------------------------------------------------------------
// FTS5 search tests (Task 1 + Task 2 + Task 5)
// ---------------------------------------------------------------------------

func (s *StoreSuite) TestFTS5TriggersExistAfterMigration() {
	db, err := sql.Open("sqlite", s.path)
	s.Require().NoError(err)
	defer func() { s.Require().NoError(db.Close()) }()

	for _, name := range []string{
		"session_elements_fts",
		"session_elements_fts_insert",
		"session_elements_fts_delete",
		"session_elements_fts_update",
	} {
		var found string
		err := db.QueryRowContext(
			s.ctx,
			`SELECT name FROM sqlite_master WHERE name = ?`,
			name,
		).Scan(&found)
		s.Require().NoError(err, "expected %s to exist after migration", name)
		s.Equal(name, found)
	}
}

func (s *StoreSuite) TestMigrationBackfillsFTS5ForExistingElements() {
	dbPath := filepath.Join(s.T().TempDir(), "old.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	s.Require().NoError(err)
	_, err = db.ExecContext(s.ctx, `
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			agent TEXT NOT NULL,
			native_id TEXT NOT NULL,
			title TEXT,
			status TEXT,
			preview TEXT,
			project_id TEXT,
			workspace_roots_json TEXT,
			last_active TEXT,
			updated_at TEXT,
			last_listed_at TEXT NOT NULL,
			last_synced_at TEXT,
			current_sync_version INTEGER DEFAULT 0,
			raw_json TEXT,
			UNIQUE(agent, native_id)
		);
		CREATE TABLE session_elements (
			session_id TEXT NOT NULL,
			sync_version INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			type TEXT NOT NULL,
			role TEXT,
			model TEXT,
			started_at TEXT,
			completed_at TEXT,
			duration_ms INTEGER DEFAULT 0,
			usage_json TEXT,
			content_text TEXT,
			normalized_json TEXT,
			raw_json TEXT,
			PRIMARY KEY(session_id, sync_version, seq)
		);
		INSERT INTO sessions (
			id, agent, native_id, title, last_listed_at, current_sync_version
		) VALUES (
			'codex:old-search', 'codex', 'old-search', 'Old searchable session',
			'2026-06-29T00:00:00Z', 1
		);
		INSERT INTO session_elements (
			session_id, sync_version, seq, type, role, content_text
		) VALUES (
			'codex:old-search', 1, 1, 'message', 'user', 'legacy searchable content'
		);
	`)
	s.Require().NoError(err)
	s.Require().NoError(db.Close())

	opened, err := store.Open(s.ctx, &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer func() {
		s.Require().NoError(opened.Store.Close())
	}()

	resp, err := opened.Store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "legacy",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Require().Len(resp.Results, 1)
	s.Equal("codex:old-search", resp.Results[0].SessionID)
}

func (s *StoreSuite) TestReplaceSessionElementsDeletesOldRows() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-replace", Title: "Replace test"},
		},
	})
	s.Require().NoError(err)

	// First sync: 2 elements
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-replace",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Hello"},
			{Seq: 2, Type: "message", Role: "assistant", ContentText: "World"},
		},
	})
	s.Require().NoError(err)

	// Second sync: 1 element (should replace, not append)
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-replace",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Updated"},
		},
	})
	s.Require().NoError(err)

	found, err := s.store.FindSession(s.ctx, &store.FindSessionRequest{ID: "codex:sess-replace"})
	s.Require().NoError(err)
	elements, err := s.store.Elements(s.ctx, &store.ElementsRequest{Session: found.Session})
	s.Require().NoError(err)

	s.Require().Len(elements.Elements, 1, "old elements should be deleted, not accumulated")
	s.Equal("Updated", elements.Elements[0].ContentText)
}

func (s *StoreSuite) TestSearchElementsReturnsMatchesByBM25Rank() {
	// Insert two sessions with overlapping content
	for _, sess := range []struct {
		agent   model.AgentName
		native  string
		title   string
		content string
	}{
		{model.AgentNameCodex, "sess-docker", "Docker deploy", "docker deployment pipeline configuration"},
		{model.AgentNameClaude, "sess-k8s", "K8s deploy", "docker and kubernetes orchestration"},
	} {
		_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
			Agent: sess.agent,
			Sessions: []*model.Session{
				{NativeID: sess.native, Title: sess.title},
			},
		})
		s.Require().NoError(err)

		_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
			SessionID: string(sess.agent) + ":" + sess.native,
			Elements: []*model.Element{
				{Seq: 1, Type: "message", Role: "user", ContentText: sess.content},
			},
		})
		s.Require().NoError(err)
	}

	resp, err := s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "docker",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Require().Len(resp.Results, 2, "both sessions contain 'docker'")

	// Results should have agent and title populated
	for _, r := range resp.Results {
		s.NotEmpty(r.SessionID)
		s.NotEmpty(r.Agent)
		s.NotEmpty(r.Title)
		s.NotEmpty(r.Snippet)
	}
}

func (s *StoreSuite) TestSearchElementsReturnsEmptyForNoMatches() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-no-match", Title: "No match"},
		},
	})
	s.Require().NoError(err)

	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-no-match",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "Hello world"},
		},
	})
	s.Require().NoError(err)

	resp, err := s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "nonexistent",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Empty(resp.Results)
}

func (s *StoreSuite) TestSearchElementsRespectsLimit() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-limit", Title: "Limit test"},
		},
	})
	s.Require().NoError(err)

	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-limit",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "docker docker docker"},
			{Seq: 2, Type: "message", Role: "assistant", ContentText: "docker docker docker"},
			{Seq: 3, Type: "message", Role: "user", ContentText: "docker docker docker"},
		},
	})
	s.Require().NoError(err)

	resp, err := s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "docker",
		Limit: 2,
	})
	s.Require().NoError(err)
	s.Len(resp.Results, 2)
}

func (s *StoreSuite) TestSearchElementsReflectsElementReplacement() {
	_, err := s.store.UpsertSessions(s.ctx, &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "sess-fts-replace", Title: "FTS replace"},
		},
	})
	s.Require().NoError(err)

	// Insert with "alpha"
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-fts-replace",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "alpha beta"},
		},
	})
	s.Require().NoError(err)

	// Search "alpha"; should find it
	resp, err := s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "alpha",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Require().Len(resp.Results, 1)

	// Replace with "gamma delta"; "alpha" should be gone from FTS
	_, err = s.store.ReplaceSessionElements(s.ctx, &store.ReplaceSessionElementsRequest{
		SessionID: "codex:sess-fts-replace",
		Elements: []*model.Element{
			{Seq: 1, Type: "message", Role: "user", ContentText: "gamma delta"},
		},
	})
	s.Require().NoError(err)

	resp, err = s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "alpha",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Empty(resp.Results, "old content should be removed from FTS after replacement")

	// "gamma" should now be findable
	resp, err = s.store.SearchElements(s.ctx, &store.SearchElementsRequest{
		Query: "gamma",
		Limit: 10,
	})
	s.Require().NoError(err)
	s.Require().Len(resp.Results, 1)
}

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
	s.Equal("rendered", injections.Injections[0].Status)
}

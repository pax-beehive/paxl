package facade_test

import (
	"context"
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

func (s *CapsuleFacadeSuite) TestInjectDeliversHandoffAndStoresInjection() {
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
	s.seedTargetSession()
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
	})

	s.Require().NoError(err)
	s.Equal(created.Capsule.CapsuleID, injected.Injection.CapsuleID)
	s.Equal("codex:target", injected.Injection.TargetSessionID)
	s.Contains(injected.Message, "system_handoff")
	s.Contains(injected.Message, "Bridge content")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal(injected.Message, string(rawPrompt))

	listed, err := capsuleFacade.ListInjections(s.ctx, &facade.ListInjectionsRequest{
		TargetSessionID: "codex:target",
	})
	s.Require().NoError(err)
	s.Require().Len(listed.Injections, 1)
	s.Equal(injected.Injection.InjectionID, listed.Injections[0].InjectionID)
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

func (s *CapsuleFacadeSuite) TestInjectLoadsUncachedTargetSessionBeforeDelivery() {
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
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	injected, err := capsuleFacade.Inject(s.ctx, &facade.InjectCapsuleRequest{
		CapsuleID:       created.Capsule.CapsuleID,
		TargetSessionID: "codex:target",
	})

	s.Require().NoError(err)
	s.Equal("codex:target", injected.Injection.TargetSessionID)
	s.Equal("cli_resume", injected.Injection.DeliveryMethod)
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
	s.Contains(resp.Capsule.Title, "Session mirror")
	s.Contains(resp.Capsule.Summary, "without asking the source agent to summarize")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "This context was mirrored by paxl")
	s.Contains(string(rawPrompt), "Bridge content")
	s.Contains(string(rawPrompt), "Assistant context")
}

func (s *CapsuleFacadeSuite) TestMirrorSessionStartsNewTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
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
	s.Contains(string(rawPrompt), "Target agent: claude")
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

func (s *CapsuleFacadeSuite) installFakeCodexRoleCapsuleGenerator(rolloutPath string, role string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"" + role + "\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Generated bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Generated summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Generated bridge content\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
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
		"content=$(printf 'x%.0s' $(seq 1 7000))\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"assistant\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Large bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Generated summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"${content}\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

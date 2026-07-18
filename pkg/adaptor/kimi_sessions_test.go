package adaptor_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

func (s *RegistrySuite) TestKimiAdapterListsLocalSessionsThroughPublicInterface() {
	kimiHome := s.T().TempDir()
	sessionDir := filepath.Join(kimiHome, "sessions", "wd_project", "session_kimi")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("KIMI_CODE_HOME", kimiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(kimiHome, "session_index.jsonl"),
		[]byte(`{"sessionId":"session_kimi","workDir":"/tmp/project","sessionDir":"`+
			sessionDir+`"}`+"\n"),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "state.json"),
		[]byte(`{
			"createdAt":"2026-07-17T18:00:00Z",
			"updatedAt":"2026-07-17T19:00:00Z",
			"workDir":"/tmp/project",
			"title":"Continue Kimi work"
		}`),
		0o600,
	))

	resp, err := adaptor.NewKimiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("kimi:session_kimi", resp.Sessions[0].ID)
	s.Equal(model.AgentNameKimi, resp.Sessions[0].Agent)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
	s.Equal("Continue Kimi work", resp.Sessions[0].Title)
	s.Equal("2026-07-17T19:00:00Z", resp.Sessions[0].UpdatedAt)
}

func (s *RegistrySuite) TestKimiAdapterRendersVisibleConversationTimeline() {
	kimiHome := s.T().TempDir()
	sessionDir := filepath.Join(kimiHome, "sessions", "wd_project", "session_timeline")
	wireDir := filepath.Join(sessionDir, "agents", "main")
	s.Require().NoError(os.MkdirAll(wireDir, 0o700))
	s.T().Setenv("KIMI_CODE_HOME", kimiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(kimiHome, "session_index.jsonl"),
		[]byte(`{"sessionId":"session_timeline","workDir":"/tmp/project","sessionDir":"`+
			sessionDir+`"}`+"\n"),
		0o600,
	))
	startedAt := time.Date(2026, time.July, 17, 20, 0, 0, 0, time.UTC)
	wire := `{"type":"turn.prompt","time":` +
		fmt.Sprint(startedAt.UnixMilli()) +
		`,"origin":{"kind":"user"},"input":[{"type":"text","text":"Continue Kimi support."}]}` + "\n" +
		`{"type":"llm.request","time":` + fmt.Sprint(startedAt.Add(time.Second).UnixMilli()) +
		`,"model":"kimi-k3","turnStep":1}` + "\n" +
		`{"type":"context.append_loop_event","time":` +
		fmt.Sprint(startedAt.Add(2*time.Second).UnixMilli()) +
		`,"event":{"type":"content.part","turnId":"turn_1","step":1,"part":{"type":"think","think":"Private reasoning."}}}` + "\n" +
		`{"type":"context.append_loop_event","time":` +
		fmt.Sprint(startedAt.Add(3*time.Second).UnixMilli()) +
		`,"event":{"type":"content.part","turnId":"turn_1","step":1,"part":{"type":"text","text":"I will continue "}}}` + "\n" +
		`{"type":"context.append_loop_event","time":` +
		fmt.Sprint(startedAt.Add(4*time.Second).UnixMilli()) +
		`,"event":{"type":"content.part","turnId":"turn_1","step":1,"part":{"type":"text","text":"the work."}}}` + "\n" +
		`{"type":"context.append_loop_event","time":` +
		fmt.Sprint(startedAt.Add(5*time.Second).UnixMilli()) +
		`,"event":{"type":"step.end","turnId":"turn_1","step":1}}` + "\n"
	s.Require().NoError(os.WriteFile(filepath.Join(wireDir, "wire.jsonl"), []byte(wire), 0o600))

	resp, err := adaptor.NewKimiAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "session_timeline"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Continue Kimi support.", resp.Elements[0].ContentText)
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("I will continue the work.", resp.Elements[1].ContentText)
	s.Equal("kimi-k3", resp.Elements[1].Model)
	s.Equal(startedAt.Add(3*time.Second).Format(time.RFC3339Nano), resp.Elements[1].StartedAt)
	s.Equal(startedAt.Add(5*time.Second).Format(time.RFC3339Nano), resp.Elements[1].CompletedAt)
}

func (s *RegistrySuite) TestKimiAdapterDeliversPromptToExistingSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("kimi", capturePath, argsPath)

	resp, err := adaptor.NewKimiAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "session_existing", Text: "Continue this work."},
	)

	s.Require().NoError(err)
	s.Equal("cli_resume", resp.DeliveryMethod)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("--session\nsession_existing\n--prompt\nContinue this work.\n", string(rawArgs))
}

func (s *RegistrySuite) TestKimiAdapterStartsNewSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("kimi", capturePath, argsPath)

	resp, err := adaptor.NewKimiAdapter().StartSession(
		context.Background(),
		&adaptor.StartSessionRequest{Text: "Start with this context."},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("--prompt\nStart with this context.\n", string(rawArgs))
}

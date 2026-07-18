package adaptor_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	_ "modernc.org/sqlite"
)

func (s *RegistrySuite) TestOpenCodeAdapterListsLocalSessionsThroughPublicInterface() {
	dataHome := s.T().TempDir()
	opencodeHome := filepath.Join(dataHome, "opencode")
	s.Require().NoError(os.MkdirAll(opencodeHome, 0o700))
	s.T().Setenv("XDG_DATA_HOME", dataHome)

	db, err := sql.Open("sqlite", filepath.Join(opencodeHome, "opencode.db"))
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.Require().NoError(db.Close()) })
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TABLE session (
		id TEXT PRIMARY KEY,
		directory TEXT NOT NULL,
		title TEXT NOT NULL,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL
	)`)
	s.Require().NoError(err)
	updatedAt := time.Date(2026, time.July, 17, 18, 30, 0, 0, time.UTC)
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO session (id, directory, title, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?)`,
		"ses_opencode",
		"/tmp/project",
		"Continue OpenCode work",
		updatedAt.Add(-time.Hour).UnixMilli(),
		updatedAt.UnixMilli(),
	)
	s.Require().NoError(err)

	resp, err := adaptor.NewOpenCodeAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("opencode:ses_opencode", resp.Sessions[0].ID)
	s.Equal(model.AgentNameOpenCode, resp.Sessions[0].Agent)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
	s.Equal("Continue OpenCode work", resp.Sessions[0].Title)
	s.Equal(updatedAt.Format(time.RFC3339Nano), resp.Sessions[0].UpdatedAt)
}

func (s *RegistrySuite) TestOpenCodeAdapterRendersVisibleConversationTimeline() {
	dataHome := s.T().TempDir()
	opencodeHome := filepath.Join(dataHome, "opencode")
	s.Require().NoError(os.MkdirAll(opencodeHome, 0o700))
	s.T().Setenv("XDG_DATA_HOME", dataHome)

	db, err := sql.Open("sqlite", filepath.Join(opencodeHome, "opencode.db"))
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.Require().NoError(db.Close()) })
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TABLE message (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		data TEXT NOT NULL
	)`)
	s.Require().NoError(err)
	_, err = db.ExecContext(ctx, `CREATE TABLE part (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		data TEXT NOT NULL
	)`)
	s.Require().NoError(err)
	startedAt := time.Date(2026, time.July, 17, 19, 0, 0, 0, time.UTC)
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES
		 (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"msg_user",
		"ses_timeline",
		startedAt.UnixMilli(),
		startedAt.UnixMilli(),
		`{"role":"user","time":{"created":1784314800000},"model":{"modelID":"gpt-5"}}`,
		"msg_assistant",
		"ses_timeline",
		startedAt.Add(time.Second).UnixMilli(),
		startedAt.Add(2*time.Second).UnixMilli(),
		`{"role":"assistant","time":{"created":1784314801000,"completed":1784314802000},"modelID":"gpt-5"}`,
	)
	s.Require().NoError(err)
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES
		 (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)`,
		"part_user", "msg_user", "ses_timeline", startedAt.UnixMilli(), startedAt.UnixMilli(),
		`{"type":"text","text":"Please continue the adapter."}`,
		"part_reasoning", "msg_assistant", "ses_timeline", startedAt.Add(time.Second).UnixMilli(),
		startedAt.Add(time.Second).UnixMilli(), `{"type":"reasoning","text":"Private reasoning."}`,
		"part_assistant", "msg_assistant", "ses_timeline", startedAt.Add(2*time.Second).UnixMilli(),
		startedAt.Add(2*time.Second).UnixMilli(), `{"type":"text","text":"I will continue it."}`,
	)
	s.Require().NoError(err)

	resp, err := adaptor.NewOpenCodeAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "ses_timeline"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Please continue the adapter.", resp.Elements[0].ContentText)
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("I will continue it.", resp.Elements[1].ContentText)
	s.Equal("gpt-5", resp.Elements[1].Model)
	s.Equal(startedAt.Add(time.Second).Format(time.RFC3339Nano), resp.Elements[1].StartedAt)
	s.Equal(startedAt.Add(2*time.Second).Format(time.RFC3339Nano), resp.Elements[1].CompletedAt)
}

func (s *RegistrySuite) TestOpenCodeAdapterDeliversPromptToExistingSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("opencode", capturePath, argsPath)

	resp, err := adaptor.NewOpenCodeAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "ses_existing", Text: "Continue this work."},
	)

	s.Require().NoError(err)
	s.Equal("cli_resume", resp.DeliveryMethod)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("run\n--session\nses_existing\nContinue this work.\n", string(rawArgs))
}

func (s *RegistrySuite) TestOpenCodeAdapterStartsNewSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("opencode", capturePath, argsPath)

	resp, err := adaptor.NewOpenCodeAdapter().StartSession(
		context.Background(),
		&adaptor.StartSessionRequest{Text: "Start with this context."},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("run\nStart with this context.\n", string(rawArgs))
}

package adaptor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeCodexLineageFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	writeRollout := func(day, name string, lines []string, mtime time.Time) {
		t.Helper()
		dir := filepath.Join(home, "sessions", "2026", "07", day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		path := filepath.Join(dir, name)
		content := ""
		for _, line := range lines {
			content += line + "\n"
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", name, err)
		}
	}
	message := func(ts, text string) string {
		return `{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}`
	}

	writeRollout("18", "rollout-2026-07-18T01-00-00-aaa.jsonl", []string{
		`{"type":"session_meta","payload":{"id":"aaa","session_id":"aaa","timestamp":"2026-07-18T01:00:00Z","cwd":"/tmp/project","source":"vscode"}}`,
		message("2026-07-18T01:00:01Z", "old turn"),
	}, time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC))
	writeRollout("19", "rollout-2026-07-19T01-00-00-bbb.jsonl", []string{
		`{"type":"session_meta","payload":{"id":"bbb","session_id":"bbb","forked_from_id":"aaa","timestamp":"2026-07-19T01:00:00Z","cwd":"/tmp/project","source":"vscode"}}`,
		message("2026-07-19T01:00:01Z", "new turn after compact"),
	}, time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC))
	writeRollout("19", "rollout-2026-07-19T02-00-00-ddd.jsonl", []string{
		`{"type":"session_meta","payload":{"id":"ddd","session_id":"aaa","forked_from_id":"aaa","timestamp":"2026-07-19T02:00:00Z","cwd":"/tmp/project","source":{"subagent":{"other":"guardian"}},"thread_source":"subagent"}}`,
		message("2026-07-19T02:00:01Z", "guardian review"),
	}, time.Date(2026, 7, 19, 2, 0, 0, 0, time.UTC))

	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(
		`{"id":"aaa","thread_name":"original thread","updated_at":"2026-07-18T02:00:00Z"}`+"\n"+
			`{"id":"bbb","thread_name":"forked thread","updated_at":"2026-07-19T03:00:00Z"}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("WriteFile(index) error = %v", err)
	}
	return home
}

func TestCodexListsOneConversationAcrossCompaction(t *testing.T) {
	for _, rootIndexed := range []bool{true, false} {
		t.Run(
			map[bool]string{true: "root indexed", false: "root missing"}[rootIndexed],
			func(t *testing.T) {
				home := writeCodexLineageFixture(t)
				index := `{"id":"bbb","thread_name":"forked thread","updated_at":"2026-07-19T03:00:00Z"}` + "\n" + `{"id":"ddd","thread_name":"Guardian review","updated_at":"2026-07-19T04:00:00Z"}` + "\n"
				if rootIndexed {
					index += `{"id":"aaa","thread_name":"original thread","updated_at":"2026-07-18T02:00:00Z"}` + "\n"
				}
				require.NoError(
					t,
					os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600),
				)
				resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{})
				require.NoError(t, err)
				require.Len(t, resp.Sessions, 1)
				require.Equal(t, "codex:aaa", resp.Sessions[0].ID)
				require.Equal(t, "forked thread", resp.Sessions[0].Title)
				require.Equal(t, "2026-07-19T03:00:00Z", resp.Sessions[0].UpdatedAt)
				require.Equal(t, "/tmp/project", resp.Sessions[0].ProjectID)
			},
		)
	}
}

func TestCodexHistoryFollowsCompactionButDirectReviewReadsItsOwnHistory(t *testing.T) {
	writeCodexLineageFixture(t)
	for id, want := range map[string]string{"aaa": "new turn after compact", "bbb": "new turn after compact", "ddd": "guardian review"} {
		resp, err := getCodexSession(context.Background(), &GetSessionRequest{NativeID: id})
		require.NoError(t, err)
		require.Len(t, resp.Elements, 1)
		require.Equal(t, want, resp.Elements[0].ContentText)
		require.Equal(t, "codex:"+id, resp.Elements[0].SessionID)
	}
}

func TestCodexGivenCanceledContextWhenReadingHistoryThenReturnsCancellation(t *testing.T) {
	writeCodexLineageFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewCodexAdapter().GetSession(ctx, &GetSessionRequest{NativeID: "aaa"})

	require.ErrorIs(t, err, context.Canceled)
}

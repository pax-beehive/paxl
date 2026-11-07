package adaptor

import (
	"context"
	"encoding/json"
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

func TestCodexPaginatedForkKeepsIndependentIdentity(t *testing.T) {
	home := writeCodexLineageFixture(t)
	path := filepath.Join(home, "sessions", "rollout-user-fork.jsonl")
	meta := `{"type":"session_meta","payload":{"id":"fork","session_id":"fork","forked_from_id":"aaa","forked_from_ordinal_exclusive":2,"thread_source":"user","history_mode":"paginated","history_base":{"thread_id":"aaa","end_ordinal_exclusive":2,"end_byte_offset":0}}}`
	require.NoError(t, os.WriteFile(path, []byte(meta+"\n"), 0o600))
	resp, err := listCodexSessions(t.Context(), &ListSessionsRequest{})
	require.NoError(t, err)
	ids := make([]string, 0, len(resp.Sessions))
	for _, session := range resp.Sessions {
		ids = append(ids, session.NativeID)
	}
	require.ElementsMatch(t, []string{"aaa", "fork"}, ids)
}

func TestCodexPaginatedForkReadsOnlyInheritedPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "sessions")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	message := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
	}
	prefix := `{"type":"session_meta","payload":{"id":"parent"}}` + "\n" + message("inherited")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-parent.jsonl"), []byte(prefix+message("parent after fork")), 0o600))
	meta := map[string]any{"type": "session_meta", "payload": map[string]any{
		"id": "fork", "session_id": "fork", "forked_from_id": "parent", "forked_from_ordinal_exclusive": 2,
		"thread_source": "user", "history_mode": "paginated", "history_base": map[string]any{
			"thread_id": "parent", "end_ordinal_exclusive": 2, "end_byte_offset": len(prefix),
		},
	}}
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-fork.jsonl"), append(append(raw, '\n'), []byte(message("fork turn"))...), 0o600))
	resp, err := getCodexSession(t.Context(), &GetSessionRequest{NativeID: "fork"})
	require.NoError(t, err)
	require.Len(t, resp.Elements, 2)
	require.Equal(t, "inherited", resp.Elements[0].ContentText)
	require.Equal(t, "fork turn", resp.Elements[1].ContentText)
	for i, e := range resp.Elements {
		require.Equal(t, "codex:fork", e.SessionID)
		require.Equal(t, int64(i+1), e.Seq)
	}
	parent, err := getCodexSession(t.Context(), &GetSessionRequest{NativeID: "parent"})
	require.NoError(t, err)
	require.Len(t, parent.Elements, 2)
	require.Equal(t, "parent after fork", parent.Elements[1].ContentText)
}

func TestCodexPaginatedHistoryRejectsInvalidReferences(t *testing.T) {
	for _, tc := range []struct {
		name, thread string
		offset       int64
		want         string
	}{
		{"missing parent", "missing", 0, "not found"},
		{"cycle", "fork", 0, "cyclic"},
		{"negative boundary", "parent", -1, "outside referenced rollout"},
		{"truncated parent", "parent", 1000000, "outside referenced rollout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			dir := filepath.Join(home, "sessions")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-parent.jsonl"), []byte(`{"type":"session_meta","payload":{"id":"parent"}}`+"\n"), 0o600))
			meta := map[string]any{"type": "session_meta", "payload": map[string]any{
				"id": "fork", "session_id": "fork", "forked_from_id": tc.thread, "forked_from_ordinal_exclusive": 1,
				"history_mode": "paginated", "history_base": map[string]any{"thread_id": tc.thread, "end_byte_offset": tc.offset},
			}}
			raw, err := json.Marshal(meta)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-fork.jsonl"), append(raw, '\n'), 0o600))
			_, err = getCodexSession(t.Context(), &GetSessionRequest{NativeID: "fork"})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCodexPaginatedHistoryReadsNestedArchivedPrefixes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	archived := filepath.Join(home, "archived_sessions")
	require.NoError(t, os.MkdirAll(sessions, 0o700))
	require.NoError(t, os.MkdirAll(archived, 0o700))
	message := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
	}
	write := func(dir, id, parent string, offset int, turn string) string {
		t.Helper()
		payload := map[string]any{"id": id, "session_id": id}
		if parent != "" {
			payload["forked_from_id"] = parent
			payload["forked_from_ordinal_exclusive"] = 2
			payload["history_mode"] = "paginated"
			payload["history_base"] = map[string]any{"thread_id": parent, "end_ordinal_exclusive": 2, "end_byte_offset": offset}
		}
		raw, err := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
		require.NoError(t, err)
		prefix := string(raw) + "\n" + message(turn)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-"+id+".jsonl"), []byte(prefix+message("excluded "+id)), 0o600))
		return prefix
	}
	root := write(archived, "root", "", 0, "root turn")
	child := write(archived, "child", "root", len(root), "child turn")
	write(sessions, "leaf", "child", len(child), "leaf turn")
	resp, err := getCodexSession(t.Context(), &GetSessionRequest{NativeID: "leaf"})
	require.NoError(t, err)
	var texts []string
	for _, e := range resp.Elements {
		texts = append(texts, e.ContentText)
	}
	require.Equal(t, []string{"root turn", "child turn", "leaf turn", "excluded leaf"}, texts)
}

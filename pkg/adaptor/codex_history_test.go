package adaptor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexHistoryRetainsToolsReasoningUsageAndDurations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "sessions"), 0o700))
	lines := `{"type":"session_meta","payload":{"id":"main"}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"abc"}}
{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"c1"}}
{"type":"response_item","payload":{"type":"function_call_output","output":"ok","call_id":"c1"}}
{"type":"event_msg","payload":{"type":"exec_command_end","call_id":"c1","duration":{"secs":1,"nanos":250000000}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":4}}}}
`
	require.NoError(
		t,
		os.WriteFile(filepath.Join(home, "sessions", "rollout-main.jsonl"), []byte(lines), 0o600),
	)
	resp, err := getCodexSession(t.Context(), &GetSessionRequest{NativeID: "main"})
	require.NoError(t, err)
	require.Len(t, resp.Elements, 4)
	require.Equal(t, "thinking", resp.Elements[0].Type)
	require.Equal(t, "exec_command", resp.Elements[1].NormalizedRaw["name"])
	require.Equal(t, map[string]any{"cmd": "pwd"}, resp.Elements[1].NormalizedRaw["arguments"])
	require.Equal(t, "ok", resp.Elements[2].NormalizedRaw["output"])
	require.Equal(t, int64(1250), resp.Elements[2].DurationMS)
	require.JSONEq(t, `{"total_token_usage":{"total_tokens":4}}`, resp.Elements[3].UsageJSON)
}

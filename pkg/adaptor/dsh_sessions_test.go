package adaptor_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func TestDSHListsDurableTitleWithoutCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	t.Setenv("PAXL_DSH_SESSIONS_DIR", "")
	dir := filepath.Join(home, "sessions", "--work--", "native-1")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	log := `{"type":"session","version":2,"id":"native-1","createdAt":1000,"cwd":"/work","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":0,"time":2000,"surfaceOp":"append","data":{"role":"user","content":[{"type":"text","text":"First question"}],"source":{"kind":"user"}}}
{"type":"session/title","seq":1,"time":3000,"data":{"title":"Named DSH conversation","source":{"kind":"user"},"messageSeqs":[]}}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.v2.jsonl"), []byte(log), 0o600))
	lookup, err := adaptor.NewDefaultRegistry().
		Lookup(t.Context(), &adaptor.LookupRequest{Name: model.AgentName("dsh")})
	require.NoError(t, err)
	resp, err := lookup.Adapter.ListSessions(t.Context(), &adaptor.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	require.Equal(t, "dsh:native-1", resp.Sessions[0].ID)
	require.Equal(t, "Named DSH conversation", resp.Sessions[0].Title)
	require.Equal(t, "First question", resp.Sessions[0].Preview)
	require.JSONEq(t, `["/work"]`, resp.Sessions[0].WorkspaceRootsJSON)
}

func dshTestAdapter(t *testing.T) adaptor.Adapter {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("PAXL_DSH_SESSIONS_DIR", "")
	lookup, err := adaptor.NewDefaultRegistry().
		Lookup(t.Context(), &adaptor.LookupRequest{Name: model.AgentName("dsh")})
	require.NoError(t, err)
	return lookup.Adapter
}

func TestDSHTracksActivityWithinTheSameSecond(t *testing.T) {
	a := dshTestAdapter(t)
	writeDSHLog(
		t,
		"activity",
		"session.v2.jsonl",
		`{"type":"session/title","seq":0,"time":1001,"data":{"title":"Updated"}}`+"\n",
	)
	listed, err := a.ListSessions(t.Context(), &adaptor.ListSessionsRequest{})
	require.NoError(t, err)
	require.Equal(t, "1970-01-01T00:00:01.001Z", listed.Sessions[0].UpdatedAt)
}

func writeDSHLog(t *testing.T, id, name, events string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("DSH_HOME"), "sessions", "--work--", id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	header := fmt.Sprintf(
		`{"type":"session","version":2,"id":%q,"createdAt":1000,"cwd":"/work","isSeeded":false,"delegationDepth":0}`+"\n",
		id,
	)
	if strings.HasPrefix(name, "session.jsonl") {
		header = strings.Replace(header, `"version":2`, `"version":0`, 1)
	}
	if strings.HasPrefix(name, "session.v1.") {
		header = strings.Replace(header, `"version":2`, `"version":1`, 1)
	}
	raw := []byte(header + events)
	if filepath.Ext(name) == ".zstd" {
		encoder, err := zstd.NewWriter(nil)
		require.NoError(t, err)
		raw = encoder.EncodeAll([]byte(header), nil)
		raw = append(raw, encoder.EncodeAll([]byte(events), nil)...)
		require.NoError(t, encoder.Close())
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func TestDSHReadsCompressedHistoryWithoutReplacementDuplicates(t *testing.T) {
	a := dshTestAdapter(t)
	events := `{"type":"user/message","seq":0,"time":2000,"surfaceOp":"append","data":{"role":"user","content":[{"type":"text","text":"Real prompt"}],"source":{"kind":"user"}}}
{"type":"assistant/message","seq":1,"time":3000,"surfaceOp":"append","data":{"message":{"role":"assistant","content":[{"type":"text","text":"Answer"}],"source":{"kind":"model","model":"deepseek-v4-flash"}},"usage":{"inputTokens":10,"outputTokens":5}}}
{"type":"user/message","seq":2,"time":4000,"surfaceOp":{"op":"replace","from":0,"to":1},"data":{"role":"user","content":[{"type":"text","text":"Compacted copy"}],"source":{"kind":"inject"}}}
{"type":"session/title","seq":3,"time":5000,"data":{"title":"Renamed conversation","source":{"kind":"user"}}}
{"type":"tool/call","seq":4,"time":6000,"data":{"callId":"call-1","name":"shell","arguments":"{}"}}
{"type":"tool/result","seq":5,"time":7000,"surfaceOp":"append","data":{"message":{"role":"user","content":[{"type":"tool-result","toolCallId":"call-1","content":[{"type":"text","text":"OK"}]}],"source":{"kind":"tool","callId":"call-1"}}}}
`
	writeDSHLog(t, "compressed", "session.v2.jsonl.zstd", events)
	listed, err := a.ListSessions(t.Context(), &adaptor.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Sessions, 1)
	require.Equal(t, "Renamed conversation", listed.Sessions[0].Title)
	history, err := a.GetSession(t.Context(), &adaptor.GetSessionRequest{NativeID: "compressed"})
	require.NoError(t, err)
	require.Len(t, history.Elements, 4)
	require.Equal(t, "Real prompt", history.Elements[0].ContentText)
	require.Equal(t, "Answer", history.Elements[1].ContentText)
	require.Equal(t, "deepseek-v4-flash", history.Elements[1].Model)
	require.JSONEq(t, `{"inputTokens":10,"outputTokens":5}`, history.Elements[1].UsageJSON)
	require.Equal(t, "tool_call", history.Elements[2].Type)
	require.Equal(t, "OK", history.Elements[3].ContentText)
}

func TestDSHSelectsNewestGenerationAndRefusesUnknownFormats(t *testing.T) {
	a := dshTestAdapter(t)
	current := writeDSHLog(
		t,
		"versions",
		"session.v2.jsonl",
		`{"type":"session/title","seq":0,"time":2000,"data":{"title":"Current title"}}`+"\n",
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(filepath.Dir(current), "session.jsonl"),
			[]byte("old file must not be read"),
			0o600,
		),
	)
	list, err := a.ListSessions(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, list.Sessions, 1)
	require.Equal(t, "Current title", list.Sessions[0].Title)
	raw, err := os.ReadFile(current)
	require.NoError(t, err)
	raw = []byte(strings.Replace(string(raw), `"version":2`, `"version":3`, 1))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(filepath.Dir(current), "session.v3.jsonl"), raw, 0o600),
	)
	_, err = a.ListSessions(t.Context(), nil)
	require.ErrorContains(t, err, "format version 3")
}

func TestDSHRetainsCompletePrefixAndDoesNotRepairFiles(t *testing.T) {
	for _, suffix := range []string{".jsonl", ".jsonl.zstd"} {
		t.Run(suffix, func(t *testing.T) {
			a := dshTestAdapter(t)
			path := writeDSHLog(
				t,
				"partial",
				"session.v2"+suffix,
				`{"type":"session/title","seq":0,"time":2000,"data":{"title":"Durable title"}}`+"\n"+`{"type":"user/message"`,
			)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			list, err := a.ListSessions(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, "Durable title", list.Sessions[0].Title)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestDSHRejectsMalformedCommittedRecords(t *testing.T) {
	a := dshTestAdapter(t)
	writeDSHLog(t, "bad", "session.v2.jsonl", "not-json\n")
	_, err := a.ListSessions(t.Context(), nil)
	require.ErrorContains(t, err, "invalid DSH event")
}

func TestDSHFiltersSubagentsSupportsOverridesAndCancellation(t *testing.T) {
	a := dshTestAdapter(t)
	parent := writeDSHLog(t, "parent", "session.v2.jsonl", "")
	child := writeDSHLog(t, "child", "session.v2.jsonl", "")
	raw, err := os.ReadFile(child)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			child,
			[]byte(
				strings.Replace(
					string(raw),
					`"delegationDepth":0`,
					`"delegationDepth":1,"origin":"subagent"`,
					1,
				),
			),
			0o600,
		),
	)
	list, err := a.ListSessions(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, list.Sessions, 1)
	require.NotEmpty(t, list.Sessions[0].Title)
	list, err = a.ListSessions(t.Context(), &adaptor.ListSessionsRequest{IncludeSubagents: true})
	require.NoError(t, err)
	require.Len(t, list.Sessions, 2)
	t.Setenv("PAXL_DSH_SESSIONS_DIR", filepath.Dir(parent))
	list, err = a.ListSessions(t.Context(), &adaptor.ListSessionsRequest{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "parent", list.Sessions[0].NativeID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = a.ListSessions(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDSHEmptyHomeAndUnsupportedWrites(t *testing.T) {
	a := dshTestAdapter(t)
	list, err := a.ListSessions(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, list.Sessions)
	_, err = a.GetSession(t.Context(), &adaptor.GetSessionRequest{NativeID: "../../secret"})
	require.ErrorContains(t, err, "not found")
	_, err = a.Prompt(t.Context(), &adaptor.PromptRequest{NativeID: "id", Text: "must not run"})
	require.ErrorContains(t, err, "does not support prompt delivery")
}

func TestDSHReadsReleasedFormatsWithoutDuplicatingPackedChunks(t *testing.T) {
	for _, name := range []string{"session.jsonl.zstd", "session.v1.jsonl.zstd"} {
		t.Run(name, func(t *testing.T) {
			a := dshTestAdapter(t)
			writeDSHLog(
				t,
				"legacy",
				name,
				`{"type":"text-chunks","seq0":0,"time0":2000,"data":{"turn":0,"step":0,"index":0,"texts":["Hello ","world"],"dt":[10]}}
{"type":"assistant/message","seq":2,"time":3000,"surfaceOp":"append","data":{"message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}],"source":{"kind":"model","model":"test"}}}}
{"type":"session/title","seq":3,"time":4000,"data":{"title":"Legacy title"}}
`,
			)
			list, err := a.ListSessions(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, "Legacy title", list.Sessions[0].Title)
			history, err := a.GetSession(
				t.Context(),
				&adaptor.GetSessionRequest{NativeID: "legacy"},
			)
			require.NoError(t, err)
			require.Len(t, history.Elements, 1)
			require.Equal(t, "Hello world", history.Elements[0].ContentText)
		})
	}
}

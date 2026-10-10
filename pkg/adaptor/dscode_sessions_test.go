package adaptor_test

import (
	"bytes"
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

func TestDSCodeListsOwnSessionsAndReadsHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSCODE_HOME", home)
	t.Setenv("PAXL_DSCODE_SESSIONS_DIR", "")
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("PAXL_DSH_SESSIONS_DIR", t.TempDir())
	dir := filepath.Join(home, "sessions", "work", "native-1")
	require.NoError(t, os.MkdirAll(dir, 0700))
	log := `{"type":"session","version":2,"id":"native-1","createdAt":1000,"cwd":"/work","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":0,"time":2000,"surfaceOp":"append","data":{"role":"user","content":[{"type":"text","text":"Question"}],"source":{"kind":"user"}}}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.v2.jsonl"), []byte(log), 0600))
	name, err := model.ParseAgentName("dscode")
	require.NoError(t, err)
	lookup, err := adaptor.NewDefaultRegistry().
		Lookup(t.Context(), &adaptor.LookupRequest{Name: name})
	require.NoError(t, err)
	sessions, err := lookup.Adapter.ListSessions(t.Context(), &adaptor.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, sessions.Sessions, 1)
	require.Equal(t, "dscode:native-1", sessions.Sessions[0].ID)
	require.Equal(t, name, sessions.Sessions[0].Agent)
	history, err := lookup.Adapter.GetSession(
		t.Context(),
		&adaptor.GetSessionRequest{NativeID: "native-1"},
	)
	require.NoError(t, err)
	require.Len(t, history.Elements, 1)
}

func TestDSCodeDeliversToLiveSessionAndResumesNativeCLI(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	t.Setenv("DSCODE_TEST_ARGS", args)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, "dscode"),
			[]byte(
				"#!/bin/sh\nprintf '%s\n' \"$@\" > \"$DSCODE_TEST_ARGS\"\nprintf 'acknowledged\n'\n",
			),
			0700,
		),
	)
	lookup, err := adaptor.NewDefaultRegistry().
		Lookup(t.Context(), &adaptor.LookupRequest{Name: model.AgentName("dscode")})
	require.NoError(t, err)
	_, err = lookup.Adapter.Prompt(
		t.Context(),
		&adaptor.PromptRequest{NativeID: "native-1", Text: "--steer shell $HOME"},
	)
	require.NoError(t, err)
	raw, err := os.ReadFile(args)
	require.NoError(t, err)
	require.Equal(t, "send\nnative-1\n--\n--steer shell $HOME\n", string(raw))
	var stdout, stderr bytes.Buffer
	_, err = lookup.Adapter.(adaptor.SessionResumer).Resume(
		t.Context(),
		&adaptor.ResumeSessionRequest{NativeID: "native-1"},
		adaptor.WithStreams(strings.NewReader(""), &stdout, &stderr),
	)
	require.NoError(t, err)
	raw, err = os.ReadFile(args)
	require.NoError(t, err)
	require.Equal(t, "resume\nnative-1\n", string(raw))
	_, err = lookup.Adapter.Prompt(
		t.Context(),
		&adaptor.PromptRequest{NativeID: "--bad", Text: "hi"},
	)
	require.Error(t, err)
}

func TestDSCodeReadsCurrentCompressedHarnessFormats(t *testing.T) {
	for _, version := range []int{3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PAXL_DSCODE_SESSIONS_DIR", root)
			dir := filepath.Join(root, "work", "current")
			require.NoError(t, os.MkdirAll(dir, 0700))
			header := fmt.Sprintf(
				`{"type":"session","version":%d,"id":"current","createdAt":1000,"cwd":"/work","isSeeded":false,"delegationDepth":0,"agentPreset":"dscode"}`+"\n",
				version,
			)
			events := `{"type":"user/message","seq":0,"time":2000,"surfaceOp":"append","data":{"role":"user","content":[{"type":"text","text":"Current prompt"}],"source":{"kind":"user"}}}
{"type":"tool/result","seq":1,"time":3000,"surfaceOp":"append","data":{"message":{"role":"tool","toolCallId":"call","content":[{"type":"text","text":"Tool output"}],"source":{"kind":"tool","callId":"call"}}}}
`
			if version == 3 {
				events = strings.Replace(
					events,
					`"content":[{"type":"text","text":"Tool output"}]`,
					`"content":[{"type":"tool-result","toolCallId":"call","content":[{"type":"text","text":"Tool output"}]}]`,
					1,
				)
			}
			encoder, err := zstd.NewWriter(nil)
			require.NoError(t, err)
			raw := encoder.EncodeAll([]byte(header), nil)
			raw = append(raw, encoder.EncodeAll([]byte(events), nil)...)
			require.NoError(t, encoder.Close())
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(dir, fmt.Sprintf("session.v%d.jsonl.zstd", version)),
					raw,
					0600,
				),
			)
			lookup, err := adaptor.NewDefaultRegistry().
				Lookup(t.Context(), &adaptor.LookupRequest{Name: model.AgentName("dscode")})
			require.NoError(t, err)
			listed, err := lookup.Adapter.ListSessions(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, "Current prompt", listed.Sessions[0].Preview)
			history, err := lookup.Adapter.GetSession(
				t.Context(),
				&adaptor.GetSessionRequest{NativeID: "current"},
			)
			require.NoError(t, err)
			require.Len(t, history.Elements, 2)
			require.Equal(t, "Tool output", history.Elements[1].ContentText)
			require.Equal(t, "dscode:current", history.Elements[1].SessionID)
		})
	}
}

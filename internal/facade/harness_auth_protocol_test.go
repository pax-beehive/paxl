//go:build !windows

package facade_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestAuthProtocolKeepsNativeLoginLocalAndEmitsVersionedEvents(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(
			launcher,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login --device-auth' ] || exit 23\necho 'Open https://example.invalid/login and enter ABCD' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", launcher)
	var events bytes.Buffer
	err := facade.NewHarnessAuthFacade().
		ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogin}, facade.WithStreams(strings.NewReader(""), &events, &bytes.Buffer{}))
	require.NoError(t, err)
	require.Contains(t, events.String(), `"version":1`)
	require.Contains(t, events.String(), `"type":"message"`)
	require.Contains(t, events.String(), `"type":"result"`)
	require.Contains(t, events.String(), `"state":"succeeded"`)
}

func TestAuthProtocolSubmitsOnlyTheCurrentSecretPromptAndNeverReflectsInput(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "claude")
	record := filepath.Join(root, "native-code")
	t.Setenv("AUTH_PROTOCOL_RECORD", record)
	require.NoError(
		t,
		os.WriteFile(
			launcher,
			[]byte(
				"#!/bin/sh\nIFS= read -r code\nprintf '%s' \"$code\" > \"$AUTH_PROTOCOL_RECORD\"\nprintf '%s' \"$code\" >&2\nprintf '\\n' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("CLAUDE_CODE_EXECUTABLE", launcher)
	input := `{"version":1,"type":"input","id":"native","value":"private-one-time-code"}` + "\n"
	var events bytes.Buffer
	err := facade.NewHarnessAuthFacade().
		ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameClaude, Operation: model.HarnessAuthLogin}, facade.WithStreams(strings.NewReader(input), &events, &bytes.Buffer{}))
	require.NoError(t, err)
	require.NotContains(t, events.String(), "private-one-time-code")
	require.Contains(t, events.String(), "[REDACTED]")
	require.Contains(t, events.String(), `"state":"succeeded"`)
	code, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, "private-one-time-code", string(code))
}
func TestAuthProtocolInitialAPIKeyPromptFeedsTheExistingLocalSecretRoute(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(
			launcher,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login --with-api-key' ] || exit 23\nIFS= read -r key\n[ \"$key\" = 'fixture-api-key' ] || exit 24\necho \"$key\" >&2\n",
			),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", launcher)
	var events bytes.Buffer
	input := `{"version":1,"type":"input","id":"initial-secret","value":"fixture-api-key"}` + "\n"
	err := facade.NewHarnessAuthFacade().
		ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogin, Method: model.HarnessAuthAPIKey, SecretStdin: true}, facade.WithStreams(strings.NewReader(input), &events, &bytes.Buffer{}))
	require.NoError(t, err)
	require.Contains(t, events.String(), `"id":"initial-secret"`)
	require.Contains(t, events.String(), `"state":"succeeded"`)
	require.NotContains(t, events.String(), "fixture-api-key")
}
func TestAuthProtocolRejectsStaleMalformedAndMultilineInput(t *testing.T) {
	for _, input := range []string{`{"version":1,"type":"input","id":"old","value":"private"}`, `{"version":2,"type":"input","id":"native","value":"private"}`, `{"version":1,"type":"input","id":"native","value":"one\ntwo"}`, `not-json`} {
		t.Run(input, func(t *testing.T) {
			launcher := filepath.Join(t.TempDir(), "claude")
			require.NoError(
				t,
				os.WriteFile(launcher, []byte("#!/bin/sh\nIFS= read -r code\n"), 0700),
			)
			t.Setenv("CLAUDE_CODE_EXECUTABLE", launcher)
			var events bytes.Buffer
			err := facade.NewHarnessAuthFacade().
				ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameClaude, Operation: model.HarnessAuthLogin}, facade.WithStreams(strings.NewReader(input+"\n"), &events, &bytes.Buffer{}))
			require.Error(t, err)
			require.NotContains(t, events.String(), `"state":"succeeded"`)
		})
	}
}
func TestAuthProtocolReportsNativeFailureAsFinalFailureAndRejectsTTYOnlyFlow(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "codex")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 23\n"), 0700))
	t.Setenv("CODEX_PATH", launcher)
	var events bytes.Buffer
	err := facade.NewHarnessAuthFacade().
		ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogin}, facade.WithStreams(strings.NewReader(""), &events, &bytes.Buffer{}))
	require.Error(t, err)
	require.Contains(t, events.String(), `"state":"failed"`)
	require.Contains(t, events.String(), `"harness":"codex"`)
	err = facade.NewHarnessAuthFacade().
		ExecuteProtocol(t.Context(), &facade.HarnessAuthRequest{Harness: model.AgentNameAntigravity, Operation: model.HarnessAuthLogin}, facade.WithStreams(strings.NewReader(""), &events, &bytes.Buffer{}))
	require.ErrorContains(t, err, "PTY")
}

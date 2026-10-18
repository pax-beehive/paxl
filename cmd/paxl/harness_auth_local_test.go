//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthDefaultLoginRunsLocallyWithoutPaxd(t *testing.T) {
	command := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(
			command,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login --device-auth' ] || exit 12\necho 'https://example.test/activate ABCD-EFGH' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", command)
	var output, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{"auth", "login", "--harness", "codex", "--format", "json"},
		strings.NewReader(""),
		&output,
		&diagnostics,
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"harness":"codex","state":"succeeded"}`, output.String())
	require.Contains(t, diagnostics.String(), "ABCD-EFGH")
}

func TestHarnessAuthAliasPreservesLocalCodeInputAndJSONOutput(t *testing.T) {
	command := filepath.Join(t.TempDir(), "claude")
	require.NoError(
		t,
		os.WriteFile(
			command,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'auth login --console' ] || exit 12\nread -r code\n[ \"$code\" = 'authorization-code' ] || exit 13\necho 'Authorize in your browser' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("CLAUDE_CODE_EXECUTABLE", command)
	var output, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{
			"harness",
			"auth",
			"login",
			"--harness",
			"claude",
			"--method",
			"console",
			"--format",
			"json",
		},
		strings.NewReader("authorization-code\n"),
		&output,
		&diagnostics,
	)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"harness":"claude","state":"succeeded","method":"console"}`,
		output.String(),
	)
	require.Contains(t, diagnostics.String(), "Authorize in your browser")
	require.NotContains(t, output.String()+diagnostics.String(), "authorization-code")
}

func TestLocalKimiLoginUsesNativeDeviceFlow(t *testing.T) {
	dir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, "kimi"),
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login' ] || exit 12\necho 'https://example.test/activate KIMI-CODE' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{"harness", "auth", "login", "--harness", "kimi", "--format", "json"},
		strings.NewReader(""),
		&output,
		&diagnostics,
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"harness":"kimi","state":"succeeded"}`, output.String())
	require.Contains(t, diagnostics.String(), "KIMI-CODE")
}

func TestLocalSecretLoginDoesNotExposeNativeOutput(t *testing.T) {
	for _, method := range []string{"api-key", "access-token"} {
		t.Run(method, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), "codex")
			require.NoError(
				t,
				os.WriteFile(
					command,
					[]byte(
						"#!/bin/sh\n[ \"$*\" = 'login --with-"+method+"' ] || exit 12\nread -r secret\n[ \"$secret\" = 'private-input' ] || exit 13\necho \"$secret\" >&2\n",
					),
					0700,
				),
			)
			t.Setenv("CODEX_PATH", command)
			var output, diagnostics bytes.Buffer
			err := runWithInput(
				t.Context(),
				[]string{
					"auth",
					"login",
					"--harness",
					"codex",
					"--method",
					method,
					"--secret-stdin",
					"--format",
					"json",
				},
				strings.NewReader("private-input\n"),
				&output,
				&diagnostics,
			)
			require.NoError(t, err)
			require.JSONEq(
				t,
				`{"harness":"codex","state":"succeeded","method":"`+method+`"}`,
				output.String(),
			)
			require.NotContains(t, output.String()+diagnostics.String(), "private-input")
		})
	}
}

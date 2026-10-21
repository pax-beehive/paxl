//go:build !windows

package adaptor_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func TestKimiAuthServiceProcess(t *testing.T) {
	if os.Getenv("PAXL_KIMI_HELPER") != "true" {
		return
	}
	for _, arg := range []string{"web", "--host", "127.0.0.1", "--port", "0", "--no-open"} {
		if !strings.Contains(strings.Join(os.Args, " "), arg) {
			os.Exit(13)
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(14)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		os.Exit(15)
	}
	const token = "native-fixture-private-bearer-token"
	// Fragmented writes must not cause the adapter to use a truncated token.
	_, _ = fmt.Fprintf(
		os.Stdout,
		"http://127.0.0.1:%d/#token=%s",
		address.Port,
		token[:18],
	)
	time.Sleep(10 * time.Millisecond)
	_, _ = fmt.Fprintln(os.Stdout, token[18:])
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/v1/auth" && r.Method == http.MethodGet {
			if response := os.Getenv("PAXL_KIMI_AUTH_RESPONSE"); response != "" {
				_, _ = fmt.Fprint(w, response)
				return
			}
			_, _ = fmt.Fprint(
				w,
				`{"code":0,"data":{"providers_count":1,"managed_provider":{"status":"authenticated"}}}`,
			)
			return
		}
		if r.URL.Path == "/api/v1/oauth/logout" && r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"logged_out":true}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	_ = server.Serve(listener)
	os.Exit(0)
}
func TestKimiStatusRejectsUnrecognizedNativeMetadata(t *testing.T) {
	for _, data := range []string{
		`{}`,
		`{"providers_count":null}`,
		`{"providers_count":-1}`,
		`{"providers_count":1,"managed_provider":{"status":"unrecognized-private-status"}}`,
	} {
		t.Run(data, func(t *testing.T) {
			kimiAuthFixture(t)
			t.Setenv("PAXL_KIMI_AUTH_RESPONSE", `{"code":0,"data":`+data+`}`)
			_, err := adaptor.NewNativeHarnessAuth().
				RunKimi(t.Context(), &adaptor.NativeAuthRequest{
					Harness: model.AgentNameKimi, Operation: model.HarnessAuthStatus,
				})
			require.ErrorContains(t, err, "unrecognized native Kimi authentication status")
			require.NotContains(t, err.Error(), "private-status")
		})
	}
}

func TestKimiStatusDoesNotTreatExpiredAccountMetadataAsVerifiedLogin(t *testing.T) {
	for _, tc := range []struct {
		data  string
		state model.HarnessAuthState
	}{
		{`{"providers_count":0,"managed_provider":null}`, model.HarnessAuthStateUnknown},
		{`{"providers_count":1,"managed_provider":null}`, model.HarnessAuthConfigured},
		{`{"providers_count":0,"managed_provider":{"status":"authenticated"}}`, model.HarnessAuthConfigured},
		{`{"providers_count":0,"managed_provider":{"status":"expired"}}`, model.HarnessAuthStateUnknown},
		{`{"providers_count":0,"managed_provider":{"status":"revoked"}}`, model.HarnessAuthStateUnknown},
		{`{"providers_count":0,"managed_provider":{"status":"unauthenticated"}}`, model.HarnessAuthStateUnknown},
	} {
		t.Run(tc.data, func(t *testing.T) {
			kimiAuthFixture(t)
			t.Setenv("PAXL_KIMI_AUTH_RESPONSE", `{"code":0,"data":`+tc.data+`}`)
			result, err := adaptor.NewNativeHarnessAuth().
				RunKimi(t.Context(), &adaptor.NativeAuthRequest{
					Harness: model.AgentNameKimi, Operation: model.HarnessAuthStatus,
				})
			require.NoError(t, err)
			require.Equal(t, tc.state, result.State)
			require.Nil(t, result.LoggedIn)
		})
	}
}
func kimiAuthFixture(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	binary, err := os.Executable()
	require.NoError(t, err)
	script := filepath.Join(root, "kimi")
	require.NoError(
		t,
		os.WriteFile(
			script,
			[]byte(
				"#!/bin/sh\nexec \"$PAXL_TEST_BINARY\" -test.run=TestKimiAuthServiceProcess -- \"$@\"\n",
			),
			0700,
		),
	)
	t.Setenv("KIMI_EXECUTABLE", script)
	t.Setenv("PAXL_KIMI_HELPER", "true")
	t.Setenv("PAXL_TEST_BINARY", binary)
	t.Setenv("HOME", root)
}
func TestKimiAuthOwnsLoopbackServiceAndKeepsBearerPrivate(t *testing.T) {
	kimiAuthFixture(t)
	auth := adaptor.NewNativeHarnessAuth()
	var output bytes.Buffer
	for _, op := range []model.HarnessAuthOperation{model.HarnessAuthStatus, model.HarnessAuthLogout, model.HarnessAuthMethods} {
		result, err := auth.RunKimi(
			t.Context(),
			&adaptor.NativeAuthRequest{
				Harness:   model.AgentNameKimi,
				Operation: op,
				Stdout:    &output,
				Stderr:    &output,
			},
		)
		require.NoError(t, err)
		require.NotContains(t, result.Message, "native-fixture-private")
		require.Empty(t, output.String())
		// Only the three metadata/account operations are exercised here.
		//exhaustive:ignore
		switch op {
		case model.HarnessAuthStatus:
			require.Equal(t, model.HarnessAuthConfigured, result.State)
		case model.HarnessAuthLogout:
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			require.Contains(t, result.Message, "Remote Control")
		case model.HarnessAuthMethods:
			require.Equal(t, model.HarnessAuthAvailable, result.State)
		}
	}
}
func TestKimiAuthRejectsUnavailableAndCancelledServices(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "kimi")
	t.Setenv("KIMI_EXECUTABLE", launcher)
	auth := adaptor.NewNativeHarnessAuth()
	req := &adaptor.NativeAuthRequest{
		Harness:   model.AgentNameKimi,
		Operation: model.HarnessAuthStatus,
	}
	_, err := auth.RunKimi(t.Context(), req)
	require.ErrorContains(t, err, "start native")
	require.NoError(
		t,
		os.WriteFile(launcher, []byte("#!/bin/sh\nprintf 'private-native-output'\nexit 1\n"), 0700),
	)
	_, err = auth.RunKimi(t.Context(), req)
	require.ErrorContains(t, err, "exited before startup")
	require.NotContains(t, err.Error(), "private-native-output")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexec sleep 60\n"), 0700))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = auth.RunKimi(ctx, req)
	require.ErrorContains(t, err, "context deadline")
}
func TestAntigravityPreservesNativeSettingsAndEnvironmentOwnership(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("GEMINI_API_KEY", "fixture-private-key")
	t.Setenv("AGY_ADC_AUTH", "")
	path := filepath.Join(root, ".gemini", "antigravity-cli", "settings.json")
	sdkFixtureFile(t, path, `{"theme":"original","nested":{"keep":true}}`)
	auth := adaptor.NewNativeHarnessAuth()
	req := &adaptor.NativeAuthRequest{
		Harness:   model.AgentNameAntigravity,
		Operation: model.HarnessAuthLogin,
		Method:    model.HarnessAuthAPIKey,
	}
	result, err := auth.RunAntigravity(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthConfigured, result.State)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"theme": "original"`)
	require.Contains(t, string(data), `"modelProvider": "gemini"`)
	require.NotContains(t, string(data), "fixture-private-key")
	req.Operation = model.HarnessAuthStatus
	req.Method = ""
	result, err = auth.Status(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthConfigured, result.State)
	require.Nil(t, result.LoggedIn)
	t.Setenv("GEMINI_API_KEY", "")
	result, err = auth.Status(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthStateUnknown, result.State)
	req.Operation = model.HarnessAuthMethods
	result, err = auth.RunAntigravity(t.Context(), req)
	require.NoError(t, err)
	require.Contains(t, result.Providers[0].Methods, model.HarnessAuthInteractive)
	req.Operation = model.HarnessAuthLogin
	req.Method = model.HarnessAuthADC
	_, err = auth.RunAntigravity(t.Context(), req)
	require.ErrorContains(t, err, "AGY_ADC_AUTH")
	t.Setenv("AGY_ADC_AUTH", "true")
	result, err = auth.RunAntigravity(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthConfigured, result.State)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "modelProvider")
	require.Contains(t, string(data), `"theme": "original"`)
	req.Operation = model.HarnessAuthStatus
	req.Method = ""
	result, err = auth.RunAntigravity(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "adc", result.AuthMethod)
	req.Operation = model.HarnessAuthLogin
	req.Method = model.HarnessAuthAPIKey
	_, err = auth.RunAntigravity(t.Context(), req)
	require.ErrorContains(t, err, "GEMINI_API_KEY")
	req.Method = ""
	req.Stdin = strings.NewReader("")
	_, err = auth.RunAntigravity(t.Context(), req)
	require.ErrorContains(t, err, "requires a terminal")
	req.Operation = model.HarnessAuthLogout
	_, err = auth.RunAntigravity(t.Context(), req)
	require.ErrorContains(t, err, "requires a terminal")
}
func TestRemainingAuthValidationRejectsInvalidSelectors(t *testing.T) {
	auth := adaptor.NewNativeHarnessAuth()
	for _, h := range []model.AgentName{model.AgentNameKimi, model.AgentNameAntigravity} {
		for _, req := range []*adaptor.NativeAuthRequest{{Harness: h, Operation: model.HarnessAuthStatus, Provider: "bad"}, {Harness: h, Operation: model.HarnessAuthStatus, Method: model.HarnessAuthBrowser}, {Harness: h}} {
			require.Error(t, auth.Validate(req))
		}
	}
	_, err := auth.RunKimi(t.Context(), nil)
	require.Error(t, err)
	_, err = auth.RunAntigravity(t.Context(), nil)
	require.Error(t, err)
}

func TestAntigravityStatusDoesNotTreatAnUnselectedAPIKeyAsActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "unused-fixture-key")
	t.Setenv("AGY_ADC_AUTH", "")
	result, err := adaptor.NewNativeHarnessAuth().
		RunAntigravity(t.Context(), &adaptor.NativeAuthRequest{Harness: model.AgentNameAntigravity, Operation: model.HarnessAuthStatus})
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthStateUnknown, result.State)
}

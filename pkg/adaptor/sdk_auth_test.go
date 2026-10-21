//go:build !windows

package adaptor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func sdkFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
}
func geminiSDKFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PAXL_AUTH_FIXTURE", root)
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	t.Setenv("PAXL_AUTH_NODE", node)
	sdkFixtureFile(t, filepath.Join(root, "package.json"), `{"name":"@google/gemini-cli"}`)
	launcher := filepath.Join(root, "bin", "gemini")
	sdkFixtureFile(t, launcher, "#!/bin/sh\nexit 99\n")
	require.NoError(t, os.Chmod(launcher, 0700))
	t.Setenv("GEMINI_EXECUTABLE", launcher)
	core := filepath.Join(root, "node_modules", "@google", "gemini-cli-core")
	sdkFixtureFile(
		t,
		filepath.Join(core, "package.json"),
		`{"name":"@google/gemini-cli-core","type":"module","main":"index.mjs"}`,
	)
	sdkFixtureFile(t, filepath.Join(core, "index.mjs"), `
import * as fs from 'node:fs/promises';import {join} from 'node:path';
const root=process.env.PAXL_AUTH_FIXTURE,key=join(root,'native-key');
export const Storage={getGlobalSettingsPath:()=>join(root,'settings.json'),getOAuthCredsPath:()=>join(root,'oauth.json')};
export const AuthType={USE_GEMINI:'gemini-api-key',USE_VERTEX_AI:'vertex-ai',LOGIN_WITH_GOOGLE:'oauth-personal'};
export async function loadApiKey(){return fs.readFile(key,'utf8').catch(e=>{if(e.code==='ENOENT')return null;throw e;});}
export async function saveApiKey(value){if(process.env.PAXL_FAIL_SAVE==='true')return;if(value===null)await fs.unlink(key).catch(()=>{});else {console.error('native key: '+value);await fs.writeFile(key,value);}}
export async function getOauthClient(){if(process.env.PAXL_OAUTH_FAIL==='true')throw Error('sensitive-provider-diagnostic');if(process.env.PAXL_OAUTH_WAIT==='true')await new Promise(()=>{});await fs.writeFile(Storage.getOAuthCredsPath(),'native-private-token');}
export async function clearCachedCredentialFile(){await fs.unlink(Storage.getOAuthCredsPath()).catch(()=>{});}
`)
	return root
}
func TestGeminiSDKOwnsCredentialPersistenceAndPreservesSettings(t *testing.T) {
	root := geminiSDKFixture(t)
	auth := adaptor.NewNativeHarnessAuth()
	var diagnostics bytes.Buffer
	req := &adaptor.NativeAuthRequest{
		Harness:   model.AgentNameGemini,
		Operation: model.HarnessAuthMethods,
		Stderr:    &diagnostics,
	}
	methods, err := auth.RunSDK(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, methods.Providers, 1)
	require.Contains(t, methods.Providers[0].Methods, model.HarnessAuthVertex)
	sdkFixtureFile(
		t,
		filepath.Join(root, "settings.json"),
		`{"theme":"saved","security":{"other":true}}`,
	)
	req.Operation = model.HarnessAuthLogin
	req.Method = model.HarnessAuthAPIKey
	req.SecretStdin = true
	req.Stdin = strings.NewReader("fixture-secret-only\n")
	result, err := auth.RunSDK(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthSucceeded, result.State)
	require.NotContains(t, diagnostics.String(), "fixture-secret-only")
	data, err := os.ReadFile(filepath.Join(root, "native-key"))
	require.NoError(t, err)
	require.Equal(t, "fixture-secret-only", string(data))
	data, err = os.ReadFile(filepath.Join(root, "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"theme": "saved"`)
	require.Contains(t, string(data), `"selectedType": "gemini-api-key"`)
	var settings struct {
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
	}
	require.NoError(t, json.Unmarshal(data, &settings))
	require.Equal(t, "gemini-api-key", settings.Security.Auth.SelectedType)
	require.NotContains(t, string(data), "fixture-secret-only")
	req.Method = ""
	req.SecretStdin = false
	req.Operation = model.HarnessAuthStatus
	result, err = auth.Status(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthConfigured, result.State)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-secret-only")
	req.Operation = model.HarnessAuthLogout
	result, err = auth.RunSDK(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthSucceeded, result.State)
	_, err = os.Stat(filepath.Join(root, "native-key"))
	require.True(t, os.IsNotExist(err))
	req.Operation = model.HarnessAuthStatus
	result, err = auth.RunSDK(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthStateUnknown, result.State)
}
func TestGeminiSDKReportsFailuresAndRejectsNonInteractiveSelection(t *testing.T) {
	geminiSDKFixture(t)
	auth := adaptor.NewNativeHarnessAuth()
	for _, tc := range []struct {
		provider, method, secret, env string
		secretMode                    bool
		contains                      string
	}{
		{provider: "unknown", contains: "provider google"},
		{method: "device", contains: "interactive terminal"},
		{method: "api-key", secret: "key\n", env: "PAXL_FAIL_SAVE", secretMode: true, contains: "did not retain"},
		{method: "browser", env: "PAXL_OAUTH_FAIL", contains: "Native SDK authentication failed"},
	} {
		t.Run(tc.method+tc.provider, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(tc.env, "true")
			}
			_, err := auth.RunSDK(
				t.Context(),
				&adaptor.NativeAuthRequest{
					Harness:     model.AgentNameGemini,
					Operation:   model.HarnessAuthLogin,
					Provider:    tc.provider,
					Method:      model.HarnessAuthMethod(tc.method),
					SecretStdin: tc.secretMode,
					Stdin:       strings.NewReader(tc.secret),
				},
			)
			require.ErrorContains(t, err, tc.contains)
			require.NotContains(t, err.Error(), "sensitive-provider-diagnostic")
		})
	}
}
func TestSDKInstallationValidationAndCancellation(t *testing.T) {
	auth := adaptor.NewNativeHarnessAuth()
	_, err := auth.RunSDK(t.Context(), nil)
	require.Error(t, err)
	root := geminiSDKFixture(t)
	for _, req := range []*adaptor.NativeAuthRequest{
		{Harness: model.AgentNameGemini, Provider: "--bad", Operation: model.HarnessAuthLogin},
		{Harness: model.AgentNameGemini, Profile: "bad", Operation: model.HarnessAuthStatus},
		{Harness: model.AgentNameGemini, Method: model.HarnessAuthBrowser, Operation: model.HarnessAuthStatus},
		{Harness: model.AgentNameGemini},
	} {
		_, err = auth.RunSDK(t.Context(), req)
		require.Error(t, err)
	}
	t.Setenv("GEMINI_EXECUTABLE", filepath.Join(root, "missing"))
	_, err = auth.RunSDK(
		t.Context(),
		&adaptor.NativeAuthRequest{
			Harness:   model.AgentNameGemini,
			Operation: model.HarnessAuthMethods,
		},
	)
	require.ErrorContains(t, err, "selected gemini")
	t.Setenv("GEMINI_EXECUTABLE", filepath.Join(root, "bin", "gemini"))
	sdkFixtureFile(t, filepath.Join(root, "package.json"), `{"name":"unrelated"}`)
	_, err = auth.RunSDK(
		t.Context(),
		&adaptor.NativeAuthRequest{
			Harness:   model.AgentNameGemini,
			Operation: model.HarnessAuthMethods,
		},
	)
	require.ErrorContains(t, err, "supported official SDK installation")
}
func TestGeminiSDKCancellationDoesNotHang(t *testing.T) {
	geminiSDKFixture(t)
	t.Setenv("PAXL_OAUTH_WAIT", "true")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := adaptor.NewNativeHarnessAuth().
		RunSDK(ctx, &adaptor.NativeAuthRequest{Harness: model.AgentNameGemini, Operation: model.HarnessAuthLogin})
	require.Error(t, err)
}

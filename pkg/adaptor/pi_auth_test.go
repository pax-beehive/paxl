//go:build !windows

package adaptor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func fakePiSDK(t *testing.T, source string) string {
	t.Helper()
	check := exec.CommandContext(
		t.Context(),
		"node",
		"-e",
		"const [a,b]=process.versions.node.split('.').map(Number); process.exit(a<22||(a===22&&b<19)?1:0)",
	)
	if err := check.Run(); err != nil {
		t.Skip("Pi SDK integration requires Node 22.19+")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0700))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, "package.json"),
			[]byte(
				`{"name":"@earendil-works/pi-coding-agent","type":"module","version":"1.3.0","bin":{"pi":"dist/cli.js"}}`,
			),
			0600,
		),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "dist", "cli.js"), []byte("#!/usr/bin/env node\n"), 0700),
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "index.js"), []byte(source), 0600))
	t.Setenv("PI_EXECUTABLE", filepath.Join(root, "dist", "cli.js"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	return root
}

func TestPiStatusUsesInstalledSDKWithoutReturningCredentials(t *testing.T) {
	fakePiSDK(t, `export const VERSION='1.3.0';
 export class ModelRuntime {
 static async create(options) { if(options.refreshOnCreate!==false) throw Error('Unexpected refresh'); return new ModelRuntime(); }
 getProviders(){return [{id:'openai',name:'OpenAI',auth:{apiKey:{name:'API key',login(){}}}}];}
 async listCredentials(){return [{providerId:'openai',type:'api_key',key:'do-not-return'}];}
 }`)
	result, err := NewNativeHarnessAuth().RunPi(t.Context(), &NativeAuthRequest{Harness: model.AgentNamePi, Operation: model.HarnessAuthStatus})
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthConfigured, result.State)
	require.Nil(t, result.LoggedIn)
	require.Len(t, result.Providers, 1)
	require.Equal(t, "openai", result.Providers[0].ID)
	require.Equal(t, "api_key", result.Providers[0].CredentialType)
}

func TestPiLoginMethodsAndLogoutUseNativeSDK(t *testing.T) {
	root := fakePiSDK(t, `import {writeFile} from 'node:fs/promises';
 export const VERSION='1.3.0';
 const providers=[{id:'example',name:'Example',auth:{oauth:{login(){}} ,apiKey:{login(){}}}}];
 export class ModelRuntime {
 static async create(){return new ModelRuntime()}
 getProviders(){return providers}
 async listCredentials(){return [{providerId:'example',type:'api_key'}]}
 async login(provider,method,interaction){
 if(provider!=='example'||method!=='api_key') throw Error('Wrong selection');
 const key=await interaction.prompt({type:'secret',message:'API key:'});
 await writeFile(new URL('../stored-test-key',import.meta.url),key);
 interaction.notify({type:'progress',message:'Stored '+key});
 }
 async logout(provider){await writeFile(new URL('../logout-provider',import.meta.url),provider)}
 }`)
	auth := NewNativeHarnessAuth()
	result, err := auth.RunPi(
		t.Context(),
		&NativeAuthRequest{Harness: model.AgentNamePi, Operation: model.HarnessAuthMethods},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]model.HarnessAuthMethod{model.HarnessAuthOAuth, model.HarnessAuthAPIKey},
		result.Providers[0].Methods,
	)
	var stderr strings.Builder
	result, err = auth.RunPi(
		t.Context(),
		&NativeAuthRequest{
			Harness:   model.AgentNamePi,
			Operation: model.HarnessAuthLogin,
			Provider:  "example",
			Method:    model.HarnessAuthAPIKey,
			Stdin:     strings.NewReader("fixture-secret\n"),
			Stderr:    &stderr,
		},
	)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthSucceeded, result.State)
	stored, err := os.ReadFile(filepath.Join(root, "stored-test-key"))
	require.NoError(t, err)
	require.Equal(t, "fixture-secret", string(stored))
	require.NotContains(t, stderr.String(), "fixture-secret")
	_, err = auth.RunPi(
		t.Context(),
		&NativeAuthRequest{
			Harness:   model.AgentNamePi,
			Operation: model.HarnessAuthLogout,
			Provider:  "example",
		},
	)
	require.NoError(t, err)
	removed, err := os.ReadFile(filepath.Join(root, "logout-provider"))
	require.NoError(t, err)
	require.Equal(t, "example", string(removed))
}

func TestPiRejectsUnsupportedProviderMethodAndMissingSelection(t *testing.T) {
	fakePiSDK(t, `export class ModelRuntime {
 static async create(){return new ModelRuntime()}
 getProviders(){return [{id:'example',auth:{apiKey:{login(){}}}}]}
 async listCredentials(){return []}
 }`)
	for _, test := range []struct {
		provider string
		method   model.HarnessAuthMethod
		message  string
	}{
		{"example", model.HarnessAuthOAuth, "supported methods for example: api-key"},
		{"", model.HarnessAuthMethodUnknown, "Available choices: example"},
		{"missing", model.HarnessAuthOAuth, "Available providers: example"},
	} {
		_, err := NewNativeHarnessAuth().RunPi(t.Context(), &NativeAuthRequest{Harness: model.AgentNamePi, Operation: model.HarnessAuthLogin, Provider: test.provider, Method: test.method})
		require.ErrorContains(t, err, test.message)
	}
}

func TestPiOAuthForwardsURLAndCancellation(t *testing.T) {
	fakePiSDK(t, `export class ModelRuntime {
 static async create(){return new ModelRuntime()}
 getProviders(){return [{id:'example',auth:{oauth:{login(){}}}}]}
 async listCredentials(){return []}
 async login(provider,method,interaction){
 interaction.notify({type:'auth_url',url:'https://example.invalid/authorize'});
 await new Promise((resolve,reject)=>{const timer=setTimeout(resolve,30000);interaction.signal.addEventListener('abort',()=>{clearTimeout(timer);reject(Error('cancelled'))},{once:true})});
 }
 }`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stderr := &cancelOnAuthURL{cancel: cancel}
	_, err := NewNativeHarnessAuth().RunPi(ctx, &NativeAuthRequest{Harness: model.AgentNamePi, Operation: model.HarnessAuthLogin, Provider: "example", Method: model.HarnessAuthOAuth, Stderr: stderr})
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, stderr.output.String(), "https://example.invalid/authorize")
}

func TestPiManagedInstallationUsesSelectedReleaseAndPrivateNode(t *testing.T) {
	sdkRoot := fakePiSDK(t, `export const VERSION='1.3.0'; export class ModelRuntime {
 static async create(){return new ModelRuntime()}
 getProviders(){return []}
 async listCredentials(){return []}
 }`)
	agent := t.TempDir()
	managed := filepath.Join(agent, "install")
	modules := filepath.Join(managed, "releases", "1.3.0", "node_modules")
	require.NoError(t, os.MkdirAll(filepath.Join(modules, "@earendil-works"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(modules, ".bin"), 0700))
	require.NoError(
		t,
		os.Symlink(sdkRoot, filepath.Join(modules, "@earendil-works", "pi-coding-agent")),
	)
	require.NoError(
		t,
		os.Symlink(filepath.Join(sdkRoot, "dist", "cli.js"), filepath.Join(modules, ".bin", "pi")),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(managed, "managed-install.json"),
			[]byte(`{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`),
			0600,
		),
	)
	current := filepath.Join(managed, "current-version")
	require.NoError(t, os.WriteFile(current, []byte("1.3.0\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(agent, "bin"), 0700))
	launcher := filepath.Join(agent, "bin", "pi")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 99\n"), 0700))
	dataHome := t.TempDir()
	privateNode := filepath.Join(dataHome, "pi-node", "current", "bin", "node")
	require.NoError(t, os.MkdirAll(filepath.Dir(privateNode), 0700))
	systemNode, err := exec.LookPath("node")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(systemNode, privateNode))
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("PI_EXECUTABLE", launcher)
	location, err := ResolvePiInstallation(launcher)
	require.NoError(t, err)
	require.Equal(t, "pi-managed", location.Source)
	require.Equal(t, privateNode, location.Node)
	result, err := NewNativeHarnessAuth().RunPi(t.Context(), &NativeAuthRequest{Harness: model.AgentNamePi, Operation: model.HarnessAuthStatus})
	require.NoError(t, err)
	require.Equal(t, "1.3.0", result.RuntimeVersion)
	for _, version := range []string{"../../escape", "missing", "2.0.0"} {
		require.NoError(t, os.WriteFile(current, []byte(version), 0600))
		_, err := ResolvePiInstallation(launcher)
		require.Error(t, err)
	}
}

type cancelOnAuthURL struct {
	output strings.Builder
	cancel context.CancelFunc
}

func (w *cancelOnAuthURL) Write(data []byte) (int, error) {
	n, err := w.output.Write(data)
	if strings.Contains(w.output.String(), "https://example.invalid/authorize") {
		w.cancel()
	}
	return n, err
}

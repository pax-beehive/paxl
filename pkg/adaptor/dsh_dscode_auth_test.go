//go:build !windows

package adaptor_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

const contextFixture = `
import * as fs from 'node:fs/promises';import {join} from 'node:path';
const state=join(process.env.PAXL_AUTH_FIXTURE,'native-credential');
const has=()=>fs.stat(state).then(()=>true,()=>false);
export class Context {
 constructor(){this.fiber={dispose:async()=>{}};}
 async plugin(plugin){
  const id=plugin.default?.marker??plugin.marker??plugin.name;
  if(id==='credentials')this.credentials={
   describeRecord:async()=>({configured:await has(),kind:'api-key',writable:true}),
   describe:async()=>({configured:await has(),kind:'api-key',writable:true}),
   deleteRecord:async()=>fs.unlink(state).catch(()=>{}),unset:async()=>fs.unlink(state).catch(()=>{}),
   set:async(_ref,value)=>{console.error('saved '+value);await fs.writeFile(state,value);}
  };
  if(id==='authorization')this.authorization={list:()=>[{key:'pi-ai/openai',label:'OpenAI',methods:[{id:'api-key'}]}],begin:async options=>{
   const key=await options.interaction.prompt({kind:'secret',message:'Native key',signal:options.signal});
   if(process.env.PAXL_CANCEL_AUTH==='true')return {status:'cancelled'};
   await this.credentials.set('native',key);options.interaction.notify({message:'Native credential saved'});return {status:'authorized'};
  }};
  if(id==='webserver')this.webServer={port:12345};
  if(id==='account')this.deepseekAccount={startSignIn:async()=>({attempt:{id:'owned'}}),cancelSignIn:async()=>{},signOut:async()=>this.credentials.unset(),async *watch(){yield {attempt:{id:'owned',authorizeUrl:'https://example.invalid/native-auth',phase:'waiting-browser'}};yield {attempt:{id:'owned',phase:process.env.PAXL_CANCEL_AUTH==='true'?'failed':'succeeded'}};}};
  if(['openai','opencode-go','antigravity'].includes(id)){
   const auth={save:async()=>{await fs.writeFile(state,'native-oauth');return true;},select:async()=>{},logout:async()=>this.credentials.unset()};
   const store={write:async()=>fs.writeFile(state,'native-oauth')};
   const login={auth,session:auth,deps:{store},cancel:()=>{},logout:async()=>this.credentials.unset(),login:async()=>{
    login.attempt={done:Promise.resolve().then(async()=>{if(process.env.PAXL_CANCEL_AUTH==='true')return;if(id==='opencode-go')await store.write();else await auth.save();})};
    return {lines:['Open native browser URL']};
   }};
   if(id==='openai')this.dscodeOpenAI={login,auth};else if(id==='opencode-go')this.dscodeOpenCodeLogin=login;else this.dscodeAntigravityLogin=login;
  }
 }
}
`

func providerSDKFixture(t *testing.T, harness string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PAXL_AUTH_FIXTURE", root)
	t.Setenv("DSCODE_HOME", filepath.Join(root, "dscode-state"))
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	t.Setenv("PAXL_AUTH_NODE", node)
	name := "@deepseek-ai/dsh"
	if harness == "dscode" {
		name = "todd-dsh-tui-harness"
		require.NoError(t, os.MkdirAll(filepath.Join(root, "runtime", "bin"), 0700))
		require.NoError(t, os.Symlink(node, filepath.Join(root, "runtime", "bin", "node")))
	}
	sdkFixtureFile(t, filepath.Join(root, "package.json"), `{"name":"`+name+`"}`)
	launcher := filepath.Join(root, "bin", harness)
	sdkFixtureFile(t, launcher, "#!/bin/sh\nexit 99\n")
	require.NoError(t, os.Chmod(launcher, 0700))
	t.Setenv(strings.ToUpper(harness)+"_EXECUTABLE", launcher)
	modules := map[string]string{
		"cordis":                        contextFixture,
		"dsh-credentials-local":         `export default {marker:'credentials'};`,
		"dsh-authorization":             `export default {marker:'authorization'};`,
		"dsh-llm":                       `export default {marker:'llm'};`,
		"dsh-llm-pi-ai":                 `export const marker='providers';`,
		"dsh-deepseek-account-platform": `export default {marker:'account'};`,
		"dsh-host-webserver":            `export default {marker:'webserver'};`,
	}
	for name, body := range modules {
		dir := filepath.Join(root, "node_modules", "@deepseek-ai", name)
		sdkFixtureFile(
			t,
			filepath.Join(dir, "package.json"),
			`{"type":"module","main":"index.mjs"}`,
		)
		sdkFixtureFile(t, filepath.Join(dir, "index.mjs"), body)
	}
	for _, name := range []string{"credentials", "openai", "opencode-go", "antigravity"} {
		body := `export default {marker:'credentials'};`
		if name != "credentials" {
			body = `export const marker='` + name + `';`
		}
		sdkFixtureFile(t, filepath.Join(root, "plugins", name, "index.mjs"), body)
	}
	sdkFixtureFile(
		t,
		filepath.Join(root, "plugins", "providers", "catalog.mjs"),
		`export const PROVIDERS=[{id:'openai',name:'OpenAI',credentialRef:'OPENAI_API_KEY',login:true},{id:'deepseek-official',name:'DeepSeek',credentialRef:'DEEPSEEK_API_KEY'},{id:'opencode-go',login:true},{id:'antigravity',login:true},{id:'grok'}];`,
	)
	return root
}
func TestDSHAndDSCODEUseNativeProviderMetadataAndCredentialOwners(t *testing.T) {
	for _, h := range []string{"dsh", "dscode"} {
		t.Run(h, func(t *testing.T) {
			root := providerSDKFixture(t, h)
			auth := adaptor.NewNativeHarnessAuth()
			var diagnostics bytes.Buffer
			req := &adaptor.NativeAuthRequest{
				Harness:   model.AgentName(h),
				Operation: model.HarnessAuthMethods,
				Stderr:    &diagnostics,
			}
			result, err := auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.NotEmpty(t, result.Providers)
			for _, p := range result.Providers {
				require.NotEqual(t, "grok", p.ID)
			}
			req.Operation = model.HarnessAuthLogin
			req.Provider = "openai"
			req.Method = model.HarnessAuthAPIKey
			req.SecretStdin = true
			req.Stdin = strings.NewReader("provider-private-key\n")
			result, err = auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			require.NotContains(t, diagnostics.String(), "provider-private-key")
			key, err := os.ReadFile(filepath.Join(root, "native-credential"))
			require.NoError(t, err)
			require.Equal(t, "provider-private-key", string(key))
			req.Operation = model.HarnessAuthStatus
			req.Method = ""
			req.SecretStdin = false
			result, err = auth.Status(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthConfigured, result.State)
			req.Operation = model.HarnessAuthLogout
			result, err = auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			req.Operation = model.HarnessAuthStatus
			result, err = auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthStateUnknown, result.State)
		})
	}
}
func TestDSCODEOAuthRequiresNewNativeCommit(t *testing.T) {
	for _, provider := range []string{"openai", "opencode-go", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			root := providerSDKFixture(t, "dscode")
			auth := adaptor.NewNativeHarnessAuth()
			req := &adaptor.NativeAuthRequest{
				Harness:   model.AgentNameDSCode,
				Operation: model.HarnessAuthLogin,
				Provider:  provider,
				Method:    model.HarnessAuthOAuth,
			}
			result, err := auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			sdkFixtureFile(t, filepath.Join(root, "native-credential"), "stale-token")
			t.Setenv("PAXL_CANCEL_AUTH", "true")
			_, err = auth.RunSDK(t.Context(), req)
			require.ErrorContains(t, err, "did not commit")
		})
	}
}
func TestSDKAccountFlowAndCancelledProviderDoNotReportSuccess(t *testing.T) {
	for _, h := range []string{"dsh", "dscode"} {
		t.Run(h, func(t *testing.T) {
			providerSDKFixture(t, h)
			auth := adaptor.NewNativeHarnessAuth()
			var diagnostics bytes.Buffer
			req := &adaptor.NativeAuthRequest{
				Harness:   model.AgentName(h),
				Operation: model.HarnessAuthLogin,
				Provider:  "deepseek-account",
				Method:    model.HarnessAuthBrowser,
				Stderr:    &diagnostics,
			}
			result, err := auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			require.Contains(t, diagnostics.String(), "https://example.invalid/native-auth")
			req.Operation = model.HarnessAuthLogout
			req.Method = ""
			result, err = auth.RunSDK(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthSucceeded, result.State)
			req.Operation = model.HarnessAuthLogin
			req.Method = model.HarnessAuthBrowser
			t.Setenv("PAXL_CANCEL_AUTH", "true")
			_, err = auth.RunSDK(t.Context(), req)
			require.ErrorContains(t, err, "did not complete")
			req.Provider = "not-installed"
			_, err = auth.RunSDK(t.Context(), req)
			require.ErrorContains(t, err, "Unknown")
		})
	}
}

func TestSDKProtocolCanSelectProviderAndAnswerMaskedPromptWithoutTTY(t *testing.T) {
	providerSDKFixture(t, "dsh")
	auth := adaptor.NewNativeHarnessAuth()
	var events []model.HarnessAuthEvent
	req := &adaptor.NativeAuthRequest{
		Harness:   model.AgentNameDSH,
		Operation: model.HarnessAuthLogin,
		Stdin:     strings.NewReader("openai\nprotocol-private-key\n"),
		Events:    func(event *model.HarnessAuthEvent) error { events = append(events, *event); return nil },
	}
	result, err := auth.RunSDK(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthSucceeded, result.State)
	var prompts []model.HarnessAuthEvent
	for _, event := range events {
		require.NotContains(t, event.Message, "protocol-private-key")
		if event.Type == "prompt" {
			prompts = append(prompts, event)
		}
	}
	require.Len(t, prompts, 2)
	require.Equal(t, "select", prompts[0].Kind)
	require.Equal(t, "secret", prompts[1].Kind)
	require.NotEqual(t, prompts[0].ID, prompts[1].ID)
}

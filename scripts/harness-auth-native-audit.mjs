// Offline audit of installed SDK credential contracts. All writes use a fresh
// temporary HOME and synthetic keys; OAuth sign-in and inference are never run.
import assert from "node:assert/strict";
import {
  mkdtempSync,
  writeFileSync,
  readFileSync,
  mkdirSync,
  rmSync,
  realpathSync,
  existsSync,
  readdirSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { createInterface } from "node:readline";

const paxl = resolve(process.argv[2] ?? "/tmp/paxl-local-harness-auth");
const scratch = mkdtempSync(join(tmpdir(), "paxl-native-auth-audit-"));
const key = "paxl-offline-audit-synthetic-key";
const deny = join(scratch, "deny-network.mjs");
writeFileSync(
  deny,
  `import net from 'node:net';import tls from 'node:tls';import http from 'node:http';import https from 'node:https';
const deny=()=>{throw Error('Network disabled by native auth audit')};
globalThis.fetch=deny;net.connect=deny;net.createConnection=deny;tls.connect=deny;http.request=deny;http.get=deny;https.request=deny;https.get=deny;\n`,
);
const node = realpathSync(process.execPath);
const wrapper = join(scratch, "node-offline");
const quote = (value) => "'" + value.replaceAll("'", "'\\''") + "'";
writeFileSync(
  wrapper,
  `#!/bin/sh\nexec ${quote(node)} --import ${quote(deny)} "$@"\n`,
  { mode: 0o700 },
);
const baseEnv = {
  PATH: process.env.PATH,
  HOME: scratch,
  TMPDIR: tmpdir(),
  LANG: "en_US.UTF-8",
  PAXL_AUTH_NODE: wrapper,
  GEMINI_FORCE_ENCRYPTED_FILE_STORAGE: "true",
  NODE_OPTIONS: "--import=" + deny,
};
function environment(name) {
  const home = join(scratch, name);
  mkdirSync(home, { recursive: true });
  return {
    ...baseEnv,
    HOME: home,
    DSH_HOME: join(home, ".dsh"),
    DSCODE_HOME: join(home, "dscode-state"),
    PI_CODING_AGENT_DIR: join(home, ".pi", "agent"),
  };
}
function run(command, args, env, input = "") {
  const result = spawnSync(command, args, {
    env,
    cwd: env.HOME,
    input,
    encoding: "utf8",
    timeout: 45000,
    maxBuffer: 1024 * 1024,
  });
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(
    result.status,
    0,
    `${command} failed: ${result.stderr}\n${result.stdout}`,
  );
  return result;
}
function login(harness, provider, env) {
  const result = run(
    paxl,
    [
      "auth",
      "login",
      "--harness",
      harness,
      ...(provider ? ["--provider", provider] : []),
      "--method",
      "api-key",
      "--secret-stdin",
      "--format",
      "json",
    ],
    env,
    key + "\n",
  );
  assert.equal(
    (result.stdout + result.stderr).includes(key),
    false,
    "credential leaked",
  );
  assert.equal(JSON.parse(result.stdout).state, "succeeded");
}
function reader(script, env, nodePath = node) {
  run(nodePath, ["--import", deny, "--input-type=module", "-e", script], env);
}
function rootOf(launcher, name) {
  if (!existsSync(launcher)) return;
  let path = dirname(realpathSync(launcher));
  for (let index = 0; index < 10; index++) {
    const manifest = join(path, "package.json");
    if (
      existsSync(manifest) &&
      JSON.parse(readFileSync(manifest)).name === name
    )
      return path;
    const parent = dirname(path);
    if (parent === path) break;
    path = parent;
  }
}
function logout(harness, provider, env) {
  run(
    paxl,
    [
      "auth",
      "logout",
      "--harness",
      harness,
      ...(provider ? ["--provider", provider] : []),
    ],
    env,
  );
}
// Exercise the real adapter without sending a model prompt or contacting a provider.
async function auditPiACP(sdkRoot, env) {
  const entry = "/usr/local/lib/node_modules/@ccgv2/pi-acp/dist/index.mjs";
  const child = spawn(node, ["--import", deny, entry], {
    env: { ...env, PI_ACP_SDK_ROOT: sdkRoot },
    cwd: env.HOME,
    stdio: ["pipe", "pipe", "pipe"],
  });
  const closed = new Promise((resolve) => child.once("close", resolve));
  child.stdin.on("error", () => {});
  let diagnostics = "";
  child.stderr.on("data", (chunk) => {
    diagnostics = (diagnostics + chunk).slice(-2048);
  });
  const lines = createInterface({ input: child.stdout })[
    Symbol.asyncIterator
  ]();
  const timeout = setTimeout(() => child.kill("SIGKILL"), 20000);
  async function request(id, method, params) {
    child.stdin.write(
      JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n",
    );
    while (true) {
      const line = await lines.next();
      assert.equal(
        line.done,
        false,
        "Pi ACP closed before replying: " +
          diagnostics.replaceAll(key, "[REDACTED]"),
      );
      const reply = JSON.parse(line.value);
      assert.equal(
        line.value.includes(key),
        false,
        "ACP returned the credential",
      );
      if (reply.id !== id) continue;
      assert.equal(reply.error, undefined, JSON.stringify(reply.error));
      return reply.result;
    }
  }
  try {
    const initialized = await request(1, "initialize", {
      protocolVersion: 1,
      clientCapabilities: {},
      clientInfo: { name: "paxl-native-auth-audit", version: "1" },
    });
    const version = JSON.parse(
      readFileSync(join(sdkRoot, "package.json")),
    ).version;
    assert.equal(
      initialized._meta?.pax?.runtime?.version,
      version,
      "Adapter did not load the selected SDK",
    );
    const session = await request(2, "session/new", {
      cwd: env.HOME,
      mcpServers: [],
    });
    assert.equal(typeof session.sessionId, "string");
    assert.ok(
      JSON.stringify(session.models).includes("deepseek"),
      "ACP did not expose the authenticated provider models",
    );
  } finally {
    clearTimeout(timeout);
    child.kill("SIGTERM");
    await closed;
  }
}
let passed = 0;
try {
  const codex = join(process.env.HOME, ".local/bin/codex");
  if (existsSync(codex)) {
    const env = { ...environment("codex"), CODEX_PATH: codex };
    env.CODEX_HOME = join(env.HOME, ".codex");
    mkdirSync(env.CODEX_HOME, { recursive: true });
    writeFileSync(
      join(env.CODEX_HOME, "config.toml"),
      'cli_auth_credentials_store = "file"\n',
    );
    login("codex", undefined, env);
    const result = run(
      paxl,
      ["auth", "status", "--harness", "codex", "--format", "json"],
      env,
    );
    assert.equal(JSON.parse(result.stdout).state, "logged_in");
    assert.equal((result.stdout + result.stderr).includes(key), false);
    logout("codex", undefined, env);
    const after = run(
      paxl,
      ["auth", "status", "--harness", "codex", "--format", "json"],
      env,
    );
    assert.equal(JSON.parse(after.stdout).state, "logged_out");
    console.log(
      "PASS Codex: native CLI API-key save/status/logout agree in an isolated file store.",
    );
    passed++;
  } else console.log("SKIP Codex: installation absent.");
  const hermesPython = process.env.PAXL_AUDIT_HERMES_PYTHON;
  if (hermesPython) {
    assert.ok(existsSync(hermesPython));
    const env = environment("hermes");
    env.HERMES_HOME = join(env.HOME, ".hermes");
    const script = `import socket,contextlib,io
socket.socket.connect=lambda *args:(_ for _ in ()).throw(RuntimeError('Network disabled by audit'))
from types import SimpleNamespace
from hermes_cli.auth_commands import auth_add_command
from hermes_cli.runtime_provider import resolve_runtime_provider
with contextlib.redirect_stdout(io.StringIO()):
 auth_add_command(SimpleNamespace(provider='deepseek',auth_type='api-key',api_key=${JSON.stringify(key)},label='offline-audit',priority=None))
runtime=resolve_runtime_provider(requested='deepseek')
assert runtime['provider']=='deepseek'
assert runtime['api_key']==${JSON.stringify(key)}
`;
    run(hermesPython, ["-c", script], env);
    console.log(
      "PASS Hermes: installed native credential writer and explicit runtime provider resolver agree. Default provider/model setup remains separate.",
    );
    passed++;
  } else
    console.log(
      "SKIP Hermes native runtime: set PAXL_AUDIT_HERMES_PYTHON to its installed environment interpreter.",
    );
  const dsh = rootOf("/usr/local/bin/dsh", "@deepseek-ai/dsh");
  if (dsh) {
    const env = { ...environment("dsh"), DSH_EXECUTABLE: "/usr/local/bin/dsh" };
    login("dsh", "deepseek-official", env);
    const script = `import assert from 'node:assert/strict';import{createRequire}from'node:module';const require=createRequire(${JSON.stringify(join(dsh, "package.json"))});const{Context}=await import(require.resolve('@deepseek-ai/cordis'));const{credentialRef}=await import(require.resolve('@deepseek-ai/dsh-credentials'));const ctx=new Context();try{await ctx.plugin((await import(require.resolve('@deepseek-ai/dsh-credentials-local'))).default,{watch:false});const hit=await ctx.credentials.resolve(credentialRef('DEEPSEEK_API_KEY'));assert.equal(hit?.value,${JSON.stringify(key)});}finally{await ctx.fiber.dispose()}`;
    reader(script, env);
    logout("dsh", "deepseek-official", env);
    console.log(
      "PASS DSH: native runtime credential reference reads the saved key; native logout completed.",
    );
    passed++;
  } else console.log("SKIP DSH: supported installation absent.");
  const openclaw = rootOf("/usr/local/bin/openclaw", "openclaw");
  if (openclaw) {
    const env = {
      ...environment("openclaw"),
      OPENCLAW_EXECUTABLE: "/usr/local/bin/openclaw",
    };
    env.OPENCLAW_STATE_DIR = join(env.HOME, ".openclaw");
    env.OPENCLAW_CONFIG_PATH = join(env.OPENCLAW_STATE_DIR, "openclaw.json");
    mkdirSync(env.OPENCLAW_STATE_DIR, { recursive: true });
    writeFileSync(
      env.OPENCLAW_CONFIG_PATH,
      JSON.stringify({ agents: { entries: { audit: {} } } }),
    );
    login("openclaw", "deepseek", env);
    const status = run(
      paxl,
      [
        "auth",
        "status",
        "--harness",
        "openclaw",
        "--provider",
        "deepseek",
        "--format",
        "json",
      ],
      env,
    );
    const result = JSON.parse(status.stdout);
    assert.equal(result.state, "configured");
    assert.ok(
      result.providers.some(
        (item) =>
          item.id === "deepseek" &&
          item.profile_id === "deepseek:manual" &&
          item.credential_type === "api_key",
      ),
    );
    const storeModule = readdirSync(join(openclaw, "dist")).find(
      (name) => name.startsWith("store-runtime-") && name.endsWith(".mjs"),
    );
    assert.ok(storeModule);
    const source = readFileSync(join(openclaw, "dist", storeModule), "utf8");
    const alias =
      source.match(
        /ensureAuthProfileStoreWithoutExternalProfiles as (\w+)/,
      )?.[1] ?? "ensureAuthProfileStoreWithoutExternalProfiles";
    reader(
      `import assert from 'node:assert/strict';const native=await import(${JSON.stringify("file://" + join(openclaw, "dist", storeModule))});const store=native[${JSON.stringify(alias)}](${JSON.stringify(join(env.OPENCLAW_STATE_DIR, "agents", "audit", "agent"))});assert.equal(store.profiles['deepseek:manual']?.key,${JSON.stringify(key)});`,
      env,
    );
    console.log(
      "PASS OpenClaw: actual API-key login, native SQLite reader and profile status agree in an isolated configuration. Gateway setup and interactive logout remain unverified.",
    );
    passed++;
  } else console.log("SKIP OpenClaw: installation absent.");
  const opencode = join(process.env.HOME, ".opencode/bin/opencode");
  if (existsSync(opencode)) {
    const env = { ...environment("opencode"), OPENCODE_EXECUTABLE: opencode };
    env.XDG_DATA_HOME = join(env.HOME, "data");
    env.XDG_CONFIG_HOME = join(env.HOME, "config");
    env.XDG_CACHE_HOME = join(env.HOME, "cache");
    const store = join(env.XDG_DATA_HOME, "opencode", "auth.json");
    mkdirSync(dirname(store), { recursive: true });
    writeFileSync(store, JSON.stringify({ deepseek: { type: "api", key } }), {
      mode: 0o600,
    });
    const result = run(
      paxl,
      ["auth", "status", "--harness", "opencode", "--format", "json"],
      env,
    );
    assert.equal(JSON.parse(result.stdout).state, "configured");
    assert.equal((result.stdout + result.stderr).includes(key), false);
    logout("opencode", "deepseek", env);
    const after = run(
      paxl,
      ["auth", "status", "--harness", "opencode", "--format", "json"],
      env,
    );
    assert.equal(JSON.parse(after.stdout).state, "unknown");
    console.log(
      "PASS OpenCode: native CLI reports and removes an isolated saved credential. Native login remains unverified.",
    );
    passed++;
  } else console.log("SKIP OpenCode: installation absent.");
  const dscode = rootOf(
    join(process.env.HOME, ".local/bin/dscode"),
    "todd-dsh-tui-harness",
  );
  if (dscode) {
    const nodePath = join(dscode, "runtime/bin/node");
    // The standalone private node is used by paxl, so block its outbound networking too.
    const env = {
      ...environment("dscode"),
      DSCODE_EXECUTABLE: join(dscode, "dscode"),
      NODE_OPTIONS: "--import=" + deny,
    };
    for (const provider of ["deepseek-official", "openai"]) {
      login("dscode", provider, env);
      const script = `import assert from'node:assert/strict';import{createRequire}from'node:module';import{pathToFileURL}from'node:url';import{join}from'node:path';const require=createRequire(${JSON.stringify(join(dscode, "package.json"))});const{Context}=await import(require.resolve('@deepseek-ai/cordis'));const ctx=new Context();try{await ctx.plugin((await import(pathToFileURL(${JSON.stringify(join(dscode, "plugins/credentials/index.mjs"))}))).default,{dshHome:process.env.DSCODE_HOME,path:join(process.env.DSCODE_HOME,'credentials.yaml'),watch:false});const ref=${JSON.stringify(provider === "openai" ? "OPENAI_API_KEY" : "DEEPSEEK_API_KEY")};assert.equal((await ctx.credentials.resolve(ref))?.value,${JSON.stringify(key)});${provider === "openai" ? `const{OpenAIStore}=await import(pathToFileURL(${JSON.stringify(join(dscode, "plugins/openai/store.mjs"))}));assert.equal(new OpenAIStore(join(process.env.DSCODE_HOME,'openai')).read()?.mode,'api-key');` : ""}}finally{await ctx.fiber.dispose()}`;
      reader(script, env, nodePath);
      logout("dscode", provider, env);
    }
    console.log(
      "PASS DSCODE: native standalone reader resolves DeepSeek/OpenAI keys and OpenAI billing selection; native logout completed.",
    );
    passed++;
  } else console.log("SKIP DSCODE: supported installation absent.");
  const pi = rootOf("/usr/local/bin/pi", "@earendil-works/pi-coding-agent");
  if (pi) {
    const env = { ...environment("pi"), PI_EXECUTABLE: "/usr/local/bin/pi" };
    login("pi", "deepseek", env);
    reader(
      `import assert from'node:assert/strict';const {ModelRuntime}=await import(${JSON.stringify("file://" + join(pi, "dist/index.js"))});const runtime=await ModelRuntime.create({refreshOnCreate:false,allowModelNetwork:false});assert.ok((await runtime.listCredentials()).some(x=>x.providerId==='deepseek'&&x.type==='api_key'));`,
      env,
    );
    try {
      const dependencyRoot =
        "/usr/local/lib/node_modules/@ccgv2/pi-acp/node_modules/@earendil-works/pi-coding-agent";
      const dependencyManifest = JSON.parse(
        readFileSync(join(dependencyRoot, "package.json")),
      );
      const sdkEntry = join(
        dependencyRoot,
        dependencyManifest.exports?.["."]?.import ?? dependencyManifest.main,
      );
      reader(
        `import assert from'node:assert/strict';const sdk=await import(${JSON.stringify("file://" + sdkEntry)});if(sdk.ModelRuntime){const runtime=await sdk.ModelRuntime.create({refreshOnCreate:false,allowModelNetwork:false});assert.ok((await runtime.listCredentials()).some(x=>x.providerId==='deepseek'&&x.type==='api_key'));}else{const auth=sdk.AuthStorage.create();assert.equal(auth.get('deepseek')?.type,'api_key');assert.equal(auth.get('deepseek')?.key,${JSON.stringify(key)});}`,
        env,
      );
      console.log(
        "PASS Pi: both selected native SDK and installed ACP SDK read the same stored credential.",
      );
    } catch (error) {
      throw Error("Pi ACP SDK compatibility not verified: " + error.message);
    }
    await auditPiACP(pi, env);
    console.log(
      "PASS Pi ACP: selected SDK version reported by initialize; session/new exposes DeepSeek models from the saved credential, without inference.",
    );
    logout("pi", "deepseek", env);
    passed++;
  } else console.log("SKIP Pi: supported installation absent.");
  const gemini = rootOf("/usr/local/bin/gemini", "@google/gemini-cli");
  if (gemini) {
    const env = {
      ...environment("gemini"),
      GEMINI_EXECUTABLE: "/usr/local/bin/gemini",
    };
    login("gemini", undefined, env);
    const launcher = readFileSync(join(gemini, "bundle/gemini.js"), "utf8");
    const core = launcher.match(
      /import\(["']\.\/(dist-[A-Za-z0-9_-]+\.js)["']\)/,
    )?.[1];
    assert.ok(core);
    const main = launcher.match(
      /import\(["']\.\/(gemini-[A-Za-z0-9_-]+\.js)["']\)/,
    )?.[1];
    assert.ok(main);
    const mainSource = readFileSync(join(gemini, "bundle", main), "utf8");
    const config = mainSource.match(
      /import\s*\{[^}]*\bloadSettings\b[^}]*\}\s*from\s*["']([^"']+)["']/s,
    )?.[1];
    assert.ok(config);
    reader(
      `import assert from'node:assert/strict';const core=await import(${JSON.stringify("file://" + join(gemini, "bundle", core))});assert.equal(await core.loadApiKey(),${JSON.stringify(key)});const{loadSettings}=await import(${JSON.stringify("file://" + join(gemini, "bundle", config))});assert.equal(loadSettings(process.cwd()).merged.security.auth.selectedType,'gemini-api-key');`,
      env,
    );
    logout("gemini", undefined, env);
    console.log(
      "PASS Gemini: native key loader and actual CLI settings loader read the saved key and auth selection.",
    );
    passed++;
  } else console.log("SKIP Gemini: supported installation absent.");
  console.log(
    `Native SDK audit passed for ${passed} installed harnesses. Provider acceptance, OAuth, and inference are not verified.`,
  );
} finally {
  rmSync(scratch, { recursive: true, force: true });
}

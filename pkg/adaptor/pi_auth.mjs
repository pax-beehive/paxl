import { pathToFileURL } from "node:url";
import { join } from "node:path";
import { createInterface } from "node:readline/promises";
import { createInterface as createLineReader } from "node:readline";
import { Writable } from "node:stream";

const [
  root,
  operation,
  requestedProvider,
  requestedMethod,
  protocolMode,
  secretMode,
] = process.argv.slice(1);
const controller = new AbortController();
process.on("SIGINT", () => controller.abort());
process.on("SIGTERM", () => controller.abort());
const secrets = [];
const safe = (value) => {
  let text = String(value ?? "");
  for (const secret of secrets)
    if (secret) text = text.replaceAll(secret, "[REDACTED]");
  return text;
};
const protocol = protocolMode === "true";
const originalOut = process.stdout.write.bind(process.stdout);
const originalErr = process.stderr.write.bind(process.stderr);
let promptSequence = 0;
const emitEvent = (value) =>
  originalOut(JSON.stringify({ version: 1, ...value }) + "\n");
const write = (text) =>
  protocol
    ? emitEvent({ type: "message", message: safe(text) })
    : originalErr(safe(text));
process.stdout.write = (value, encoding, callback) => {
  const ok = write(value);
  (typeof encoding === "function" ? encoding : callback)?.();
  return ok;
};
process.stderr.write = (value, encoding, callback) => {
  const ok = write(value);
  (typeof encoding === "function" ? encoding : callback)?.();
  return ok;
};
const result = (value) =>
  originalOut(JSON.stringify({ harness: "pi", ...value }) + "\n");
class InputError extends Error {}
let terminal;
let lines;
let muted = false;
const isTerminal = Boolean(process.stdin.isTTY && process.stderr.isTTY);

async function question(
  message,
  secret = false,
  signal = controller.signal,
  emitPrompt = true,
) {
  if (protocol && emitPrompt && secretMode !== "true")
    emitEvent({
      type: "prompt",
      id: String(++promptSequence),
      kind: secret ? "secret" : "text",
      message,
    });
  if (signal.aborted) throw new InputError("Login stopped.");
  let answer;
  if (isTerminal) {
    if (!terminal) {
      const output = new Writable({
        write(chunk, _encoding, callback) {
          if (!muted) process.stderr.write(chunk);
          callback();
        },
      });
      output.columns = process.stderr.columns;
      terminal = createInterface({
        input: process.stdin,
        output,
        terminal: true,
        historySize: 0,
      });
      terminal.on("SIGINT", () => controller.abort());
    }
    if (secret) {
      write(message);
      muted = true;
      try {
        answer = await terminal.question("", { signal });
      } finally {
        muted = false;
        write("\n");
      }
    } else answer = await terminal.question(message, { signal });
  } else {
    if (!lines) {
      const reader = createLineReader({
        input: process.stdin,
        crlfDelay: Infinity,
        terminal: false,
      });
      lines = { reader, iterator: reader[Symbol.asyncIterator]() };
    }
    write(message);
    const next = await lines.iterator.next();
    if (next.done)
      throw new InputError("Login input ended before authorization completed.");
    answer = next.value;
  }
  if (secret) secrets.push(answer);
  return answer;
}

async function choose(label, choices, defaultID) {
  if (!isTerminal && !protocol) {
    throw new InputError(
      `${label} required. Available choices: ${choices.map((c) => c.id).join(", ")}. Use --provider/--method or run in a terminal.`,
    );
  }
  if (protocol) {
    emitEvent({
      type: "prompt",
      id: String(++promptSequence),
      kind: "select",
      message: label,
      options: choices,
    });
    const answer = await question("", false, controller.signal, false);
    if (!choices.some((c) => c.id === answer))
      throw new InputError("Choose one of the listed options.");
    return answer;
  }
  write(`${label}:\n`);
  choices.forEach((choice, index) =>
    write(`  ${index + 1}. ${choice.label ?? choice.id} (${choice.id})\n`),
  );
  while (true) {
    const input = (
      await question(defaultID ? `Select [${defaultID}]: ` : "Select: ")
    ).trim();
    const found = choices.find(
      (choice, index) => input === choice.id || input === String(index + 1),
    );
    if (found) return found.id;
    if (!input && defaultID) return defaultID;
    write("Choose a listed name or number.\n");
  }
}

function methods(provider) {
  const choices = [];
  if (provider.auth?.oauth?.login)
    choices.push({
      id: "oauth",
      label:
        provider.auth.oauth.loginLabel ??
        provider.auth.oauth.name ??
        "OAuth sign-in",
    });
  if (provider.auth?.apiKey?.login)
    choices.push({
      id: "api-key",
      label: provider.auth.apiKey.name ?? "API key",
    });
  return choices;
}
function view(provider) {
  return {
    id: provider.id,
    label: provider.name,
    methods: methods(provider).map((method) => method.id),
  };
}
function notify(notification) {
  if (protocol) {
    if (notification.type === "auth_url")
      emitEvent({
        type: "notice",
        message: notification.instructions,
        url: notification.url,
      });
    else if (notification.type === "device_code")
      emitEvent({
        type: "notice",
        url: notification.verificationUri,
        code: notification.userCode,
      });
    else emitEvent({ type: "message", message: notification.message ?? "" });
    return;
  }
  const event = notification;
  switch (event.type) {
    case "auth_url":
      write(
        `${event.instructions ?? "Open this URL in your browser:"}\n${event.url}\n`,
      );
      break;
    case "device_code":
      write(`Open ${event.verificationUri}\nEnter code: ${event.userCode}\n`);
      break;
    case "info":
      write(`${event.message}\n`);
      for (const link of event.links ?? []) write(`${link.url}\n`);
      break;
    case "progress":
      write(`${event.message}\n`);
      break;
    default:
      break;
  }
}
async function prompt(input) {
  const signal = input.signal
    ? AbortSignal.any([controller.signal, input.signal])
    : controller.signal;
  if (input.type === "select") return choose(input.message, input.options);
  return question(`${input.message} `, input.type === "secret", signal);
}

async function run() {
  const [major, minor] = process.versions.node.split(".").map(Number);
  if (major < 22 || (major === 22 && minor < 19))
    throw new InputError(
      `Pi requires Node 22.19 or newer; current Node is ${process.versions.node}. Check PATH.`,
    );
  const sdk = await import(pathToFileURL(join(root, "dist", "index.js")).href);
  if (!sdk.ModelRuntime?.create)
    throw new InputError(
      "This Pi SDK lacks ModelRuntime authentication. Update Pi with paxl harness upgrade pi.",
    );
  const runtime = await sdk.ModelRuntime.create({
    refreshOnCreate: false,
    allowModelNetwork: false,
    signal: controller.signal,
  });
  const providers = runtime.getProviders();
  const version = { runtime_version: String(sdk.VERSION ?? "") };
  if (operation === "methods") {
    result({
      state: "available",
      providers: providers.filter((p) => methods(p).length).map(view),
      ...version,
    });
    return;
  }
  const stored = await runtime.listCredentials({ signal: controller.signal });
  if (
    requestedProvider &&
    !runtime.getProvider?.(requestedProvider) &&
    !providers.some((p) => p.id === requestedProvider)
  ) {
    throw new InputError(
      `Unknown Pi provider ${requestedProvider}. Available providers: ${providers.map((p) => p.id).join(", ")}.`,
    );
  }
  if (operation === "status") {
    const selected = requestedProvider
      ? stored.filter((c) => c.providerId === requestedProvider)
      : stored;
    result({
      state: selected.length ? "configured" : "unknown",
      provider: requestedProvider || undefined,
      providers: selected.map((c) => ({
        id: c.providerId,
        credential_type: c.type,
        source: "stored",
      })),
      message:
        "Stored credential metadata only. Environment, helpers, expiry, and provider acceptance are not verified.",
      ...version,
    });
    return;
  }
  let providerID = requestedProvider;
  if (!providerID) {
    const candidates =
      operation === "logout"
        ? providers.filter((p) => stored.some((c) => c.providerId === p.id))
        : providers.filter((p) => methods(p).length);
    if (!candidates.length && operation === "logout") {
      result({
        state: "succeeded",
        message: "No stored Pi credentials to remove.",
        ...version,
      });
      return;
    }
    providerID = await choose(
      "Provider",
      candidates.map((p) => ({ id: p.id, label: p.name })),
    );
  }
  if (operation === "logout") {
    await runtime.logout(providerID, { signal: controller.signal });
    result({
      state: "succeeded",
      provider: providerID,
      message:
        "Removed the stored provider credential. Environment credentials may still apply.",
      ...version,
    });
    return;
  }
  const provider = providers.find((p) => p.id === providerID);
  const choices = methods(provider);
  if (!choices.length)
    throw new InputError(
      `${providerID} uses ambient credentials and has no interactive login method.`,
    );
  let method = requestedMethod;
  if (!method)
    method =
      choices.length === 1
        ? choices[0].id
        : await choose("Authentication method", choices, choices[0].id);
  if (!choices.some((c) => c.id === method))
    throw new InputError(
      `Unsupported method ${method}; supported methods for ${providerID}: ${choices.map((c) => c.id).join(", ")}.`,
    );
  await runtime.login(
    providerID,
    method === "api-key" ? "api_key" : "oauth",
    { signal: controller.signal, prompt, notify },
    {
      getDeviceId: () => {
        if (!sdk.SettingsManager?.create)
          throw new InputError(
            "The installed Pi SDK cannot provide its installation identity.",
          );
        return sdk.SettingsManager.create(process.cwd(), undefined, {
          projectTrusted: false,
        }).getOrCreateDeviceId();
      },
    },
  );
  result({ state: "succeeded", provider: providerID, method, ...version });
}
try {
  await run();
} catch (error) {
  const message =
    error instanceof InputError
      ? safe(error.message)
      : controller.signal.aborted
        ? "Login stopped."
        : "Pi authentication failed. Use Pi's /login for native diagnostics.";
  result({ state: "failed", message });
  process.exitCode = 1;
} finally {
  terminal?.close();
  lines?.reader.close();
}

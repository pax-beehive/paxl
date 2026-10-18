import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { join, dirname } from "node:path";
import { homedir } from "node:os";
import * as fs from "node:fs/promises";
import { createInterface } from "node:readline/promises";
import { createInterface as lineReader } from "node:readline";
import { Writable } from "node:stream";

const [
  harness,
  root,
  operation,
  requestedProvider,
  requestedMethod,
  secretMode,
] = process.argv.slice(1);
const controller = new AbortController();
process.on("SIGINT", () => controller.abort());
process.on("SIGTERM", () => controller.abort());
const originalOut = process.stdout.write.bind(process.stdout);
const originalErr = process.stderr.write.bind(process.stderr);
const secrets = [];
const safe = (value) => {
  let text = String(value ?? "");
  for (const key of secrets) if (key) text = text.replaceAll(key, "[REDACTED]");
  return text;
};
const write = (value) => originalErr(safe(value));
// Native SDK diagnostics must not corrupt the final JSON or reflect a supplied key.
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
  originalOut(JSON.stringify({ harness, ...value }) + "\n");
const requireInstalled = createRequire(join(root, "package.json"));
const load = (name) =>
  import(pathToFileURL(requireInstalled.resolve(name)).href);
class InputError extends Error {}
let terminal,
  lines,
  muted = false;
const isTerminal = Boolean(process.stdin.isTTY && process.stderr.isTTY);
async function question(message, secret = false, signal = controller.signal) {
  signal.throwIfAborted();
  if (secretMode === "true" && !secret)
    throw new InputError(
      "This provider needs additional prompts. Run interactively without --secret-stdin.",
    );
  let answer;
  if (isTerminal) {
    if (!terminal) {
      const output = new Writable({
        write(chunk, _encoding, done) {
          if (!muted) write(chunk);
          done();
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
      const reader = lineReader({
        input: process.stdin,
        crlfDelay: Infinity,
        terminal: false,
      });
      lines = { reader, iterator: reader[Symbol.asyncIterator]() };
    }
    write(message);
    let onAbort;
    const aborted = new Promise((_, reject) => {
      onAbort = () => reject(new InputError("Authentication cancelled."));
      signal.addEventListener("abort", onAbort, { once: true });
    });
    let read;
    try {
      read = await Promise.race([lines.iterator.next(), aborted]);
    } finally {
      signal.removeEventListener("abort", onAbort);
    }
    if (read.done) throw new InputError("Authentication input ended.");
    answer = read.value;
  }
  if (secret) {
    if (!answer || answer.length > 4096 || answer.trim() !== answer)
      throw new InputError(
        "Expected one nonempty secret line of at most 4096 bytes.",
      );
    secrets.push(answer);
  }
  return answer;
}
async function choose(label, choices) {
  if (!isTerminal)
    throw new InputError(
      `${label} required. Available choices: ${choices.map((c) => c.id).join(", ")}. Use --provider/--method or run in a terminal.`,
    );
  write(`${label}:\n`);
  choices.forEach((c, i) =>
    write(`  ${i + 1}. ${c.label ?? c.id} (${c.id})\n`),
  );
  while (true) {
    const text = (await question("Select: ")).trim();
    const found = choices.find(
      (c, i) => c.id === text || String(i + 1) === text,
    );
    if (found) return found.id;
    write("Choose a listed name or number.\n");
  }
}
function notice(value) {
  if (value.message) write(value.message + "\n");
  if (value.url) write(value.url + "\n");
  if (value.code) write("Enter code in browser: " + value.code + "\n");
}
async function nativePrompt(value) {
  const signal = value.signal
    ? AbortSignal.any([controller.signal, value.signal])
    : controller.signal;
  if (value.kind === "select") return choose(value.message, value.options);
  return question(value.message + " ", value.kind === "secret", signal);
}
const metadata =
  "Stored native credential metadata only; environment credentials, expiry and provider access are not verified.";
async function finish(run) {
  try {
    await run();
  } catch (error) {
    result({
      state: "failed",
      message:
        error instanceof InputError
          ? safe(error.message)
          : controller.signal.aborted
            ? "Authentication cancelled."
            : "Native SDK authentication failed. Check the installation and retry.",
    });
    process.exitCode = 1;
  } finally {
    terminal?.close();
    lines?.reader.close();
  }
}

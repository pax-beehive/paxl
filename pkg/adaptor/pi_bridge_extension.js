import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const EXTENSION_NAME = "paxl-pi-bridge";

export default function (pi) {
  let server = null;
  let socketPath = "";
  let recordPath = "";

  const closeBridge = () => {
    if (server) {
      try {
        server.close();
      } catch {
        // Ignore close races during session replacement.
      }
      server = null;
    }
    for (const file of [socketPath, recordPath]) {
      if (!file) continue;
      try {
        fs.rmSync(file, { force: true });
      } catch {
        // Stale bridge files are cleaned up best-effort.
      }
    }
    socketPath = "";
    recordPath = "";
  };

  pi.on("session_start", async (_event, ctx) => {
    closeBridge();
    const sessionId = ctx.sessionManager.getSessionId();
    if (!sessionId) return;

    const root = bridgeRoot();
    const socketDir = path.join(root, "sockets");
    const sessionDir = path.join(root, "sessions");
    fs.mkdirSync(socketDir, { recursive: true, mode: 0o700 });
    fs.mkdirSync(sessionDir, { recursive: true, mode: 0o700 });

    const safeID = safeFileName(sessionId);
    socketPath = path.join(socketDir, `${safeID}.sock`);
    recordPath = path.join(sessionDir, `${safeID}.json`);
    fs.rmSync(socketPath, { force: true });

    server = net.createServer((conn) => {
      let buffered = "";
      conn.setEncoding("utf8");
      conn.on("data", (chunk) => {
        buffered += chunk;
        let index = buffered.indexOf("\n");
        while (index !== -1) {
          const line = buffered.slice(0, index).trim();
          buffered = buffered.slice(index + 1);
          if (line) handleLine(pi, ctx, conn, line);
          index = buffered.indexOf("\n");
        }
      });
    });
    server.on("error", () => {});
    server.listen(socketPath, () => {
      try {
        fs.chmodSync(socketPath, 0o600);
      } catch {}
      writeRecord(ctx, sessionId, socketPath, recordPath);
      ctx.ui.setStatus(EXTENSION_NAME, "paxl bridge active");
    });
  });

  pi.on("session_shutdown", async () => {
    closeBridge();
  });
}

function handleLine(pi, ctx, conn, line) {
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    writeError(conn, null, -32700, "Parse error");
    return;
  }

  const id = request.id ?? null;
  const method = request.method;
  const params = request.params ?? {};
  try {
    switch (method) {
      case "initialize":
        writeResult(conn, id, {
          protocolVersion: 1,
          agentInfo: { name: EXTENSION_NAME, title: "paxl Pi active bridge", version: "0.1.0" },
          authMethods: [],
          agentCapabilities: {
            loadSession: false,
            mcpCapabilities: { http: false, sse: false },
            promptCapabilities: { image: false, audio: false, embeddedContext: false },
            sessionCapabilities: { list: {} },
          },
        });
        break;
      case "authenticate":
        writeResult(conn, id, {});
        break;
      case "session/list":
        writeResult(conn, id, {
          sessions: [sessionInfo(ctx)],
          nextCursor: null,
          _meta: {},
        });
        break;
      case "session/prompt":
        handlePrompt(pi, ctx, conn, id, params);
        break;
      default:
        writeError(conn, id, -32601, `Unknown method: ${method}`);
    }
  } catch (error) {
    writeError(conn, id, -32000, error instanceof Error ? error.message : String(error));
  }
}

function handlePrompt(pi, ctx, conn, id, params) {
  const expectedSessionID = ctx.sessionManager.getSessionId();
  if (params.sessionId !== expectedSessionID) {
    writeError(conn, id, -32602, `Session not active: ${params.sessionId}`);
    return;
  }
  const text = promptText(params.prompt);
  if (!text) {
    writeError(conn, id, -32602, "Prompt text is required");
    return;
  }
  const delivery = params.delivery === "followUp" ? "followUp" : "steer";
  pi.sendUserMessage(text, { deliverAs: delivery });
  writeRecord(ctx, expectedSessionID);
  writeResult(conn, id, {
    delivery: delivery === "followUp" ? "pi_extension_follow_up" : "pi_extension_steer",
  });
}

function promptText(prompt) {
  if (typeof prompt === "string") return prompt;
  if (!Array.isArray(prompt)) return "";
  return prompt
    .map((part) => {
      if (typeof part === "string") return part;
      if (part && part.type === "text" && typeof part.text === "string") return part.text;
      return "";
    })
    .filter(Boolean)
    .join("\n");
}

function sessionInfo(ctx) {
  const sessionId = ctx.sessionManager.getSessionId();
  return {
    sessionId,
    cwd: ctx.cwd || ctx.sessionManager.getCwd(),
    title: ctx.sessionManager.getSessionName() || sessionId,
    updatedAt: new Date().toISOString(),
    status: ctx.isIdle() ? "idle" : "running",
  };
}

function writeRecord(ctx, sessionId, currentSocketPath, currentRecordPath) {
  const targetRecordPath = currentRecordPath || recordPath;
  const targetSocketPath = currentSocketPath || socketPath;
  if (!targetRecordPath || !targetSocketPath) return;
  const record = {
    session_id: sessionId,
    cwd: ctx.cwd || ctx.sessionManager.getCwd(),
    title: ctx.sessionManager.getSessionName() || sessionId,
    socket_path: targetSocketPath,
    pid: process.pid,
    updated_at: new Date().toISOString(),
  };
  fs.writeFileSync(targetRecordPath, JSON.stringify(record, null, 2) + "\n", { mode: 0o600 });
}

function writeResult(conn, id, result) {
  conn.write(JSON.stringify({ jsonrpc: "2.0", id, result }) + "\n");
}

function writeError(conn, id, code, message) {
  conn.write(JSON.stringify({ jsonrpc: "2.0", id, error: { code, message } }) + "\n");
}

function bridgeRoot() {
  if (process.env.PAXL_PI_BRIDGE_DIR) return process.env.PAXL_PI_BRIDGE_DIR;
  const agentDir = process.env.PI_CODING_AGENT_DIR || path.join(os.homedir(), ".pi", "agent");
  return path.join(agentDir, "paxl-bridge");
}

function safeFileName(value) {
  return String(value).replace(/[^A-Za-z0-9._-]/g, "_");
}

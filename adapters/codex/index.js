import { spawn } from "node:child_process";

export const name = "baron-codex-adapter";
export const version = "0.1.0";
// The bridge forwards each event to the canonical `baron hook codex <event>` entrypoint.
export const CODEX_EVENTS = Object.freeze([
  "SessionStart",
  "UserPromptSubmit",
  "PreToolUse",
  "PostToolUse",
  "PreCompact",
  "PostCompact",
  "Stop",
  "SessionEnd",
]);

const EVENT_NAMES = new Set(CODEX_EVENTS);

export function normalizeEvent(event) {
  if (typeof event !== "string" || !EVENT_NAMES.has(event)) {
    throw new Error(`unsupported Codex event ${String(event)}`);
  }
  return event;
}

export function createCodexAdapter(options = {}) {
  const binary = options.baronBinary ?? process.env.BARON_BINARY ?? "baron";
  const cwd = options.cwd ?? process.cwd();
  const timeoutMs = options.timeoutMs ?? 5000;
  return {
    name,
    version,
    onEvent: (event, payload) => invoke(binary, cwd, timeoutMs, normalizeEvent(event), payload),
    onSessionStart: (payload) => invoke(binary, cwd, timeoutMs, "SessionStart", payload),
    onUserPrompt: (payload) => invoke(binary, cwd, timeoutMs, "UserPromptSubmit", payload),
    onPreToolUse: (payload) => invoke(binary, cwd, timeoutMs, "PreToolUse", payload),
    onPostToolUse: (payload) => invoke(binary, cwd, timeoutMs, "PostToolUse", payload),
    onPreCompact: (payload) => invoke(binary, cwd, timeoutMs, "PreCompact", payload),
    onPostCompact: (payload) => invoke(binary, cwd, timeoutMs, "PostCompact", payload),
    onStop: (payload) => invoke(binary, cwd, timeoutMs, "Stop", payload),
    onSessionEnd: (payload) => invoke(binary, cwd, timeoutMs, "SessionEnd", payload),
  };
}

function invoke(binary, cwd, timeoutMs, event, payload) {
  return new Promise((resolve) => {
    const child = spawn(binary, ["hook", "codex", event], {
      cwd,
      stdio: ["pipe", "pipe", "ignore"],
    });
    let output = "";
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(value);
    };
    const timer = setTimeout(() => {
      child.kill();
      finish({ ok: false, error: "Baron Codex adapter timeout" });
    }, timeoutMs);
    child.stdout.on("data", (chunk) => {
      if (output.length < 65536) output += chunk.toString("utf8").slice(0, 65536 - output.length);
    });
    child.on("error", (error) => finish({ ok: false, error: error.message }));
    child.on("close", () => {
      try {
        finish(JSON.parse(output));
      } catch {
        finish({ ok: false, error: "invalid Baron Codex adapter response" });
      }
    });
    child.stdin.end(JSON.stringify({ payload }));
  });
}

if (process.argv[1] && process.argv[1].endsWith("index.js") && process.argv[2]) {
  const event = normalizeEvent(process.argv[2]);
  let input = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { input += chunk; });
  process.stdin.on("end", async () => {
    let payload = {};
    try { payload = input.trim() ? JSON.parse(input) : {}; } catch { payload = { raw_input: input }; }
    const response = await createCodexAdapter().onEvent(event, payload);
    process.stdout.write(JSON.stringify(response) + "\n");
  });
}

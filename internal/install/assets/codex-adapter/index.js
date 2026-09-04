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
  "task_started",
  "task_updated",
  "task_failed",
  "task_blocked",
  "task_verified",
  "task_completed",
  "task_interrupted",
]);
export const TASK_FIELDS = Object.freeze(["task_id", "active_task_id", "completion_policy", "verification_ref", "verification_kind", "verification_scope"]);

const EVENT_NAMES = new Set(CODEX_EVENTS);

export function normalizeEvent(event) {
  if (typeof event !== "string" || !EVENT_NAMES.has(event)) {
    throw new Error(`unsupported Codex event ${String(event)}`);
  }
  return event;
}

export function detectPentestRequest(payload) {
  const prompt = promptFromPayload(payload);
  if (prompt === "") return null;
  const normalized = normalizePrompt(prompt);
  if (!/(^|\s)(pentest|penetration\s+test|security\s+test|kiem\s+thu\s+bao\s+mat|kiem\s+tra\s+bao\s+mat)(\s|$)/i.test(normalized)) return null;
  const mode = /(^|\s|-)(deep|deep\s+scan|sau)(\s|$)/i.test(normalized) ? "deep" :
    /(^|\s|-)(normal|standard|thuong)(\s|$)/i.test(normalized) ? "normal" : "";
  return { mode, prompt };
}

export function createPentestDirective(payload) {
  const request = detectPentestRequest(payload);
  if (request === null) return "";
  if (request.mode === "") {
    return "[Baron pentest directive] The user requested a pentest but did not choose normal or deep. Ask which mode to run before starting; do not launch Strix from a lifecycle hook.";
  }
  return `[Baron pentest directive] The user explicitly requested a ${request.mode} pentest. Run baron pentest --${request.mode} now, read the canonical report, and remediate only through the active agent after Baron records the pre-fix checkpoint. Strix is report-only and must not modify the real source tree. Test and retest findings, report the result, and do not commit, push, deploy, or publish.`;
}

export function createCodexAdapter(options = {}) {
  const binary = options.baronBinary ?? process.env.BARON_BINARY ?? "baron";
  const cwd = options.cwd ?? process.cwd();
  const timeoutMs = options.timeoutMs ?? 5000;
  return {
    name,
    version,
    onEvent: (event, payload) => invoke(binary, cwd, timeoutMs, normalizeEvent(event), normalizeTaskPayload(payload)),
    onSessionStart: (payload) => invoke(binary, cwd, timeoutMs, "SessionStart", normalizeTaskPayload(payload)),
    onUserPrompt: async (payload) => addPentestDirective(await invoke(binary, cwd, timeoutMs, "UserPromptSubmit", normalizeTaskPayload(payload)), payload),
    onPreToolUse: (payload) => invoke(binary, cwd, timeoutMs, "PreToolUse", normalizeTaskPayload(payload)),
    onPostToolUse: (payload) => invoke(binary, cwd, timeoutMs, "PostToolUse", normalizeTaskPayload(payload)),
    onPreCompact: (payload) => invoke(binary, cwd, timeoutMs, "PreCompact", normalizeTaskPayload(payload)),
    onPostCompact: (payload) => invoke(binary, cwd, timeoutMs, "PostCompact", normalizeTaskPayload(payload)),
    onStop: (payload) => invoke(binary, cwd, timeoutMs, "Stop", normalizeTaskPayload(payload)),
    onSessionEnd: (payload) => invoke(binary, cwd, timeoutMs, "SessionEnd", normalizeTaskPayload(payload)),
    onTaskStarted: (payload) => invoke(binary, cwd, timeoutMs, "task_started", normalizeTaskPayload(payload)),
    onTaskUpdated: (payload) => invoke(binary, cwd, timeoutMs, "task_updated", normalizeTaskPayload(payload)),
    onTaskFailed: (payload) => invoke(binary, cwd, timeoutMs, "task_failed", normalizeTaskPayload(payload)),
    onTaskBlocked: (payload) => invoke(binary, cwd, timeoutMs, "task_blocked", normalizeTaskPayload(payload)),
    onTaskVerified: (payload) => invoke(binary, cwd, timeoutMs, "task_verified", normalizeTaskPayload(payload)),
    onTaskCompleted: (payload) => invoke(binary, cwd, timeoutMs, "task_completed", normalizeTaskPayload(payload)),
    onTaskInterrupted: (payload) => invoke(binary, cwd, timeoutMs, "task_interrupted", normalizeTaskPayload(payload)),
  };
}

function addPentestDirective(response, payload) {
  const directive = createPentestDirective(payload);
  if (directive === "") return response;
  const result = response && typeof response === "object" && !Array.isArray(response) ? { ...response } : {};
  const hookOutput = result.hookSpecificOutput && typeof result.hookSpecificOutput === "object" && !Array.isArray(result.hookSpecificOutput)
    ? { ...result.hookSpecificOutput } : { hookEventName: "UserPromptSubmit" };
  const existing = typeof hookOutput.additionalContext === "string" ? hookOutput.additionalContext.trim() : "";
  hookOutput.additionalContext = existing === "" ? directive : `${existing}\n${directive}`;
  result.hookSpecificOutput = hookOutput;
  if (result.continue === undefined) result.continue = true;
  return result;
}

function promptFromPayload(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return "";
  for (const key of ["prompt", "user_prompt", "userPrompt", "text", "message"]) {
    if (typeof payload[key] === "string" && payload[key].trim() !== "") return payload[key].trim();
  }
  const messages = Array.isArray(payload.messages) ? payload.messages : [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message || typeof message !== "object" || message.role !== "user") continue;
    if (typeof message.content === "string" && message.content.trim() !== "") return message.content.trim();
    if (Array.isArray(message.content)) {
      const block = message.content.find((item) => item && typeof item.text === "string" && item.text.trim() !== "");
      if (block) return block.text.trim();
    }
  }
  return "";
}

function normalizePrompt(value) {
  return value.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase().replace(/[—–]/g, "-");
}

function normalizeTaskPayload(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return payload;
  const root = { ...payload };
  const task = root.task && typeof root.task === "object" && !Array.isArray(root.task) ? root.task : {};
  if (!root.task_id && (root.taskId || task.task_id || task.id)) root.task_id = root.taskId || task.task_id || task.id;
  if (!root.active_task_id && root.activeTaskId) root.active_task_id = root.activeTaskId;
  for (const field of ["completion_policy", "verification_ref", "verification_kind", "verification_scope"]) {
    if (root[field] === undefined && task[field] !== undefined) root[field] = task[field];
  }
  return root;
}

function invoke(binary, cwd, timeoutMs, event, payload) {
  return new Promise((resolve) => {
    const child = spawnBaron(binary, ["hook", "codex", event], {
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

function spawnBaron(binary, args, options) {
  if (process.platform === "win32" && /\.(cmd|bat)$/i.test(binary)) {
    return spawn(process.env.ComSpec || "cmd.exe", ["/d", "/c", binary, ...args], options);
  }
  return spawn(binary, args, options);
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

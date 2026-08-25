import { spawn } from "node:child_process";

export type CodexEvent =
  | "SessionStart"
  | "UserPromptSubmit"
  | "PreToolUse"
  | "PostToolUse"
  | "PreCompact"
  | "PostCompact"
  | "Stop"
  | "SessionEnd";

export interface CodexAdapterOptions {
  baronBinary?: string;
  cwd?: string;
  timeoutMs?: number;
}

export const CODEX_ADAPTER_VERSION = "0.1.0";
export const CODEX_EVENTS: readonly CodexEvent[] = [
  "SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
  "PreCompact", "PostCompact", "Stop", "SessionEnd",
];

export function normalizeEvent(event: string): CodexEvent {
  if (!(CODEX_EVENTS as readonly string[]).includes(event)) {
    throw new Error(`unsupported Codex event ${event}`);
  }
  return event as CodexEvent;
}

export function createCodexAdapter(options: CodexAdapterOptions = {}) {
  const binary = options.baronBinary ?? process.env.BARON_BINARY ?? "baron";
  const cwd = options.cwd ?? process.cwd();
  const timeoutMs = options.timeoutMs ?? 5000;
  return {
    name: "baron-codex-adapter",
    version: CODEX_ADAPTER_VERSION,
    onEvent: (event: string, payload: unknown) => invoke(binary, cwd, timeoutMs, normalizeEvent(event), payload),
  };
}

function invoke(binary: string, cwd: string, timeoutMs: number, event: CodexEvent, payload: unknown): Promise<unknown> {
  return new Promise((resolve) => {
    const child = spawn(binary, ["hook", "codex", event], { cwd, stdio: ["pipe", "pipe", "ignore"] });
    let output = "";
    const timer = setTimeout(() => {
      child.kill();
      resolve({ ok: false, error: "Baron Codex adapter timeout" });
    }, timeoutMs);
    child.stdout.on("data", (chunk: Buffer) => { output += chunk.toString("utf8").slice(0, 65536 - output.length); });
    child.on("error", (error) => { clearTimeout(timer); resolve({ ok: false, error: error.message }); });
    child.on("close", () => {
      clearTimeout(timer);
      try { resolve(JSON.parse(output)); } catch { resolve({ ok: false, error: "invalid Baron Codex adapter response" }); }
    });
    child.stdin.end(JSON.stringify({ payload }));
  });
}

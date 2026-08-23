/**
 * Minimal DSH boundary. Baron business logic remains in the Go binary; this
 * adapter only translates lifecycle callbacks into canonical hook invocations.
 */
import { spawn } from "node:child_process";

export type BaronDSHEvent =
  | "session_started"
  | "user_prompt"
  | "tool_started"
  | "tool_finished"
  | "file_changed"
  | "test_started"
  | "test_finished"
  | "checkpoint_updated"
  | "assistant_final"
  | "session_clean_closed";

export interface BaronAdapterOptions {
  baronBinary?: string;
  cwd?: string;
  timeoutMs?: number;
}

export const BARON_DSH_ADAPTER_VERSION = "0.1.0";
export const SUPPORTED_DSH_VERSIONS = ["0.1.1-rc.2"] as const;
export const name = "baron-dsh-adapter";

export function assertSupportedDshVersion(version: string): void {
  if (!SUPPORTED_DSH_VERSIONS.includes(version as (typeof SUPPORTED_DSH_VERSIONS)[number])) {
    throw new Error(`unsupported dsh version ${version}; supported versions: ${SUPPORTED_DSH_VERSIONS.join(", ")}`);
  }
}

// DSH's native Cordis boundary. The adapter observes lifecycle events and
// delegates all durable work to the short-lived Go hook process.
export function apply(ctx: { on: (event: string, handler: (...args: any[]) => any) => void }): void {
  const adapter = createBaronAdapter();
  ctx.on("agent/session-start", (payload: unknown) => adapter.onSessionStart(payload));
  ctx.on("agent/pre-step", (payload: unknown) => adapter.onPreStep(payload));
  ctx.on("tools/pre-execute", async (execution: unknown, next?: () => unknown) => {
    await adapter.onToolStarted(execution);
    return next ? next() : undefined;
  });
  ctx.on("tools/post-execute", (payload: unknown) => adapter.onToolFinished(payload));
  ctx.on("session/event", (payload: unknown) => adapter.onSessionEvent(payload));
  ctx.on("agent/turn-stopping", (payload: unknown) => adapter.onTurnStopping(payload));
  ctx.on("session/flush", (payload: unknown) => adapter.onFlush(payload));
}

export interface BaronDSHAdapter {
  name: string;
  version: string;
  onSessionStart(payload?: unknown): Promise<unknown>;
  onPreStep(payload?: unknown): Promise<unknown>;
  onToolStarted(payload?: unknown): Promise<unknown>;
  onToolFinished(payload?: unknown): Promise<unknown>;
  onFileChanged(payload?: unknown): Promise<unknown>;
  onSessionEvent(payload?: unknown): Promise<unknown>;
  onTurnStopping(payload?: unknown): Promise<unknown>;
  onFlush(payload?: unknown): Promise<unknown>;
}

export function createBaronAdapter(options: BaronAdapterOptions = {}): BaronDSHAdapter {
  const binary = options.baronBinary ?? "baron";
  const cwd = options.cwd ?? process.cwd();
  const timeoutMs = options.timeoutMs ?? 5000;
  const emit = (event: BaronDSHEvent, payload: unknown) => invoke(binary, cwd, timeoutMs, event, payload);
  return {
    name: "baron-dsh-adapter",
    version: BARON_DSH_ADAPTER_VERSION,
    onSessionStart: (payload) => emit("session_started", payload),
    onPreStep: (payload) => emit("checkpoint_updated", payload),
    onToolStarted: (payload) => emit("tool_started", payload),
    onToolFinished: (payload) => emit("tool_finished", payload),
    onFileChanged: (payload) => emit("file_changed", payload),
    onSessionEvent: (payload) => emit("checkpoint_updated", payload),
    onTurnStopping: (payload) => emit("assistant_final", payload),
    onFlush: (payload) => emit("session_clean_closed", payload),
  };
}

function invoke(binary: string, cwd: string, timeoutMs: number, event: BaronDSHEvent, payload: unknown): Promise<unknown> {
  return new Promise((resolve) => {
    const child = spawn(binary, ["hook", "dsh", event], { cwd, stdio: ["pipe", "pipe", "ignore"] });
    let output = "";
    const timer = setTimeout(() => {
      child.kill();
      resolve({ ok: false, error: "Baron hook timeout" });
    }, timeoutMs);
    child.stdout.on("data", (chunk: Buffer) => { output += chunk.toString("utf8"); });
    child.on("error", (error) => {
      clearTimeout(timer);
      resolve({ ok: false, error: error.message });
    });
    child.on("close", () => {
      clearTimeout(timer);
      try { resolve(JSON.parse(output)); } catch { resolve({ ok: false, error: "invalid Baron hook response" }); }
    });
    child.stdin.end(JSON.stringify({ payload }));
  });
}

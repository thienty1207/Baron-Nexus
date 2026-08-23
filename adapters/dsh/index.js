import { spawn } from "node:child_process";

export const name = "baron-dsh-adapter";

export function apply(ctx) {
  const binary = process.env.BARON_BINARY || "baron";
  const invoke = (event, payload) => new Promise((resolve) => {
    const child = spawn(binary, ["hook", "dsh", event], {
      cwd: process.cwd(),
      stdio: ["pipe", "pipe", "ignore"],
    });
    let output = "";
    const timer = setTimeout(() => {
      child.kill();
      resolve({ ok: false, error: "Baron hook timeout" });
    }, 5000);
    child.stdout.on("data", (chunk) => { output += chunk.toString("utf8"); });
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
  ctx.on("agent/session-start", (payload) => invoke("session_started", payload));
  ctx.on("agent/pre-step", (payload) => invoke("checkpoint_updated", payload));
  ctx.on("tools/pre-execute", async (execution, next) => {
    await invoke("tool_started", execution);
    return next();
  });
  ctx.on("tools/post-execute", (payload) => invoke("tool_finished", payload));
  ctx.on("session/event", (payload) => invoke("checkpoint_updated", payload));
  ctx.on("agent/turn-stopping", (payload) => invoke("assistant_final", payload));
  ctx.on("session/flush", (payload) => invoke("session_clean_closed", payload));
}

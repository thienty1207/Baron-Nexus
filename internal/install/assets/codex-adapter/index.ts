export type CodexEvent = "SessionStart" | "UserPromptSubmit" | "PreToolUse" | "PostToolUse" | "PreCompact" | "PostCompact" | "Stop" | "SessionEnd" | "task_started" | "task_updated" | "task_failed" | "task_blocked" | "task_verified" | "task_completed" | "task_interrupted";
export const CODEX_ADAPTER_VERSION = "0.1.0";
export const TASK_FIELDS = ["task_id", "active_task_id", "completion_policy", "verification_ref", "verification_kind", "verification_scope"] as const;
export const CODEX_EVENTS: readonly CodexEvent[] = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "PostCompact", "Stop", "SessionEnd", "task_started", "task_updated", "task_failed", "task_blocked", "task_verified", "task_completed", "task_interrupted"];
export function normalizeEvent(event: string): CodexEvent {
  if (!(CODEX_EVENTS as readonly string[]).includes(event)) throw new Error(`unsupported Codex event ${event}`);
  return event as CodexEvent;
}

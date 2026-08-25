export type CodexEvent = "SessionStart" | "UserPromptSubmit" | "PreToolUse" | "PostToolUse" | "PreCompact" | "PostCompact" | "Stop" | "SessionEnd";
export const CODEX_ADAPTER_VERSION = "0.1.0";
export const CODEX_EVENTS: readonly CodexEvent[] = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "PostCompact", "Stop", "SessionEnd"];
export function normalizeEvent(event: string): CodexEvent {
  if (!(CODEX_EVENTS as readonly string[]).includes(event)) throw new Error(`unsupported Codex event ${event}`);
  return event as CodexEvent;
}

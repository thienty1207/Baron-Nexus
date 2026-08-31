/**
 * Minimal DSH boundary. Baron business logic remains in the Go binary; this
 * adapter only translates lifecycle callbacks into canonical hook invocations.
 */
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";

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
export const SUPPORTED_DSH_VERSIONS = ["latest"] as const;
export const name = "baron-dsh-adapter";

export function assertSupportedDshVersion(version: string): void {
  if (!/^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version.trim())) {
    throw new Error(`unsupported dsh version ${version}; Baron supports the latest semantic DSH release at run time`);
  }
}

// DSH's native Cordis boundary. The adapter observes lifecycle events and
// delegates all durable work to the short-lived Go hook process.
export function apply(ctx: { on: (event: string, handler: (...args: any[]) => any, options?: unknown) => void }): void {
  const adapter = createBaronAdapter();
  ctx.on("agent/session-start", (payload: unknown) => adapter.onSessionStart(payload));
  ctx.on("agent/pre-step", (payload: unknown, next: () => Promise<unknown>) => adapter.onPreStep(payload, next), { prepend: true });
  ctx.on("tools/pre-execute", async (execution: unknown, next?: () => unknown) => {
    await adapter.onToolStarted(execution);
    return next ? next() : undefined;
  });
  ctx.on("tools/result", async (execution: unknown, result: unknown) => {
    return adapter.onToolFinished({ execution, result });
  });
  ctx.on("session/event", (session: unknown, event: unknown) => adapter.onSessionEvent({ session, event }));
  ctx.on("agent/turn-stopping", (payload: unknown) => adapter.onTurnStopping(payload));
  ctx.on("session/flush", (payload: unknown) => adapter.onFlush(payload));
}

export interface BaronDSHAdapter {
  name: string;
  version: string;
  onSessionStart(payload?: unknown): Promise<unknown>;
  onPreStep(payload?: unknown, next?: () => Promise<unknown>): Promise<unknown>;
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
  const capturedToolCalls = new Set<string>();
  const startedTools = new Map<string, Record<string, unknown>>();
  const onToolFinished = (payload?: unknown): Promise<unknown> => {
    const callID = toolCallID(payload);
    if (callID !== "") {
      if (capturedToolCalls.has(callID)) return Promise.resolve({ ok: true, duplicate: true });
      capturedToolCalls.add(callID);
    }
    return emit("tool_finished", lifecyclePayload("tool_finished", payload));
  };
  return {
    name: "baron-dsh-adapter",
    version: BARON_DSH_ADAPTER_VERSION,
    onSessionStart: (payload) => emit("session_started", lifecyclePayload("session_started", payload)),
    onPreStep: async (payload, next) => {
      const response = await emit("checkpoint_updated", lifecyclePayload("checkpoint_updated", payload));
      const toolEvidence = latestToolEvidence(payload);
      if (toolEvidence !== undefined) await onToolFinished(toolEvidence);
      const decision = next ? await next() : { kind: "enter", messages: [] };
      return addHistoricalContext(decision, response);
    },
    onToolStarted: async (payload) => {
      const execution = toolExecutionRecord(payload);
      if (execution.callId) startedTools.set(execution.callId, execution);
      return emit("tool_started", lifecyclePayload("tool_started", payload));
    },
    onToolFinished,
    onFileChanged: (payload) => emit("file_changed", lifecyclePayload("file_changed", payload)),
    onSessionEvent: async (payload) => {
      const toolResult = toolResultFromSessionEvent(payload);
      if (toolResult !== undefined) {
        const execution = startedTools.get(toolResult.callID) ?? { callId: toolResult.callID };
        await onToolFinished({ session: isRecord(payload) ? payload.session : undefined, execution, result: toolResult.result });
        startedTools.delete(toolResult.callID);
      }
      return emit("checkpoint_updated", lifecyclePayload("checkpoint_updated", payload));
    },
    onTurnStopping: (payload) => {
      const root: Record<string, unknown> = isRecord(payload) ? { ...payload } : {};
      if (!isRecord(root.execution)) {
        const execution = latestToolExecution(root) ?? lastStartedTool(startedTools);
        if (execution !== undefined) root.execution = execution;
      }
      const response = emit("assistant_final", lifecyclePayload("assistant_final", root));
      startedTools.clear();
      return response;
    },
    onFlush: (payload) => emit("session_clean_closed", lifecyclePayload("session_clean_closed", payload)),
  };
}

type HookResponse = {
  context?: string;
  [key: string]: unknown;
};

function invoke(binary: string, cwd: string, timeoutMs: number, event: BaronDSHEvent, payload: unknown): Promise<HookResponse> {
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
      try { resolve(JSON.parse(output) as HookResponse); } catch { resolve({ ok: false, error: "invalid Baron hook response" }); }
    });
    try {
      child.stdin.end(JSON.stringify({ payload }));
    } catch (error) {
      clearTimeout(timer);
      child.kill();
      resolve({ ok: false, error: error instanceof Error ? error.message : "cannot serialize DSH lifecycle payload" });
    }
  });
}

function addHistoricalContext(decision: unknown, response: HookResponse): unknown {
  if (!isRecord(decision) || decision.kind !== "enter" || !Array.isArray(decision.messages)) return decision;
  const context = typeof response.context === "string" ? response.context.trim() : "";
  if (context === "") return decision;
  return {
    kind: "enter",
    messages: [...decision.messages, createRecallMessage(context)],
  };
}

function createRecallMessage(text: string): Record<string, unknown> {
  return {
    id: `baron-context-${randomUUID()}`,
    role: "user",
    source: { kind: "plugin", plugin: name, form: "recall", version: 1 },
    content: [{ type: "text", text }],
  };
}

function lifecyclePayload(event: BaronDSHEvent, payload: unknown): Record<string, unknown> {
  const root = isRecord(payload) ? payload : {};
  const result: Record<string, unknown> = {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  const sessionID = firstString(agent.id, session.id, root.session_id, root.sessionId, root.id);
  if (sessionID !== "") result.session_id = sessionID;
  const cwd = firstString(session.header && isRecord(session.header) ? session.header.cwd : undefined, root.cwd);
  if (cwd !== "") result.cwd = cwd;
  for (const key of ["source", "turn", "step", "goal", "current_step", "next_action", "task_status", "completion_verified", "status", "command", "file", "symbol", "test", "exit_code", "class", "prompt", "text", "message", "response", "last_assistant_message", "summary", "decision", "content", "tool_output", "tool_response"]) {
    const value = root[key];
    if (isSafeScalar(value)) result[key] = value;
  }
  const prompt = latestUserText(root);
  if (prompt !== "") result.prompt = prompt;
  const execution = isRecord(root.execution) ? root.execution : root;
  const executionArguments = isRecord(execution.arguments) ? execution.arguments : {};
  const executionCommand = firstString(executionArguments.command, executionArguments.cmd, execution.command, execution.tool, execution.name, execution.tool_name);
  if (executionCommand !== "") result.command = executionCommand;
  const executionFile = firstString(execution.file, execution.path);
  if (executionFile !== "") result.file = executionFile;
  const toolResult = isRecord(root.result) ? root.result : {};
  const toolError = isRecord(toolResult.error) ? toolResult.error : {};
  const resultText = firstString(root.summary, root.text, root.response, toolResult.summary, toolResult.text, toolError.message, textFromContent(toolResult.content));
  if (resultText !== "") {
    if (event === "assistant_final") result.response = resultText;
    else result.summary = resultText;
  }
  if (toolResult.isError === true) {
    result.status = "failed";
    result.class = "tool_error";
  }
  const exitCode = exitCodeFromText(resultText);
  if (exitCode !== undefined) {
    result.exit_code = exitCode;
    if (exitCode !== 0) result.status = "failed";
  }
  if (event === "assistant_final") {
    const assistantText = latestAssistantText(agent);
    if (assistantText !== "") {
      result.response = assistantText;
      result.summary = assistantText;
    }
  }
  const assistantExitCode = exitCodeFromText(firstString(result.response, result.summary));
  if (event === "assistant_final" && assistantExitCode !== undefined) {
    result.exit_code = assistantExitCode;
    if (assistantExitCode !== 0) {
      result.status = "failed";
      result.class = "tool_error";
    }
  }
  if (event === "checkpoint_updated" && isRecord(root.event)) {
    const eventText = textFromContent(isRecord(root.event).data);
    if (eventText !== "") result.summary = eventText;
  }
  return result;
}

function latestToolEvidence(payload: unknown): Record<string, unknown> | undefined {
  const root = isRecord(payload) ? payload : {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  let messages: unknown[] = [];
  if (typeof session.deriveMessages === "function") {
    try {
      const derived = session.deriveMessages.call(session);
      if (Array.isArray(derived)) messages = derived;
    } catch {
      return undefined;
    }
  }
  if (messages.length === 0 && Array.isArray(root.messages)) messages = root.messages;
  for (let messageIndex = messages.length - 1; messageIndex >= 0; messageIndex -= 1) {
    const message = messages[messageIndex];
    if (!isRecord(message) || !Array.isArray(message.content)) continue;
    for (const block of message.content) {
      if (!isRecord(block) || (block.type !== "tool-result" && block.type !== "tool_result")) continue;
      const callID = firstString(block.toolCallId, block.tool_call_id, block.callId, block.call_id);
      const execution: Record<string, unknown> = { callId: callID };
      for (let priorIndex = messageIndex - 1; priorIndex >= 0; priorIndex -= 1) {
        const prior = messages[priorIndex];
        if (!isRecord(prior) || !Array.isArray(prior.content)) continue;
        const call = prior.content.find((candidate) => isRecord(candidate) && (candidate.type === "tool-call" || candidate.type === "tool_call") && firstString(candidate.id, candidate.toolCallId, candidate.tool_call_id, candidate.callId, candidate.call_id) === callID);
        if (!isRecord(call)) continue;
        execution.name = firstString(call.name);
        execution.arguments = parseArguments(call.arguments);
        break;
      }
      return { agent, session_id: firstString(agent.id, session.id), execution, result: { isError: block.isError === true, content: block.content } };
    }
  }
  return undefined;
}

function parseArguments(value: unknown): Record<string, unknown> {
  if (isRecord(value)) return value;
  if (typeof value === "string") {
    try {
      const parsed: unknown = JSON.parse(value);
      return isRecord(parsed) ? parsed : { value };
    } catch {
      return { value };
    }
  }
  return {};
}

function toolCallID(payload: unknown): string {
  const root = isRecord(payload) ? payload : {};
  const execution = isRecord(root.execution) ? root.execution : {};
  const result = isRecord(root.result) ? root.result : {};
  return firstString(execution.callId, execution.call_id, root.callId, root.call_id, result.toolCallId, result.tool_call_id);
}

function toolExecutionRecord(payload: unknown): Record<string, unknown> & { callId: string } {
  const root = isRecord(payload) ? payload : {};
  return {
    callId: firstString(root.callId, root.call_id, root.id),
    name: firstString(root.name, root.tool, root.tool_name),
    arguments: parseArguments(root.arguments),
  };
}

function toolResultFromSessionEvent(payload: unknown): { callID: string; result: Record<string, unknown> } | undefined {
  const root = isRecord(payload) ? payload : {};
  const event = isRecord(root.event) ? root.event : {};
  if (event.type !== "tool/result") return undefined;
  const data = isRecord(event.data) ? event.data : {};
  const message = isRecord(data.message) ? data.message : {};
  const blocks = Array.isArray(message.content) ? message.content : [];
  const block = blocks.find((candidate) => isRecord(candidate) && (candidate.type === "tool-result" || candidate.type === "tool_result"));
  if (!isRecord(block)) return undefined;
  return {
    callID: firstString(block.toolCallId, block.tool_call_id, block.callId, block.call_id),
    result: { isError: block.isError === true, content: block.content },
  };
}

function lastStartedTool(startedTools: Map<string, Record<string, unknown>>): Record<string, unknown> | undefined {
  let last: Record<string, unknown> | undefined;
  for (const execution of startedTools.values()) last = execution;
  return last;
}

function latestToolExecution(payload: unknown): Record<string, unknown> | undefined {
  const root = isRecord(payload) ? payload : {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  if (typeof session.deriveMessages !== "function") return undefined;
  let messages: unknown[];
  try {
    const derived = session.deriveMessages.call(session);
    messages = Array.isArray(derived) ? derived : [];
  } catch {
    return undefined;
  }
  for (let messageIndex = messages.length - 1; messageIndex >= 0; messageIndex -= 1) {
    const message = messages[messageIndex];
    if (!isRecord(message) || !Array.isArray(message.content)) continue;
    for (let blockIndex = message.content.length - 1; blockIndex >= 0; blockIndex -= 1) {
      const block = message.content[blockIndex];
      if (!isRecord(block) || (block.type !== "tool-call" && block.type !== "tool_call")) continue;
      return { callId: firstString(block.id, block.toolCallId, block.tool_call_id, block.callId, block.call_id), name: firstString(block.name), arguments: parseArguments(block.arguments) };
    }
  }
  return undefined;
}

function latestAssistantText(agent: Record<string, unknown>): string {
  const session = isRecord(agent.session) ? agent.session : {};
  const deriveMessages = session.deriveMessages;
  if (typeof deriveMessages !== "function") return "";
  try {
    const messages = deriveMessages.call(session);
    if (!Array.isArray(messages)) return "";
    for (let index = messages.length - 1; index >= 0; index -= 1) {
      const message = messages[index];
      if (isRecord(message) && message.role === "assistant") return textFromContent(message.content);
    }
  } catch {
    return "";
  }
  return "";
}

function latestUserText(payload: Record<string, unknown>): string {
  const agent = isRecord(payload.agent) ? payload.agent : {};
  const session = isRecord(payload.session) ? payload.session : isRecord(agent.session) ? agent.session : {};
  let messages: unknown[] = [];
  if (typeof session.deriveMessages === "function") {
    try {
      const derived = session.deriveMessages.call(session);
      if (Array.isArray(derived)) messages = derived;
    } catch {
      messages = [];
    }
  }
  if (messages.length === 0 && Array.isArray(payload.messages)) messages = payload.messages;
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!isRecord(message) || message.role !== "user" || isRecallMessage(message)) continue;
    const text = textFromContent(message.content);
    if (text !== "") return text;
  }
  return firstString(payload.prompt, payload.text, payload.message);
}

function isRecallMessage(message: Record<string, unknown>): boolean {
  const source = isRecord(message.source) ? message.source : {};
  return source.kind === "plugin" && source.form === "recall";
}

function textFromContent(content: unknown): string {
  if (typeof content === "string") return content.slice(0, 12000);
  if (!Array.isArray(content)) return "";
  return content.map((block) => {
    if (typeof block === "string") return block;
    if (!isRecord(block)) return "";
    return firstString(block.text, block.output, block.value, block.message);
  }).filter(Boolean).join("\n").slice(0, 12000);
}

function isRecord(value: unknown): value is Record<string, any> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isSafeScalar(value: unknown): value is string | number | boolean {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

function firstString(...values: unknown[]): string {
  for (const value of values) if (typeof value === "string" && value.trim() !== "") return value.trim().slice(0, 12000);
  return "";
}

function exitCodeFromText(value: string): number | undefined {
  const match = value.match(/\[exit code\s*:\s*(-?\d+)\]|\bexit code\b\s*(?::|=|was|is)\s*\**(-?\d+)/i);
  return match ? Number(match[1] ?? match[2]) : undefined;
}

import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";

export const name = "baron-dsh-adapter";
export const version = "0.1.0";

export function apply(ctx) {
  const adapter = createBaronAdapter();
  ctx.on("agent/session-start", (payload) => adapter.onSessionStart(payload));
  ctx.on("agent/pre-step", (payload, next) => adapter.onPreStep(payload, next), { prepend: true });
  ctx.on("tools/pre-execute", async (execution, next) => {
    await adapter.onToolStarted(execution);
    return next ? next() : undefined;
  });
  ctx.on("tools/result", async (execution, result) => {
    return adapter.onToolFinished({ execution, result });
  });
  ctx.on("session/event", (session, event) => adapter.onSessionEvent({ session, event }));
  ctx.on("agent/turn-stopping", (payload) => adapter.onTurnStopping(payload));
  ctx.on("session/flush", (payload) => adapter.onFlush(payload));
}

export function createBaronAdapter(options = {}) {
  const binary = options.baronBinary || process.env.BARON_BINARY || "baron";
  const cwd = options.cwd || process.cwd();
  const timeoutMs = options.timeoutMs || 5000;
  const emit = (event, payload) => invoke(binary, cwd, timeoutMs, event, payload);
  const capturedToolCalls = new Set();
  const startedTools = new Map();
  const onToolFinished = (payload) => {
    const callID = toolCallID(payload);
    if (callID !== "") {
      if (capturedToolCalls.has(callID)) return Promise.resolve({ ok: true, duplicate: true });
      capturedToolCalls.add(callID);
    }
    return emit("tool_finished", lifecyclePayload("tool_finished", payload));
  };
  return {
    name,
    version,
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
      if (execution.callId !== "") startedTools.set(execution.callId, execution);
      return emit("tool_started", lifecyclePayload("tool_started", payload));
    },
    onToolFinished,
    onFileChanged: (payload) => emit("file_changed", lifecyclePayload("file_changed", payload)),
    onSessionEvent: async (payload) => {
      const toolResult = toolResultFromSessionEvent(payload);
      if (toolResult !== undefined) {
        const execution = startedTools.get(toolResult.callId) || { callId: toolResult.callId };
        await onToolFinished({ session: isRecord(payload) ? payload.session : undefined, execution, result: toolResult.result });
        startedTools.delete(toolResult.callId);
      }
      return emit("checkpoint_updated", lifecyclePayload("checkpoint_updated", payload));
    },
    onTurnStopping: (payload) => {
      const root = isRecord(payload) ? { ...payload } : {};
      if (!isRecord(root.execution)) {
        const execution = latestToolExecution(root) || lastStartedTool(startedTools);
        if (execution !== undefined) root.execution = execution;
      }
      const response = emit("assistant_final", lifecyclePayload("assistant_final", root));
      startedTools.clear();
      return response;
    },
    onFlush: (payload) => emit("session_clean_closed", lifecyclePayload("session_clean_closed", payload)),
  };
}

function invoke(binary, cwd, timeoutMs, event, payload) {
  return new Promise((resolve) => {
    const child = spawn(binary, ["hook", "dsh", event], { cwd, stdio: ["pipe", "pipe", "ignore"] });
    let output = "";
    const timer = setTimeout(() => {
      child.kill();
      resolve({ ok: false, error: "Baron hook timeout" });
    }, timeoutMs);
    child.stdout.on("data", (chunk) => { output += chunk.toString("utf8"); });
    child.on("error", (error) => {
      clearTimeout(timer);
      resolve({ ok: false, error: error.message });
    });
    child.on("close", () => {
      clearTimeout(timer);
      try { resolve(JSON.parse(output)); } catch { resolve({ ok: false, error: "invalid Baron hook response" }); }
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

function addHistoricalContext(decision, response) {
  if (!isRecord(decision) || decision.kind !== "enter" || !Array.isArray(decision.messages)) return decision;
  const context = typeof response.context === "string" ? response.context.trim() : "";
  if (context === "") return decision;
  return { kind: "enter", messages: [...decision.messages, createRecallMessage(context)] };
}

function createRecallMessage(text) {
  return {
    id: `baron-context-${randomUUID()}`,
    role: "user",
    source: { kind: "plugin", plugin: name, form: "recall", version: 1 },
    content: [{ type: "text", text }],
  };
}

function lifecyclePayload(event, payload) {
  const root = isRecord(payload) ? payload : {};
  const result = {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  const sessionID = firstString(agent.id, session.id, root.session_id, root.sessionId, root.id);
  if (sessionID !== "") result.session_id = sessionID;
  const header = isRecord(session.header) ? session.header : {};
  const cwd = firstString(header.cwd, root.cwd);
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
    const eventText = textFromContent(root.event.data);
    if (eventText !== "") result.summary = eventText;
  }
  return result;
}

function latestToolEvidence(payload) {
  const root = isRecord(payload) ? payload : {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  let messages = [];
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
      const execution = { callId: callID };
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

function parseArguments(value) {
  if (isRecord(value)) return value;
  if (typeof value === "string") {
    try {
      const parsed = JSON.parse(value);
      return isRecord(parsed) ? parsed : { value };
    } catch {
      return { value };
    }
  }
  return {};
}

function toolCallID(payload) {
  const root = isRecord(payload) ? payload : {};
  const execution = isRecord(root.execution) ? root.execution : {};
  const result = isRecord(root.result) ? root.result : {};
  return firstString(execution.callId, execution.call_id, root.callId, root.call_id, result.toolCallId, result.tool_call_id);
}

function toolExecutionRecord(payload) {
  const root = isRecord(payload) ? payload : {};
  const execution = {
    callId: firstString(root.callId, root.call_id, root.id),
    name: firstString(root.name, root.tool, root.tool_name),
    arguments: parseArguments(root.arguments),
  };
  return execution;
}

function toolResultFromSessionEvent(payload) {
  const root = isRecord(payload) ? payload : {};
  const event = isRecord(root.event) ? root.event : {};
  if (event.type !== "tool/result") return undefined;
  const data = isRecord(event.data) ? event.data : {};
  const message = isRecord(data.message) ? data.message : {};
  const blocks = Array.isArray(message.content) ? message.content : [];
  const block = blocks.find((candidate) => isRecord(candidate) && (candidate.type === "tool-result" || candidate.type === "tool_result"));
  if (!isRecord(block)) return undefined;
  return {
    callId: firstString(block.toolCallId, block.tool_call_id, block.callId, block.call_id),
    result: { isError: block.isError === true, content: block.content },
  };
}

function lastStartedTool(startedTools) {
  let last;
  for (const execution of startedTools.values()) last = execution;
  return last;
}

function latestToolExecution(payload) {
  const root = isRecord(payload) ? payload : {};
  const agent = isRecord(root.agent) ? root.agent : {};
  const session = isRecord(root.session) ? root.session : isRecord(agent.session) ? agent.session : {};
  if (typeof session.deriveMessages !== "function") return undefined;
  let messages;
  try {
    messages = session.deriveMessages.call(session);
  } catch {
    return undefined;
  }
  if (!Array.isArray(messages)) return undefined;
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

function latestAssistantText(agent) {
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

function latestUserText(payload) {
  const agent = isRecord(payload.agent) ? payload.agent : {};
  const session = isRecord(payload.session) ? payload.session : isRecord(agent.session) ? agent.session : {};
  let messages = [];
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

function isRecallMessage(message) {
  const source = isRecord(message.source) ? message.source : {};
  return source.kind === "plugin" && source.form === "recall";
}

function textFromContent(content) {
  if (typeof content === "string") return content.slice(0, 12000);
  if (!Array.isArray(content)) return "";
  return content.map((block) => {
    if (typeof block === "string") return block;
    if (!isRecord(block)) return "";
    return firstString(block.text, block.output, block.value, block.message);
  }).filter(Boolean).join("\n").slice(0, 12000);
}

function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isSafeScalar(value) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

function firstString(...values) {
  for (const value of values) if (typeof value === "string" && value.trim() !== "") return value.trim().slice(0, 12000);
  return "";
}

function exitCodeFromText(value) {
  const match = typeof value === "string" ? value.match(/\[exit code\s*:\s*(-?\d+)\]|\bexit code\b\s*(?::|=|was|is)\s*\**(-?\d+)/i) : null;
  return match ? Number(match[1] ?? match[2]) : undefined;
}

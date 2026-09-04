#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_node() {
  if command -v node >/dev/null 2>&1; then
    node "$@"
    return
  fi
  if ! command -v wsl.exe >/dev/null 2>&1; then
    printf '%s\n' 'Node.js is required, and Ubuntu WSL was not found.' >&2
    return 127
  fi
  local args=("$@")
  local last_index=$(($# - 1))
  args[$last_index]=$wsl_repo
  cat | MSYS_NO_PATHCONV=1 wsl.exe --distribution Ubuntu -- /usr/bin/node "${args[@]}"
}
if ! command -v node >/dev/null 2>&1; then
  if ! command -v wsl.exe >/dev/null 2>&1; then
    printf '%s\n' 'Node.js is required, and Ubuntu WSL was not found.' >&2
    exit 127
  fi
  wsl_repo=$(wsl.exe --distribution Ubuntu -- wslpath -a "$repo_root" | tr -d '\r')
  if [ -z "$wsl_repo" ]; then
    printf '%s\n' 'Ubuntu WSL path conversion failed.' >&2
    exit 127
  fi
fi
run_node --input-type=module - "$repo_root" <<'NODE'
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const repo = process.argv[2];
const work = await mkdtemp(join(tmpdir(), "baron-dsh-adapter-test-"));
const fakeBaronScript = join(work, "fake-baron.mjs");
await writeFile(fakeBaronScript, `
let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => { input += chunk; });
process.stdin.on("end", () => {
  let parsed = {};
  try { parsed = JSON.parse(input); } catch {}
  process.stdout.write(JSON.stringify({
    ok: true,
    historical_context_available: true,
    context: "[historical-reference] CODEX_TO_DSH_ADAPTER_TEST_SENTINEL " + (process.argv[4] || "unknown") + " " + JSON.stringify(parsed)
  }));
});
`, { mode: 0o700 });
const fakeBaron = join(work, process.platform === "win32" ? "baron.cmd" : "baron");
if (process.platform === "win32") {
  await writeFile(fakeBaron, `@echo off\r\n"${process.execPath}" "${fakeBaronScript}" %*\r\n`);
} else {
  await writeFile(fakeBaron, `#!/usr/bin/env node\nimport "${fakeBaronScript}";\n`, { mode: 0o700 });
  await chmod(fakeBaron, 0o700);
}

const module = await import(pathToFileURL(join(repo, "adapters/dsh/index.js")));
const embeddedModule = await import(pathToFileURL(join(repo, "internal/install/assets/dsh-adapter/index.js")));
const expectedTaskEvents = [
  "task_started",
  "task_updated",
  "task_failed",
  "task_blocked",
  "task_verified",
  "task_completed",
  "task_interrupted",
];
for (const adapterModule of [module, embeddedModule]) {
  assert.deepEqual(adapterModule.TASK_EVENTS, expectedTaskEvents);
  const taskAdapter = adapterModule.createBaronAdapter({ baronBinary: fakeBaron });
  for (const event of expectedTaskEvents) {
    const handler = `on${event.split("_").map((part) => part[0].toUpperCase() + part.slice(1)).join("")}`;
    assert.equal(typeof taskAdapter[handler], "function", `${adapterModule.name} exposes ${handler}`);
  }
  const taskResponse = await taskAdapter.onTaskStarted({
    task_id: "task-parity-sentinel",
    active_task_id: "active-task-parity-sentinel",
    task: { completion_policy: "completion", verification_scope: "completion" },
    changed_files: ["backend/main.go"],
  });
  assert.match(taskResponse.context, /"task_id":"task-parity-sentinel"/);
  assert.match(taskResponse.context, /"completion_policy":"completion"/);
  assert.match(taskResponse.context, /"verification_scope":"completion"/);
  assert.match(taskResponse.context, /"changed_files":\["backend\/main\.go"\]/);
}
assert.deepEqual(module.detectPentestRequest({ prompt: "pentest sâu dự án này" }), { mode: "deep", prompt: "pentest sâu dự án này" });
assert.equal(module.detectPentestRequest({ prompt: "continue" }), null);
process.env.BARON_BINARY = fakeBaron;
const handlers = new Map();
module.apply({ on(event, handler) { handlers.set(event, handler); } });
const decision = await handlers.get("agent/pre-step")({
  agent: { id: "session-dsh-test" },
  messages: [{ role: "user", content: [{ type: "text", text: "continue" }] }]
}, async () => ({
  kind: "enter",
  messages: [{ role: "user", content: [{ type: "text", text: "continue" }] }]
}));

assert.equal(decision.kind, "enter");
assert.equal(decision.messages.length, 2);
assert.equal(decision.messages[1].role, "user");
assert.equal(decision.messages[1].source.kind, "plugin");
assert.equal(decision.messages[1].source.form, "recall");
assert.match(decision.messages[1].content[0].text, /CODEX_TO_DSH_ADAPTER_TEST_SENTINEL/);
assert.match(decision.messages[1].content[0].text, /"prompt":"continue"/);
const scalarPromptDecision = await handlers.get("agent/pre-step")({
  agent: { id: "session-dsh-scalar-test" },
  prompt: "SCALAR_DSH_PROMPT_SENTINEL"
}, async () => ({
  kind: "enter",
  messages: [{ role: "user", content: [{ type: "text", text: "continue" }] }]
}));
assert.match(scalarPromptDecision.messages[1].content[0].text, /SCALAR_DSH_PROMPT_SENTINEL/);
const pentestDecision = await handlers.get("agent/pre-step")({
  agent: { id: "session-dsh-pentest-test" },
  prompt: "pentest sâu dự án này"
}, async () => ({
  kind: "enter",
  messages: [{ role: "user", content: [{ type: "text", text: "pentest sâu dự án này" }] }]
}));
assert.equal(pentestDecision.messages.length, 3);
assert.match(pentestDecision.messages[2].content[0].text, /baron pentest --deep/);
assert.match(pentestDecision.messages[2].content[0].text, /must not modify the real source tree/);
const finalResponse = await handlers.get("agent/turn-stopping")({
  agent: {
    id: "session-dsh-final-test",
    session: { deriveMessages: () => [
      { role: "user", content: [{ type: "text", text: "ACTUAL_DSH_USER_TURN" }] },
      { role: "user", source: { kind: "plugin", form: "recall" }, content: [{ type: "text", text: "DO_NOT_REPLAY_RECALL" }] },
      { role: "assistant", content: [{ type: "text", text: "ACTUAL_DSH_ASSISTANT_TURN" }] },
    ] },
  },
});
assert.match(finalResponse.context, /"prompt":"ACTUAL_DSH_USER_TURN"/);
assert.match(finalResponse.context, /"response":"ACTUAL_DSH_ASSISTANT_TURN"/);
assert.doesNotMatch(finalResponse.context, /DO_NOT_REPLAY_RECALL/);
const toolResult = await handlers.get("tools/result")({
  name: "bash",
  arguments: { command: "bash -lc 'exit 23'" },
}, {
  isError: true,
  error: { message: "exit code: 23" },
  content: [{ type: "text", text: "[exit code: 23]" }],
});
assert.match(toolResult.context, /tool_finished/);
await rm(work, { recursive: true, force: true });
console.log("DSH adapter context injection test passed.");
NODE

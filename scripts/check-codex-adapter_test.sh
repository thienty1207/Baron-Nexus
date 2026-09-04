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
const work = await mkdtemp(join(tmpdir(), "baron-codex-adapter-test-"));
const fakeBaronScript = join(work, "fake-baron.mjs");
await writeFile(fakeBaronScript, `
let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => { input += chunk; });
process.stdin.on("end", () => {
  let parsed = {};
  try { parsed = JSON.parse(input); } catch {}
  process.stdout.write(JSON.stringify({ ok: true, received: parsed }));
});
`);
const fakeBaron = join(work, process.platform === "win32" ? "baron.cmd" : "baron");
if (process.platform === "win32") {
  await writeFile(fakeBaron, `@echo off\r\n"${process.execPath}" "${fakeBaronScript}" %*\r\n`);
} else {
  await writeFile(fakeBaron, `#!/usr/bin/env node\nimport "${fakeBaronScript}";\n`, { mode: 0o700 });
  await chmod(fakeBaron, 0o700);
}

const module = await import(pathToFileURL(join(repo, "adapters/codex/index.js")));
const embeddedModule = await import(pathToFileURL(join(repo, "internal/install/assets/codex-adapter/index.js")));
const expectedEvents = [
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
];
const expectedTaskFields = ["task_id", "active_task_id", "completion_policy", "verification_ref", "verification_kind", "verification_scope"];
for (const adapterModule of [module, embeddedModule]) {
  assert.deepEqual(adapterModule.CODEX_EVENTS, expectedEvents);
  assert.deepEqual(adapterModule.TASK_FIELDS, expectedTaskFields);
  const taskAdapter = adapterModule.createCodexAdapter({ baronBinary: fakeBaron });
  for (const event of expectedEvents.slice(8)) {
    const handler = `on${event.split("_").map((part) => part[0].toUpperCase() + part.slice(1)).join("")}`;
    assert.equal(typeof taskAdapter[handler], "function", `${adapterModule.name} exposes ${handler}`);
  }
  const taskResponse = await taskAdapter.onTaskStarted({
    task: { id: "task-parity-sentinel", completion_policy: "completion", verification_scope: "completion" },
    activeTaskId: "active-task-parity-sentinel",
  });
  assert.equal(taskResponse.received.payload.task_id, "task-parity-sentinel");
  assert.equal(taskResponse.received.payload.active_task_id, "active-task-parity-sentinel");
  assert.equal(taskResponse.received.payload.completion_policy, "completion");
  assert.equal(taskResponse.received.payload.verification_scope, "completion");
}
assert.deepEqual(module.detectPentestRequest({ prompt: "pentest --deep" }), { mode: "deep", prompt: "pentest --deep" });
assert.equal(module.detectPentestRequest({ prompt: "continue" }), null);
const adapter = module.createCodexAdapter({ baronBinary: fakeBaron });
const response = await adapter.onUserPrompt({ prompt: "pentest --deep" });
const context = response.hookSpecificOutput.additionalContext;
assert.match(context, /baron pentest --deep/);
assert.match(context, /report-only/);
await rm(work, { recursive: true, force: true });
console.log("Codex adapter pentest directive test passed.");
NODE

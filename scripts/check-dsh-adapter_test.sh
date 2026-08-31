#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
node --input-type=module - "$repo_root" <<'NODE'
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const repo = process.argv[2];
const work = await mkdtemp(join(tmpdir(), "baron-dsh-adapter-test-"));
const fakeBaron = join(work, "baron");
await writeFile(fakeBaron, `#!/usr/bin/env node
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
await chmod(fakeBaron, 0o700);

const module = await import(pathToFileURL(join(repo, "adapters/dsh/index.js")));
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

// Miriam eval harness — a terminal TUI for testing Miriam's responses, latency,
// token cost, and quality. It drives the real orchestrator through the gated
// /eval/miriam endpoint (see internal/api/handlers/eval). Run: bun run src/eval.ts
//
// Env:
//   EVAL_URL       default http://localhost:8080/api/v1/eval/miriam
//   EVAL_TOKEN     shared secret matching EVAL_TOKEN on the backend (required)
//   EVAL_USER_ID   Rail user id to impersonate for testing (required, overridable with /user)
import { Spectrum, markdown, type Space } from "spectrum-ts";
import { terminal } from "spectrum-ts/providers/terminal";

const EVAL_URL = process.env.EVAL_URL || "http://localhost:8080/api/v1/eval/miriam";
const AUTOPILOT_URL = process.env.AUTOPILOT_URL || "http://localhost:8080/api/v1/eval/autopilot";
const ANOMALY_URL = process.env.ANOMALY_URL || "http://localhost:8080/api/v1/eval/anomaly";
const EVAL_TOKEN = process.env.EVAL_TOKEN || "";
let defaultUserID = process.env.EVAL_USER_ID || "";

if (!EVAL_TOKEN) {
  console.error("EVAL_TOKEN is required (must match the backend's EVAL_TOKEN).");
  process.exit(1);
}

interface EvalResponse {
  content: string;
  conversation_id: string;
  provider: string;
  tokens: number;
  latency_ms: number;
  pending_action?: { action: string; description: string };
  quality: { pass: boolean; failures?: string[] };
}

// Per-space session state so each terminal chat is an isolated conversation.
interface Session {
  userID: string;
  conversationID: string;
}
const sessions = new Map<string, Session>();

function session(spaceId: string): Session {
  let s = sessions.get(spaceId);
  if (!s) {
    s = { userID: defaultUserID, conversationID: "" };
    sessions.set(spaceId, s);
  }
  return s;
}

async function callMiriam(s: Session, message: string): Promise<EvalResponse> {
  const res = await fetch(EVAL_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Eval-Token": EVAL_TOKEN },
    body: JSON.stringify({
      user_id: s.userID,
      message,
      conversation_id: s.conversationID || undefined,
    }),
  });
  if (!res.ok) {
    throw new Error(`eval endpoint ${res.status}: ${await res.text()}`);
  }
  return (await res.json()) as EvalResponse;
}

function metaLine(r: EvalResponse): string {
  const parts = [
    `${r.latency_ms}ms`,
    `${r.tokens} tok`,
    r.provider,
    `quality: ${r.quality.pass ? "pass" : "FAIL"}`,
  ];
  if (r.quality.failures?.length) parts.push(`(${r.quality.failures.join(", ")})`);
  if (r.pending_action) parts.push(`→ staged action: ${r.pending_action.action}`);
  return `⟨ ${parts.join(" · ")} ⟩`;
}

const commands = [
  { name: "/new", description: "Start a fresh conversation in this chat" },
  { name: "/user", description: "Switch the test user id: /user <uuid>" },
  { name: "/whoami", description: "Show the current test user and conversation" },
  { name: "/autopilot", description: "Trigger autopilot phase: /autopilot morning|midday|evening|all" },
  { name: "/anomaly", description: "Run anomaly scan for current user" },
  { name: "/help", description: "List commands" },
];

async function handleCommand(space: Space, s: Session, text: string): Promise<boolean> {
  const [cmd, ...rest] = text.trim().split(/\s+/);
  switch (cmd) {
    case "/new":
      s.conversationID = "";
      await space.send("Started a fresh conversation.");
      return true;
    case "/user":
      if (rest[0]) {
        s.userID = rest[0];
        s.conversationID = "";
        await space.send(`Now testing as user ${s.userID}.`);
      } else {
        await space.send("Usage: /user <uuid>");
      }
      return true;
    case "/autopilot": {
      const phase = rest[0];
      if (!phase || !["morning", "midday", "evening", "all"].includes(phase)) {
        await space.send("Usage: /autopilot morning|midday|evening|all");
        return true;
      }
      const phases = phase === "all" ? ["morning", "midday", "evening"] : [phase];
      for (const p of phases) {
        const res = await fetch(`${AUTOPILOT_URL}/${p}`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-Eval-Token": EVAL_TOKEN },
          body: JSON.stringify({ user_id: s.userID || undefined }),
        });
        if (!res.ok) {
          await space.send(`autopilot ${p}: ${res.status} ${await res.text()}`);
          continue;
        }
        const data = await res.json();
        await space.send(`${data.summary}`);
      }
      return true;
    }
    case "/anomaly": {
      if (!s.userID) {
        await space.send("Set a test user first: /user <uuid>");
        return true;
      }
      const res = await fetch(ANOMALY_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Eval-Token": EVAL_TOKEN },
        body: JSON.stringify({ user_id: s.userID }),
      });
      if (!res.ok) {
        await space.send(`anomaly scan: ${res.status} ${await res.text()}`);
        return true;
      }
      const data = await res.json();
      await space.send(data.summary || "Anomaly scan complete.");
      return true;
    }
    case "/whoami":
      await space.send(`user: ${s.userID || "(unset)"} · conversation: ${s.conversationID || "(new)"}`);
      return true;
    case "/help":
      await space.send(commands.map((c) => `${c.name} — ${c.description}`).join("\n"));
      return true;
    default:
      return false;
  }
}

async function main() {
  const app = await Spectrum({ providers: [terminal.config({ commands })] });
  console.log(`Miriam eval ready. Endpoint: ${EVAL_URL}`);
  console.log(defaultUserID ? `Test user: ${defaultUserID}` : "No EVAL_USER_ID set — use /user <uuid>");

  for await (const [space, message] of app.messages) {
    if (message.content.type !== "text") continue;
    const text = message.content.text.trim();
    if (!text) continue;

    const s = session(space.id);
    if (text.startsWith("/") && (await handleCommand(space, s, text))) continue;

    if (!s.userID) {
      await space.send("Set a test user first: /user <uuid>");
      continue;
    }

    await space.startTyping().catch(() => {});
    try {
      const r = await callMiriam(s, text);
      s.conversationID = r.conversation_id;
      await space.send(markdown(r.content));
      await space.send(metaLine(r));
    } catch (err) {
      await space.send(`error: ${(err as Error).message}`);
    } finally {
      await space.stopTyping().catch(() => {});
    }
  }
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});

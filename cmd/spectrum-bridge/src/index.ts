import { Spectrum, type Space, type Message, typing } from "spectrum-ts";
import { imessage } from "spectrum-ts/providers/imessage";
import { telegram } from "spectrum-ts/providers/telegram";
import { whatsappBusiness } from "spectrum-ts/providers/whatsapp-business";
import express from "express";
import crypto from "node:crypto";
import { loadConfig } from "./config";
import { MessageHandler, OutboundMessage } from "./handler";
import { getLogger, childLogger } from "./logger";
import { SpaceStore } from "./space-store";
import { PersistentOutboundQueue, type QueuedMessage } from "./outbound-queue";

const config = loadConfig();
const log = getLogger();

const app = express();

// Webhook route MUST be registered BEFORE the global express.json() middleware.
// express.json() consumes the request body stream; if it runs first, the route-level
// express.raw() can't recover the raw bytes the Spectrum SDK needs for HMAC verification.
// We use a deferred handler since the agent isn't created until start().
let webhookAgent: Awaited<ReturnType<typeof Spectrum>> | null = null;

app.post(
  config.SPECTRUM_WEBHOOK_PATH,
  express.raw({ type: "*/*" }),
  async (req, res) => {
    if (!webhookAgent) {
      res.status(503).json({ error: "bridge not ready" });
      return;
    }
    try {
      const result = await webhookAgent.webhook(
        {
          body: req.body as Uint8Array,
          headers: req.headers as Record<string, string>,
        },
        async (space: Space, message: Message) => {
          await handleInbound(space, message);
        },
      );
      res.status(result.status).set(result.headers).send(Buffer.from(result.body));
    } catch (err) {
      log.error({ err }, "webhook handler failed");
      res.status(500).json({ error: "internal error" });
    }
  },
);

// Global JSON parser for /send and other JSON endpoints — registered AFTER the
// webhook route so it doesn't consume the webhook's raw body.
app.use(
  express.json({
    verify: (req, _res, buf) => {
      (req as express.Request & { rawBody?: string }).rawBody = buf.toString("utf-8");
    },
  }),
);

const handler = new MessageHandler();

// Persistent space store — survives bridge restarts so we know which threads exist.
const spaceStore = new SpaceStore();
await spaceStore.load();
spaceStore.startAutoSave();

// Persistent outbound queue — survives bridge restarts so proactive messages are
// not lost when the live Space handle is cold. Messages flush when the user texts
// again and the space warms up.
const outboundQueue = new PersistentOutboundQueue();
await outboundQueue.load();
outboundQueue.startAutoSave();

// Registry of Space handles seen on inbound messages, so the outbound consumer
// can send to a conversation by id. Spectrum only surfaces Space objects through
// the inbound stream, so we can only send to spaces the user has messaged from —
// which covers every reply/confirmation path.
const spaces = new Map<string, Space>();

const HMAC_FRESHNESS_WINDOW_MS = 5 * 60 * 1000;
const MAX_SEEN_NONCES = 10_000;
const seenNonces = new Map<string, number>(); // nonce -> expiration timestamp

// Periodically evict expired nonces used for replay protection.
setInterval(() => {
  const now = Date.now();
  for (const [nonce, expiresAt] of seenNonces) {
    if (expiresAt < now) seenNonces.delete(nonce);
  }
}, 60_000);

function evictOldestNoncesIfNeeded(): void {
  if (seenNonces.size < MAX_SEEN_NONCES) return;

  // Emergency cleanup: remove oldest nonces by expiration time, keeping ~80%
  // of the limit to avoid thrashing.
  const entries = Array.from(seenNonces.entries());
  entries.sort((a, b) => a[1] - b[1]);
  const keepCount = Math.floor(MAX_SEEN_NONCES * 0.8);
  const dropCount = Math.max(0, entries.length - keepCount);
  for (let i = 0; i < dropCount; i++) {
    seenNonces.delete(entries[i][0]);
  }
  log.warn({ dropped: dropCount, remaining: seenNonces.size }, "emergency nonce eviction");
}

function isFreshTimestamp(timestampSec: number): boolean {
  const nowMs = Date.now();
  const tsMs = timestampSec * 1000;
  return tsMs >= nowMs - HMAC_FRESHNESS_WINDOW_MS && tsMs <= nowMs + HMAC_FRESHNESS_WINDOW_MS;
}

function isNonceUnique(nonce: string, timestampSec: number): boolean {
  if (seenNonces.has(nonce)) return false;
  evictOldestNoncesIfNeeded();
  seenNonces.set(nonce, timestampSec * 1000 + HMAC_FRESHNESS_WINDOW_MS);
  return true;
}

function signPayload(payload: string, timestamp: string, nonce: string): string {
  return crypto
    .createHmac("sha256", config.RAIL_HMAC_SECRET)
    .update(`${timestamp}.${nonce}.${payload}`)
    .digest("hex");
}

function verifyHMAC(payload: string, signature: string, timestamp: string, nonce: string): boolean {
  try {
    const ts = parseInt(timestamp, 10);
    if (!Number.isFinite(ts) || !isFreshTimestamp(ts) || !isNonceUnique(nonce, ts)) {
      return false;
    }
    const expected = signPayload(payload, timestamp, nonce);
    return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature));
  } catch {
    return false;
  }
}

function normalizePlatform(platform: string): string {
  switch (platform.toLowerCase()) {
    case "imessage":
      return "imessage";
    case "telegram":
      return "telegram";
    case "whatsapp":
    case "whatsapp business":
      return "whatsapp";
    default:
      return platform;
  }
}

function makeHMACHeaders(payload: string): Record<string, string> {
  const timestamp = String(Math.floor(Date.now() / 1000));
  const nonce = crypto.randomUUID();
  return {
    "X-HMAC-Timestamp": timestamp,
    "X-HMAC-Nonce": nonce,
    "X-HMAC-SHA256": signPayload(payload, timestamp, nonce),
  };
}

async function postToBackend(path: string, body: unknown): Promise<void> {
  const url = `${config.RAIL_BACKEND_URL}${path}`;
  const payload = JSON.stringify(body);
  const hmacHeaders = makeHMACHeaders(payload);

  const resp = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...hmacHeaders,
    },
    body: payload,
    signal: AbortSignal.timeout(15_000),
  });

  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    log.error({ url, status: resp.status, body: text.slice(0, 200) }, "backend POST failed");
    throw new Error(`backend ${path}: ${resp.status}`);
  }
}

async function sendToSpace(msg: OutboundMessage): Promise<boolean> {
  const space = spaces.get(msg.thread_id);
  if (!space) {
    const known = spaceStore.has(msg.thread_id);
    log.warn(
      { thread_id: msg.thread_id, known_space: known },
      `no active space for thread_id="${msg.thread_id}"`,
    );
    return false;
  }
  try {
    await handler.handleOutbound(space, msg);
    // Mark inbound message as read after successful reply
    const lastInbound = handler.getLastInboundMessage(msg.thread_id);
    if (lastInbound) {
      space.read(lastInbound).catch(() => {});
    }
    return true;
  } catch (err) {
    log.error({ err, thread_id: msg.thread_id }, "failed to send to space");
    return false;
  }
}

// Send a queued message and update the queue based on the outcome.
async function attemptQueuedSend(item: QueuedMessage): Promise<void> {
  const sent = await sendToSpace(item.msg);
  if (sent) {
    outboundQueue.remove(item.id);
    return;
  }

  const updated = outboundQueue.recordAttempt(item.id);
  if (updated) {
    log.info(
      { thread_id: item.msg.thread_id, attempt: updated.attempts, retry_in: updated.nextRetryAt - Date.now() },
      "outbound queued for retry",
    );
  }
}

// Process outbound queue — retry messages that failed due to cold spaces.
function processOutboundQueue(): void {
  const ready = outboundQueue.getReady();
  for (const item of ready) {
    attemptQueuedSend(item).catch((err) =>
      log.error({ err, thread_id: item.msg.thread_id }, "failed to process queued outbound"),
    );
  }
}

setInterval(processOutboundQueue, 5_000);

// Flush any messages queued for a thread that just warmed up. Called after an
// inbound message registers the space handle. This deliberately does not await
// individual sends so the inbound webhook can respond promptly.
async function flushQueuedMessages(threadID: string): Promise<void> {
  const pending = outboundQueue.bumpThread(threadID);
  if (pending.length === 0) return;

  log.info({ thread_id: threadID, count: pending.length }, "flushing queued messages for warmed space");
  for (const item of pending) {
    attemptQueuedSend(item).catch((err) =>
      log.error({ err, thread_id: item.msg.thread_id }, "failed to flush queued outbound"),
    );
  }
}

// HTTP endpoint for the backend to send outbound messages to the bridge.
app.post("/send", (req, res) => {
  const raw =
    (req as express.Request & { rawBody?: string }).rawBody ?? JSON.stringify(req.body);
  const sig = req.headers["x-hmac-sha256"] as string | undefined;
  const timestamp = req.headers["x-hmac-timestamp"] as string | undefined;
  const nonce = req.headers["x-hmac-nonce"] as string | undefined;
  if (!sig || !timestamp || !nonce || !verifyHMAC(raw, sig, timestamp, nonce)) {
    log.warn({ ip: req.ip }, "invalid HMAC signature on /send");
    res.status(401).json({ error: "invalid or missing HMAC signature" });
    return;
  }
  const msg = req.body as OutboundMessage;

  sendToSpace(msg).then((sent) => {
    if (!sent) {
      // Queue for retry — user will need to message again to warm the space,
      // or the space will be re-discovered on next inbound. Critical messages
      // get a longer TTL so anomaly alerts and money receipts survive longer.
      outboundQueue.enqueue(msg, msg.category ?? "normal");
    }
  });
  res.json({ status: "queued" });
});

app.get(["/", "/health"], (_req, res) => {
  const stats = outboundQueue.getStats();
  res.json({
    status: "ok",
    spaces: spaces.size,
    known_threads: spaceStore.count(),
    queued_outbound: stats.totalQueued,
    queued_by_thread: stats.byThread,
    queued_oldest_ms: stats.oldestMessage ? Date.now() - stats.oldestMessage : undefined,
    uptime_sec: Math.floor(process.uptime()),
  });
});

async function handleInbound(space: Space, message: Message): Promise<void> {
  const threadID = space.id;
  const platform = normalizePlatform(message.platform);
  spaces.set(threadID, space);
  spaceStore.register(threadID, space.id);
  handler.registerInboundMessage(message);

  // The space just warmed up. Flush any proactive messages that were queued
  // while the handle was cold (bridge restart or eviction) without blocking
  // the inbound path.
  flushQueuedMessages(threadID).catch((err) =>
    log.error({ err, thread_id: threadID }, "failed to flush queued messages"),
  );

  const senderId = message.sender?.id;
  if (!senderId) return;

  const content = message.content;
  const log = childLogger({ sender: senderId, thread: threadID, msg_type: content.type });

  if ((message as any).isFromMe) {
    log.debug("skipping self-send echo");
    return;
  }

  // Show typing indicator while we process
  space.startTyping().catch(() => {});

  try {
    // Poll vote — confirm/cancel action
    if (content.type === "poll_option") {
      if (!content.selected) return;
      const choice = content.option.title.trim().toLowerCase();
      const event = {
        action: choice === "confirm" ? "confirm" : "cancel",
        poll_title: content.poll.title,
        user_id: senderId,
        space_id: space.id,
        platform,
      };
      await postToBackend("/api/v1/platform/action", event);
      log.info({ action: event.action }, "posted poll vote to backend");
      return;
    }

    // Voice note
    if (content.type === "voice") {
      let audioB64: string;
      try {
        const buf = await content.read();
        audioB64 = Buffer.from(buf).toString("base64");
      } catch (err) {
        log.error({ err }, "failed to read voice note");
        return;
      }
      const inbound = {
        platform,
        user_id: senderId,
        thread_id: space.id,
        text: "",
        space_id: space.id,
        msg_id: message.id,
        is_voice: true,
        audio_b64: audioB64,
        audio_mime: content.mimeType,
      };
      await postToBackend("/api/v1/platform/inbound", inbound);
      log.info({ audio_len: audioB64.length }, "posted voice note to backend");
      return;
    }

    // Image attachment
    if (content.type === "attachment") {
      if (!content.mimeType?.startsWith("image/")) {
        log.debug({ mime: content.mimeType }, "ignoring non-image attachment");
        return;
      }
      let imageB64: string;
      try {
        const buf = await content.read();
        imageB64 = Buffer.from(buf).toString("base64");
      } catch (err) {
        log.error({ err }, "failed to read attachment");
        return;
      }
      const inbound = {
        platform,
        user_id: senderId,
        thread_id: space.id,
        text: "",
        space_id: space.id,
        msg_id: message.id,
        is_image: true,
        image_b64: imageB64,
        image_mime: content.mimeType,
      };
      await postToBackend("/api/v1/platform/inbound", inbound);
      log.info({ image_len: imageB64.length }, "posted image to backend");
      return;
    }

    // Text message
    if (content.type === "text") {
      const text = content.text?.trim();
      if (!text) return;

      // Check for YES/NO confirmation replies (for platforms without poll support)
      const normalized = text.toLowerCase();
      if (normalized === "yes" || normalized === "confirm" || normalized === "no" || normalized === "cancel") {
        const event = {
          action: normalized === "yes" || normalized === "confirm" ? "confirm" : "cancel",
          poll_title: "",
          user_id: senderId,
          space_id: space.id,
          platform,
        };
        await postToBackend("/api/v1/platform/action", event);
        log.info({ action: event.action, text }, "posted YES/NO confirmation to backend");
        return;
      }

      const inbound = {
        platform,
        user_id: senderId,
        thread_id: space.id,
        text,
        space_id: space.id,
        msg_id: message.id,
      };
      await postToBackend("/api/v1/platform/inbound", inbound);
      log.info({ text: text.slice(0, 60) }, "posted inbound message to backend");
    }
  } catch (err) {
    log.error({ err, thread_id: threadID }, "inbound handling failed");
  } finally {
    space.stopTyping().catch(() => {});
  }
}

async function start() {
  log.info(
    { backend_url: config.RAIL_BACKEND_URL },
    "starting spectrum bridge",
  );

  const providers = [imessage.config()];
  if (config.TELEGRAM_BOT_TOKEN) {
    providers.push(
      telegram.config({
        botToken: config.TELEGRAM_BOT_TOKEN,
        ...(config.TELEGRAM_WEBHOOK_SECRET
          ? { webhookSecret: config.TELEGRAM_WEBHOOK_SECRET }
          : {}),
      }) as never,
    );
    log.info("Telegram provider enabled");
  }

  if (config.WHATSAPP_ACCESS_TOKEN && config.WHATSAPP_PHONE_NUMBER_ID) {
    providers.push(
      whatsappBusiness.config({
        accessToken: config.WHATSAPP_ACCESS_TOKEN,
        phoneNumberId: config.WHATSAPP_PHONE_NUMBER_ID,
        ...(config.WHATSAPP_APP_SECRET ? { appSecret: config.WHATSAPP_APP_SECRET } : {}),
      }) as never,
    );
    log.info("WhatsApp Business enabled");
  }

  const agent = await Spectrum({
    projectId: config.SPECTRUM_PROJECT_ID,
    projectSecret: config.SPECTRUM_PROJECT_SECRET,
    providers,
    ...(config.SPECTRUM_WEBHOOK_SECRET ? { webhookSecret: config.SPECTRUM_WEBHOOK_SECRET } : {}),
  });

  webhookAgent = agent;

  app.listen(config.BRIDGE_PORT, () => {
    log.info({ port: config.BRIDGE_PORT }, "bridge HTTP server listening");
  });

  log.info("waiting for provider messages...");

  for await (const [space, message] of agent.messages) {
    handleInbound(space, message).catch((err) => log.error({ err }, "inbound handling error"));
  }
}

start().catch((err) => {
  log.fatal({ err }, "bridge failed to start");
  process.exit(1);
});

process.on("SIGTERM", async () => {
  log.info("shutting down...");
  await spaceStore.flush();
  spaceStore.stopAutoSave();
  await outboundQueue.flush();
  outboundQueue.stopAutoSave();
  process.exit(0);
});

process.on("SIGINT", async () => {
  log.info("shutting down (SIGINT)...");
  await spaceStore.flush();
  spaceStore.stopAutoSave();
  await outboundQueue.flush();
  outboundQueue.stopAutoSave();
  process.exit(0);
});

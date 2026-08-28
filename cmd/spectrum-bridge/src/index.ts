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
import { contactFromSpectrum, isVCardMime } from "./contact";

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

// Inbound message deduplication. The bridge registers both a webhook handler
// (app.webhook) and the streaming async iterator (app.messages). Per the
// Spectrum docs, app.webhook() does NOT feed app.messages — they are
// independent delivery paths. If the Spectrum dashboard is configured for
// webhooks, the same message arrives through both. We dedupe by message.id
// so the backend only processes each message once.
const MSG_DEDUP_TTL_MS = 60_000;
const MAX_DEDUP_IDS = 5_000;
const processedMessageIds = new Map<string, number>(); // msgId -> expiration

// Periodically evict expired nonces and dedup ids.
setInterval(() => {
  const now = Date.now();
  for (const [nonce, expiresAt] of seenNonces) {
    if (expiresAt < now) seenNonces.delete(nonce);
  }
  for (const [msgId, expiresAt] of processedMessageIds) {
    if (expiresAt < now) processedMessageIds.delete(msgId);
  }
}, 60_000);

/**
 * Quickselect (Hoare's algorithm): partially reorders `arr` so that the element
 * at index `k` is in its final sorted position, with all smaller elements before
 * it and all larger elements after it. O(n) average time, O(1) extra space.
 * Avoids the O(n log n) full sort that was previously used for nonce eviction.
 */
function quickselect(arr: [string, number][], k: number): void {
  let lo = 0;
  let hi = arr.length - 1;
  while (lo < hi) {
    const pivot = arr[(lo + hi) >> 1][1];
    let i = lo;
    let j = hi;
    while (i <= j) {
      while (arr[i][1] < pivot) i++;
      while (arr[j][1] > pivot) j--;
      if (i <= j) {
        [arr[i], arr[j]] = [arr[j], arr[i]];
        i++;
        j--;
      }
    }
    if (k <= j) hi = j;
    else if (k >= i) lo = i;
    else break;
  }
}

function evictOldestNoncesIfNeeded(): void {
  if (seenNonces.size < MAX_SEEN_NONCES) return;

  // Emergency cleanup: remove oldest nonces by expiration time, keeping ~80%
  // of the limit to avoid thrashing. Uses quickselect (O(n) average) instead
  // of a full sort (O(n log n)) to find the eviction cutoff.
  const entries = Array.from(seenNonces.entries());
  const keepCount = Math.floor(MAX_SEEN_NONCES * 0.8);
  const dropCount = Math.max(0, entries.length - keepCount);
  if (dropCount === 0) return;

  quickselect(entries, dropCount);
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
    if (!Number.isFinite(ts) || !isFreshTimestamp(ts)) {
      return false;
    }
    const expected = signPayload(payload, timestamp, nonce);
    if (!crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature))) {
      return false;
    }
    // Replay rejection runs only after the HMAC signature is verified so
    // that unauthenticated requests cannot exhaust the nonce store.
    return isNonceUnique(nonce, ts);
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

// Backend POST retry policy: a transient backend failure (5xx, network blip,
// timeout) previously meant the message was silently lost — the inbound handler
// has no redelivery guarantee, so the user's text just vanished. We now retry
// with backoff before giving up. 4xx (except 429) are permanent failures —
// e.g. HMAC mismatches — and fail immediately.
const BACKEND_MAX_ATTEMPTS = 3;
const BACKEND_RETRY_DELAY_MS = 1500;
const BACKEND_RETRYABLE_STATUS = new Set([408, 425, 429, 500, 502, 503, 504]);

class BackendPostError extends Error {
  status?: number;
  constructor(path: string, status?: number) {
    super(`backend ${path}: ${status ?? "network error"}`);
    this.status = status;
  }
}

async function postToBackendOnce(path: string, body: unknown): Promise<void> {
  const url = `${config.RAIL_BACKEND_URL}${path}`;
  const payload = JSON.stringify(body);
  // Fresh timestamp+nonce per attempt: each attempt is an independent signed
  // request; reusing a nonce would trip the bridge/backend replay guards.
  const hmacHeaders = makeHMACHeaders(payload);

  let resp: Response;
  try {
    resp = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...hmacHeaders,
      },
      body: payload,
      signal: AbortSignal.timeout(15_000),
    });
  } catch {
    throw new BackendPostError(path);
  }

  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    log.error({ url, status: resp.status, body: text.slice(0, 200) }, "backend POST failed");
    throw new BackendPostError(path, resp.status);
  }
}

async function postToBackend(path: string, body: unknown): Promise<void> {
  for (let attempt = 1; ; attempt++) {
    try {
      await postToBackendOnce(path, body);
      return;
    } catch (err) {
      const status = err instanceof BackendPostError ? err.status : undefined;
      const retryable =
        attempt < BACKEND_MAX_ATTEMPTS &&
        (status === undefined || BACKEND_RETRYABLE_STATUS.has(status));
      if (!retryable) throw err;
      log.warn(
        { path, attempt, status: status ?? "network", retry_in_ms: BACKEND_RETRY_DELAY_MS },
        "backend POST failed, retrying",
      );
      await new Promise((resolve) => setTimeout(resolve, BACKEND_RETRY_DELAY_MS * attempt));
    }
  }
}

// Per-thread typing keepers. iMessage's native indicator expires within
// seconds, and backend processing (LLM think time, tool calls) regularly takes
// longer than the old single startTyping() + immediate stopTyping-in-finally.
// The keeper refreshes the indicator every TYPING_REFRESH_MS until an outbound
// reply actually goes out or a safety deadline hits, so the user sees "..."
// for the whole wait instead of a dead indicator.
const TYPING_REFRESH_MS = 20_000;
const TYPING_MAX_MS = 90_000;

interface TypingKeeper {
  refresh: NodeJS.Timeout;
  deadline: NodeJS.Timeout;
}

const typingKeepers = new Map<string, TypingKeeper>();

function startTypingKeeper(threadID: string, space: Space): void {
  stopTypingKeeper(threadID);
  space.startTyping().catch((err) => {
    log.warn({ err, thread_id: threadID }, "startTyping failed (provider may not support typing indicators)");
  });
  const refresh = setInterval(() => {
    space.startTyping().catch((err) => {
      log.warn({ err, thread_id: threadID }, "startTyping refresh failed");
    });
  }, TYPING_REFRESH_MS);
  const deadline = setTimeout(() => {
    log.warn({ thread_id: threadID }, "typing keeper hit safety deadline");
    stopTypingKeeper(threadID);
  }, TYPING_MAX_MS);
  typingKeepers.set(threadID, { refresh, deadline });
}

function clearTypingKeeper(threadID: string): void {
  const keeper = typingKeepers.get(threadID);
  if (!keeper) return;
  clearInterval(keeper.refresh);
  clearTimeout(keeper.deadline);
  typingKeepers.delete(threadID);
}

/**
 * Tear down the keeper AND signal stop-typing. Used when nothing will be sent
 * (safety deadline, send failure) so no dead "..." dangles in the thread.
 */
function stopTypingKeeper(threadID: string): void {
  clearTypingKeeper(threadID);
  spaces.get(threadID)?.stopTyping().catch((err) => {
    log.warn({ err, thread_id: threadID }, "stopTyping failed");
  });
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
  // A reply is going out now: retire the keeper's refresh timers but do NOT
  // signal stop-typing here — handleOutbound asserts typing before every
  // bubble, and an explicit stop here would blank the indicator for a beat
  // right before the reply lands. Retries or the next message's typing
  // assertion will keep the indicator alive until a real message lands.
  clearTypingKeeper(msg.thread_id);
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
    space.stopTyping().catch(() => {});
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
  // Deduplicate by message.id — the webhook handler and the streaming iterator
  // are independent delivery paths and may both deliver the same message.
  if (message.id) {
    if (processedMessageIds.has(message.id)) {
      getLogger().debug({ msg_id: message.id }, "deduplicated inbound message");
      return;
    }
    // Bounded: if at capacity, evict oldest entries by expiration time.
    if (processedMessageIds.size >= MAX_DEDUP_IDS) {
      const entries = Array.from(processedMessageIds.entries());
      entries.sort((a, b) => a[1] - b[1]);
      const dropCount = Math.floor(MAX_DEDUP_IDS * 0.2);
      for (let i = 0; i < dropCount; i++) {
        processedMessageIds.delete(entries[i][0]);
      }
    }
    processedMessageIds.set(message.id, Date.now() + MSG_DEDUP_TTL_MS);
  }

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

  // Keep the typing indicator alive for as long as backend processing takes.
  startTypingKeeper(threadID, space);

  try {
    // Poll vote — the selected option title is posted as ordinary inbound text
    // so onboarding, send_poll, and Confirm/Cancel all go through one path.
    // The Go processor maps Confirm/Cancel to pending actions when one exists.
    if (content.type === "poll_option") {
      if (!content.selected) return;
      const text = content.option?.title?.trim();
      if (!text) return;
      const inbound = {
        platform,
        user_id: senderId,
        thread_id: space.id,
        text,
        space_id: space.id,
        msg_id: message.id,
        is_poll_vote: true,
      };
      await postToBackend("/api/v1/platform/inbound", inbound);
      log.info({ text: text.slice(0, 60) }, "posted poll vote as inbound");
      return;
    }

    // Shared contact card (iMessage Share Contact)
    if (content.type === "contact") {
      const inbound = {
        platform,
        user_id: senderId,
        thread_id: space.id,
        text: "",
        space_id: space.id,
        msg_id: message.id,
        is_contact: true,
        contact: contactFromSpectrum(content),
      };
      await postToBackend("/api/v1/platform/inbound", inbound);
      log.info("posted shared contact to backend");
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

    // Image or vCard attachment
    if (content.type === "attachment") {
      const filename = (content as { filename?: string; name?: string }).filename
        || (content as { filename?: string; name?: string }).name;
      if (isVCardMime(content.mimeType, filename)) {
        let vcardText = "";
        try {
          const buf = await content.read();
          vcardText = Buffer.from(buf).toString("utf8");
        } catch (err) {
          log.error({ err }, "failed to read vcard attachment");
          return;
        }
        const inbound = {
          platform,
          user_id: senderId,
          thread_id: space.id,
          text: "",
          space_id: space.id,
          msg_id: message.id,
          is_contact: true,
          vcard_text: vcardText,
        };
        await postToBackend("/api/v1/platform/inbound", inbound);
        log.info({ bytes: vcardText.length }, "posted vcard attachment to backend");
        return;
      }
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

    // Text message. Bare YES/NO is handled by the backend when a pending
    // action exists, so onboarding consent is not swallowed here.
    if (content.type === "text") {
      const text = content.text?.trim();
      if (!text) return;

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
    // Release the dedup reservation so a redelivered copy of this message can
    // still be processed — the backend never accepted it.
    if (message.id) processedMessageIds.delete(message.id);
    log.error({ err, thread_id: threadID }, "inbound handling failed");
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

  // Map our LOG_LEVEL to the SDK's accepted levels. The SDK doesn't accept
  // "trace" or "fatal", so we fold them to the nearest supported level.
  const sdkLogLevel: Record<string, string> = {
    trace: "debug",
    debug: "debug",
    info: "info",
    warn: "warn",
    error: "error",
    fatal: "error",
  };

  const agent = await Spectrum({
    projectId: config.SPECTRUM_PROJECT_ID,
    projectSecret: config.SPECTRUM_PROJECT_SECRET,
    providers,
    options: { logLevel: sdkLogLevel[config.LOG_LEVEL] ?? "info" } as never,
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

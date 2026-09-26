import { Spectrum, type Space, type Message } from "spectrum-ts";
import { imessage } from "spectrum-ts/providers/imessage";
import { telegram } from "spectrum-ts/providers/telegram";
import { whatsappBusiness } from "spectrum-ts/providers/whatsapp-business";
import express from "express";
import crypto from "node:crypto";
import type { Server } from "node:http";
import { loadConfig } from "./config";
import { MessageHandler, OutboundMessage } from "./handler";
import { ConfirmationCardStore } from "./confirmation-store";
import { getLogger, childLogger } from "./logger";
import { SpaceStore } from "./space-store";
import { SpaceResolver } from "./space-resolver";
import { OutboundPacer } from "./outbound-pacer";
import { DeliverabilityTracker } from "./deliverability";
import { FailureAudit } from "./failure-audit";
import { extractSpaceMeta } from "./space-meta";
import { PersistentOutboundQueue, type QueuedMessage } from "./outbound-queue";
import { InboundSpool } from "./inbound-spool";
import {
  InboundDebouncer,
  isOutboundEcho,
  routeInboundContent,
  type InboundPayload,
} from "./inbound";
import { TurnSupersession } from "./turn-supersession";
import { aliasProviderPlatformKeys } from "./platform-alias";

const config = loadConfig();
const log = getLogger();

// Inbound turn supersession (docs/miriam-inbound-supersession.md): gate 2 of 2.
// The backend stamps each reply with the turn it answers; we drop any reply
// whose turn the user has already moved past. Gated on MIRIAM_TURN_SUPERSESSION
// (off by default) and paired with the backend's PLATFORM_TURN_SUPERSESSION.
const turns = new TurnSupersession(config.MIRIAM_TURN_SUPERSESSION);

// Spectrum SDK lifecycle handles (bound in start(), torn down in shutdown()).
// Module-scoped so signal handlers can drain them — the SDK's own stop()
// closes the message stream, destroys provider clients, and flushes telemetry.
let spectrumAgent: Awaited<ReturnType<typeof Spectrum>> | null = null;
// Back-compat alias used by the webhook route below.
let webhookAgent: Awaited<ReturnType<typeof Spectrum>> | null = null;
let httpServer: Server | null = null;
let shuttingDown = false;

const app = express();

// Webhook route MUST be registered BEFORE the global express.json() middleware.
// express.json() consumes the request body stream; if it runs first, the route-level
// express.raw() can't recover the raw bytes the Spectrum SDK needs for HMAC verification.
// The agent is bound in start(); until then the route answers 503.
// In `stream` transport mode this route is not registered (see below).
const WEBHOOK_ENABLED = config.SPECTRUM_TRANSPORT_MODE !== "stream";

if (WEBHOOK_ENABLED) {
app.post(
  config.SPECTRUM_WEBHOOK_PATH,
  express.raw({ type: "*/*" }),
  async (req, res) => {
    if (!webhookAgent || shuttingDown) {
      res.status(503).json({ error: "bridge not ready" });
      return;
    }
    try {
      const result = await webhookAgent.webhook(
        {
          body: req.body as Uint8Array,
          headers: req.headers as Record<string, string>,
        },
        (space: Space, message: Message) => {
          // Fire-and-forget per SDK semantics: the HTTP response is already
          // sent by the time this runs, and throws are swallowed by the SDK.
          // Own the errors here — audit + log — so failures stay visible and
          // the dedup reservation is released for redelivery.
          handleInbound(space, message).catch((err) => {
            failures.record("webhook", message?.id ?? "unknown", { thread: space?.id }, err);
            log.error({ err }, "webhook inbound handling error");
          });
        },
      );
      res.status(result.status).set(result.headers).send(Buffer.from(result.body));
    } catch (err) {
      log.error({ err }, "webhook handler failed");
      res.status(500).json({ error: "internal error" });
    }
  },
);
}

// Global JSON parser for /send and other JSON endpoints — registered AFTER the
// webhook route so it doesn't consume the webhook's raw body.
app.use(
  express.json({
    verify: (req, _res, buf) => {
      (req as express.Request & { rawBody?: string }).rawBody = buf.toString("utf-8");
    },
  }),
);

// Live confirmation card handles (action_id -> message id) so approve /
// reject / expire / fill edits mutate the SAME card — across restarts too.
const cardStore = new ConfirmationCardStore();
await cardStore.load();
cardStore.startAutoSave();

const handler = new MessageHandler({
  maxBubbles: config.OUTBOUND_MAX_BUBBLES,
  miniApp: {
    appName: config.IMESSAGE_APP_NAME,
    extensionBundleId: config.IMESSAGE_EXTENSION_BUNDLE_ID,
    teamId: config.APPLE_TEAM_ID,
  },
  cardStore,
  cardAssetsDir: config.CONFIRM_CARD_ASSETS_DIR,
});
const failures = new FailureAudit();

// Persistent space store — survives bridge restarts so we know which threads exist.
const spaceStore = new SpaceStore();
await spaceStore.load();
spaceStore.startAutoSave();

// Persistent outbound queue — survives bridge restarts so proactive messages are
// not lost when the live Space handle is cold. Messages rehydrate via the
// SpaceResolver (im.space.get) or flush when the user texts again.
const outboundQueue = new PersistentOutboundQueue();
await outboundQueue.load();
outboundQueue.startAutoSave();

// Global cross-conversation send pacer (see outbound-pacer.ts). Per-bubble
// typing delays humanize a single reply; this bucket stops fan-outs and
// post-restart flushes from hitting the wire as a line-flagging burst.
const pacer = new OutboundPacer({
  capacity: config.PACER_BURST,
  refillIntervalMs: config.PACER_REFILL_MS,
});

// Platform hard caps (5,000 outbound/server/day, 50 new convos/line/day).
const deliverability = new DeliverabilityTracker({
  dailyOutboundCap: config.DELIVERY_DAILY_CAP,
  newConvosPerLinePerDay: config.DELIVERY_NEW_CONVOS_PER_LINE,
});

// Owns Space handles for outbound sends: live inbound handles first, then
// rehydration from the persisted SpaceStore record via im.space.get (cheap,
// purely local construction on the remote iMessage provider). The fetcher is
// bound once Spectrum() exists; until then the live cache is all there is.
const spaceResolver = new SpaceResolver({
  get: async () => {
    throw new Error("space resolver not bound (agent not started)");
  },
  lookup: (threadId: string) => spaceStore.get(threadId),
});

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
// Upper bound on per-thread turn state. Fail-open, so eviction only disables
// supersession until the next message re-establishes the thread's turn.
const MAX_TRACKED_TURNS = 10_000;
const processedMessageIds = new Map<string, number>(); // msgId -> expiration

// Periodically evict expired nonces and dedup ids.
const sweeper = setInterval(() => {
  const now = Date.now();
  for (const [nonce, expiresAt] of seenNonces) {
    if (expiresAt < now) seenNonces.delete(nonce);
  }
  for (const [msgId, expiresAt] of processedMessageIds) {
    if (expiresAt < now) processedMessageIds.delete(msgId);
  }
  // Turn-supersession state is fail-open, so bounding it is safe: clearing only
  // stops suppressing stale replies until the next message re-establishes a turn.
  if (turns.trackedThreads > MAX_TRACKED_TURNS) turns.clear();
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

async function postToBackendOnce(path: string, body: unknown, attempt: number, timeoutMs: number): Promise<void> {
  const url = `${config.RAIL_BACKEND_URL}${path}`;
  // The attempt counter travels inside the signed body, not a header, so the
  // backend can trust it. It tells the backend whether a redelivery is still
  // coming: a transient failure can be requeued until the last attempt, where
  // the backend has to answer the user instead.
  const payload = JSON.stringify(
    body && typeof body === "object" && !Array.isArray(body)
      ? { ...(body as Record<string, unknown>), attempt, max_attempts: maxAttemptsFor(body) }
      : body,
  );
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
      signal: AbortSignal.timeout(timeoutMs),
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

function backendTimeoutMs(body: unknown): number {
  // A statement PDF is read before the reply is sent. Text stays on the short
  // timeout. 150s covers extraction plus one model pass plus Miriam's answer.
  if (body && typeof body === "object" && !Array.isArray(body)) {
    const doc = body as { is_document?: boolean };
    if (doc.is_document) return 150_000;
  }
  return 15_000;
}

// Document scans are idempotent-unsafe to blindly retry: each attempt mints a
// fresh ScanGuest + Offer nonce set. Timeouts (status undefined) get ONE
// retry; fast failures keep the normal budget. Worst case is ~300s of holding
// the turn, not ~450s+.
function maxAttemptsFor(body: unknown): number {
  if (body && typeof body === "object" && !Array.isArray(body)) {
    const doc = body as { is_document?: boolean };
    if (doc.is_document) return Math.min(BACKEND_MAX_ATTEMPTS, 2);
  }
  return BACKEND_MAX_ATTEMPTS;
}

async function postToBackend(path: string, body: unknown): Promise<void> {
  // Turn supersession: mint one turn id per content-bearing inbound batch, so
  // the backend can echo it and gate 2 below can drop a reply the user has
  // already moved past. Lifecycle signals (read receipts, unsends) carry no
  // content and are left untagged.
  if (
    path === "/api/v1/platform/inbound" &&
    body &&
    typeof body === "object" &&
    !Array.isArray(body)
  ) {
    turns.tagInbound(body as InboundPayload);
  }
  const timeoutMs = backendTimeoutMs(body);
  const maxAttempts = maxAttemptsFor(body);
  for (let attempt = 1; ; attempt++) {
    try {
      await postToBackendOnce(path, body, attempt, timeoutMs);
      return;
    } catch (err) {
      const status = err instanceof BackendPostError ? err.status : undefined;
      const retryable =
        attempt < maxAttempts &&
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

// Inbound burst debounce: rapid-fire texts ("Hey" / "Oluwatobiloba" / "Hi")
// are coalesced into ONE backend turn so onboarding state doesn't race and the
// user gets one reply instead of three. Non-text content (votes, contacts,
// voice, images, reactions) flushes the pending buffer first, then posts
// immediately. The typing keeper starts when the first text enters the buffer
// so the "..." bubble covers the debounce window.
const INBOUND_DEBOUNCE_MS = 4_000;
const INBOUND_MAX_WAIT_MS = 10_000;
const INBOUND_MAX_BUFFER = 5;

const debouncer = new InboundDebouncer({
  post: (_key, payload: InboundPayload) => postToBackend("/api/v1/platform/inbound", payload),
  debounceMs: INBOUND_DEBOUNCE_MS,
  maxWaitMs: INBOUND_MAX_WAIT_MS,
  maxBuffer: INBOUND_MAX_BUFFER,
  onBufferStart: (threadID) => {
    const space = spaceResolver.cached(threadID);
    if (space) startTypingKeeper(threadID, space);
  },
  onError: (threadID, err) => {
    log.error({ err, thread_id: threadID }, "debounced inbound flush failed");
  },
  onCarried: (threadID, err) => {
    log.warn(
      { err, thread_id: threadID },
      "inbound flush exhausted retries — carried forward to the thread's next message",
    );
  },
});

// Durable spool for the debounce buffer: a hard crash mid-quiet-window must not
// lose the user's words. Restore any buffered batches now — they flush as soon
// as their original deadline passes — and keep the spool current while we run.
const inboundSpool = new InboundSpool();
const spooledInbound = await inboundSpool.load();
if (spooledInbound.length > 0) {
  const restored = debouncer.restore(spooledInbound);
  log.info({ buffers: restored }, "re-armed buffered inbound from spool");
}
inboundSpool.startAutoSave(() => debouncer.snapshot());

// Per-thread typing keepers. The SDK's space.responding(fn) covers typing for
// a single in-process send, but our "thinking" window spans HTTP hops
// (debounce -> backend LLM -> /send), so no single fn scope covers it. The
// keeper refreshes the native indicator until an outbound reply actually goes
// out or a safety deadline hits. Every path that ends a turn — successful
// send, send error, inbound error — must call stopTypingKeeper.
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

function stopTypingKeeper(threadID: string): void {
  const keeper = typingKeepers.get(threadID);
  if (!keeper) return;
  clearInterval(keeper.refresh);
  clearTimeout(keeper.deadline);
  typingKeepers.delete(threadID);
  spaceResolver.cached(threadID)?.stopTyping().catch((err) => {
    log.warn({ err, thread_id: threadID }, "stopTyping failed");
  });
}

/** Outcome of a send attempt: ok | cold (no handle — defer, no retry spent) |
 *  capped (deliverability cap — defer) | failed (provider error — backoff). */
type SendOutcome = "ok" | "cold" | "capped" | "failed";

async function sendToSpace(msg: OutboundMessage): Promise<SendOutcome> {
  // Gate 2 of turn supersession: the user has already sent something newer, so
  // this reply is stale. Report "ok" (not "capped"/"failed") so nothing queues
  // a retry that would deliver it late.
  if (turns.isStale(msg.thread_id, msg.turn_id)) {
    log.info(
      { thread_id: msg.thread_id, turn_id: msg.turn_id },
      "dropping reply for superseded inbound turn",
    );
    return "ok";
  }
  // Backend-scheduled delivery: the Go ProactiveGuard owns quiet-hours, but
  // any send may carry send_after as an escape hatch. Honored here by
  // (re)queueing with notBefore instead of sending early.
  if (msg.send_after && msg.send_after > Date.now()) {
    outboundQueue.enqueue(msg, msg.category ?? "normal", { notBefore: msg.send_after });
    return "ok";
  }
  // Stable idempotency key for the handler-level retry dedup.
  if (!msg.client_guid) msg.client_guid = crypto.randomUUID();

  const space = await spaceResolver.resolve(msg.thread_id);
  if (!space) {
    const known = spaceStore.has(msg.thread_id);
    log.warn(
      { thread_id: msg.thread_id, known_space: known },
      `no space handle for thread_id="${msg.thread_id}" (will rehydrate or warm on next inbound)`,
    );
    return "cold";
  }
  // Transport-level deliverability caps — never burn the line on the wire.
  if (!deliverability.checkOutbound()) {
    log.warn({ thread_id: msg.thread_id }, "daily outbound cap reached, deferring send");
    return "capped";
  }
  // First-contact sends count against the per-line new-conversation quota.
  // Replies inside known threads never consult it.
  const isFirstContact = !spaceStore.has(msg.thread_id) && normalizePlatform(msg.platform) === "imessage";
  const line = spaceStore.get(msg.thread_id)?.phone ?? "imessage";
  if (isFirstContact && !deliverability.checkNewConvo(line)) {
    log.warn({ thread_id: msg.thread_id, line }, "new-conversation quota reached, deferring send");
    return "capped";
  }
  // Cross-conversation pacing: one token per send so fan-outs and queue
  // flushes never hit the wire as a line-flagging burst.
  await pacer.acquire();
  if (shuttingDown) return "capped";
  // A reply is going out now: end the processing indicator. The handler's own
  // pacing (typeThenSend) re-triggers typing between multi-bubble replies.
  stopTypingKeeper(msg.thread_id);
  try {
    await handler.handleOutbound(space, { ...msg, is_first: msg.is_first ?? isFirstContact });
    deliverability.recordOutbound();
    if (isFirstContact) deliverability.recordNewConvo(line);
    // Mark inbound message as read after successful reply
    const lastInbound = handler.getLastInboundMessage(msg.thread_id);
    if (lastInbound) {
      space.read(lastInbound).catch(() => {});
    }
    return "ok";
  } catch (err) {
    failures.record("outbound", msg.client_guid ?? msg.thread_id, msg, err);
    log.error({ err, thread_id: msg.thread_id }, "failed to send to space");
    return "failed";
  }
}

// Send a queued message and update the queue based on the outcome. Cold/capped
// outcomes DEFER without spending the retry budget (a retry cannot fix a cold
// handle or a spent quota — burning attempts there dropped messages ~1 minute
// after a restart despite their 4h/24h TTLs).
async function attemptQueuedSend(item: QueuedMessage): Promise<void> {
  const outcome = await sendToSpace(item.msg);
  if (outcome === "ok") {
    outboundQueue.remove(item.id);
    return;
  }
  if (outcome === "cold" || outcome === "capped") {
    outboundQueue.defer(item.id, 30_000);
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
  if (shuttingDown) return;
  const ready = outboundQueue.getReady();
  for (const item of ready) {
    attemptQueuedSend(item).catch((err) =>
      log.error({ err, thread_id: item.msg.thread_id }, "failed to process queued outbound"),
    );
  }
}

const queueTimer = setInterval(processOutboundQueue, 5_000);

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

  // sendToSpace owns scheduling: future send_after is (re)queued with
  // notBefore, cold/capped outcomes defer without spending retry budget.
  // The resolver rehydrates handles from disk, so proactive sends no longer
  // wait for the user to text again.
  sendToSpace(msg).then((outcome) => {
    if (outcome !== "ok") {
      outboundQueue.enqueue(
        msg,
        msg.category ?? "normal",
        msg.send_after && msg.send_after > Date.now() ? { notBefore: msg.send_after } : undefined,
      );
    }
  });
  res.json({ status: "queued" });
});

app.get(["/", "/health"], (_req, res) => {
  const stats = outboundQueue.getStats();
  const inboundBuffered = debouncer.snapshot();
  res.json({
    status: shuttingDown ? "draining" : "ok",
    transport_mode: config.SPECTRUM_TRANSPORT_MODE,
    webhook_enabled: WEBHOOK_ENABLED,
    spaces: spaceResolver.size,
    known_threads: spaceStore.count(),
    confirmation_cards: cardStore.count(),
    queued_outbound: stats.totalQueued,
    queued_by_thread: stats.byThread,
    queued_oldest_ms: stats.oldestMessage ? Date.now() - stats.oldestMessage : undefined,
    pacer: { available: pacer.available(), pending: pacer.pending },
    deliverability: deliverability.stats(),
    inbound_spool: {
      buffers: inboundBuffered.length,
      entries: inboundBuffered.reduce((n, b) => n + b.entries.length, 0),
      carried: inboundBuffered.reduce((n, b) => n + (b.carried?.length ?? 0), 0),
    },
    turn_supersession: {
      enabled: turns.isEnabled,
      tracked_threads: turns.trackedThreads,
    },
    recent_failures: failures.count(),
    uptime_sec: Math.floor(process.uptime()),
  });
});

// Recent failure audit (bounded, payload summaries only — no message bodies).
app.get("/health/failures", (_req, res) => {
  res.json({ failures: failures.recent(20) });
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
  spaceResolver.prime(threadID, space);
  // Persist platform + line phone so the resolver can rebuild this handle
  // after a restart (im.space.get requires `phone` with 2+ dedicated lines).
  const meta = extractSpaceMeta(space, platform, message);
  const isNewSpace = spaceStore.register(threadID, space.id, {
    platform: meta.platform,
    phone: meta.phone,
  });
  handler.registerInboundMessage(message);

  // First time we've EVER seen this space (persisted across restarts): share
  // Miriam's own contact card so she shows up as a named contact instead of a
  // raw phone number. iMessage-only; fire-and-forget so the debounce window
  // is never delayed by it.
  if (isNewSpace && platform === "imessage") {
    Promise.resolve()
      .then(() => imessage(space).shareContactCard())
      .catch((err) => log.warn({ err, thread_id: threadID }, "failed to share contact card"));
  }

  // The space just warmed up. Flush any proactive messages that were queued
  // while the handle was cold (bridge restart or eviction) without blocking
  // the inbound path.
  flushQueuedMessages(threadID).catch((err) =>
    log.error({ err, thread_id: threadID }, "failed to flush queued messages"),
  );

  const senderId = message.sender?.id;
  if (!senderId) return;

  const content = message.content;
  const reqLog = childLogger({
    sender: senderId,
    thread: threadID,
    msg_type: content.type,
    ...(meta.senderService ? { service: meta.senderService } : {}),
  });

  // Self-echo guard: Spectrum echoes our own outbound sends (including poll
  // votes on bot-authored polls) back through the inbound stream. The real
  // field is message.direction — the old `isFromMe` check never matched.
  if (isOutboundEcho(message)) {
    reqLog.debug({ msg_id: message.id }, "skipping outbound-direction echo");
    return;
  }

  // Keep the typing indicator alive for as long as backend processing takes.
  // (For debounced text the keeper is (re)started by the debouncer's
  // onBufferStart; starting it here is idempotent.)
  startTypingKeeper(threadID, space);

  try {
    await routeInboundContent(
      { postToBackend, debouncer, log: reqLog },
      { platform, senderId, threadID, spaceId: space.id },
      message,
      content,
    );
  } catch (err) {
    // The turn died before reaching the backend: stop the "..." indicator
    // (else it runs to the 90s deadline on a dead turn) and release the dedup
    // reservation so a redelivered copy of this message can still be processed.
    stopTypingKeeper(threadID);
    failures.record("inbound", message.id ?? threadID, { thread: threadID }, err);
    if (message.id) processedMessageIds.delete(message.id);
    reqLog.error({ err, thread_id: threadID }, "inbound handling failed");
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

  spectrumAgent = agent;
  webhookAgent = agent;

  // The SDK map is keyed by the provider's display name ("iMessage"). Live
  // webhook envelopes use "imessage". Without this alias the SDK returns 200
  // and never calls handleInbound, so the text gets no reply.
  const platformMap = (
    agent as { __internal?: { platforms?: Map<string, unknown> } }
  ).__internal?.platforms;
  if (platformMap) {
    const aliases = aliasProviderPlatformKeys(platformMap);
    if (aliases.length > 0) {
      log.info({ aliases }, "aliased webhook platform keys");
    }
  } else {
    log.warn(
      "spectrum agent exposed no platform map; lowercase webhook platforms will be dropped",
    );
  }

  // Bind cold-handle rehydration now that the platform instance exists.
  // im.space.get is a purely local construction on the remote provider —
  // no network call — so lazy rehydration is cheap.
  const im = imessage(agent);
  spaceResolver.setFetcher((id: string, params?: { phone?: string }) => im.space.get(id, params));

  if (config.SPECTRUM_TRANSPORT_MODE !== "stream" && !config.SPECTRUM_WEBHOOK_SECRET) {
    log.warn(
      "SPECTRUM_WEBHOOK_SECRET is unset: native webhook deliveries will be answered 500 by the SDK. " +
        "Set the secret (or switch to Fusor/stream transport) before expecting webhook inbound.",
    );
  }
  if (config.SPECTRUM_TRANSPORT_MODE === "both") {
    log.warn("transport mode 'both' runs webhook + streaming iterator in parallel (double delivery, dedup load). Prefer 'webhook'.");
  }

  httpServer = app.listen(config.BRIDGE_PORT, () => {
    log.info(
      { port: config.BRIDGE_PORT, transport: config.SPECTRUM_TRANSPORT_MODE },
      "bridge HTTP server listening",
    );
  });

  if (config.SPECTRUM_TRANSPORT_MODE === "webhook") {
    log.info("webhook transport: streaming iterator disabled, inbound arrives via HTTP");
    // Park forever (until shutdown) without opening the streaming connection.
    await new Promise((resolve) => {
      const t = setInterval(() => {
        if (shuttingDown) {
          clearInterval(t);
          resolve(undefined);
        }
      }, 1000);
    });
    return;
  }

  log.info("waiting for provider messages...");

  for await (const [space, message] of agent.messages) {
    if (shuttingDown) break;
    handleInbound(space, message).catch((err) => {
      failures.record("stream", message?.id ?? "unknown", { thread: space?.id }, err);
      log.error({ err }, "inbound handling error");
    });
  }
}

start().catch((err) => {
  log.fatal({ err }, "bridge failed to start");
  process.exit(1);
});

// Graceful shutdown: the SDK owns no signal handlers (it's a library), so the
// process belongs to us. Order matters — stop intake, flush debounced bursts
// to the backend, tear down Spectrum (closes the stream, destroys provider
// clients, flushes telemetry), then persist and exit. Idempotent and
// once-guarded so a second signal during drain doesn't cut it short.
async function shutdown(signal: string): Promise<void> {
  if (shuttingDown) return;
  shuttingDown = true;
  log.info({ signal }, "shutting down...");

  clearInterval(sweeper);
  clearInterval(queueTimer);
  spaceStore.stopAutoSave();
  cardStore.stopAutoSave();
  outboundQueue.stopAutoSave();
  inboundSpool.stopAutoSave();

  try {
    await debouncer.flushAll();
  } catch (err) {
    log.warn({ err }, "debouncer flushAll during shutdown failed");
  }
  // Persist whatever the flush could not deliver (carried batches) before the
  // in-memory buffer is disposed, so the next boot re-arms it.
  await inboundSpool.flush(debouncer.snapshot());
  debouncer.dispose();

  if (spectrumAgent) {
    try {
      await Promise.race([
        spectrumAgent.stop(),
        new Promise((_, reject) => setTimeout(() => reject(new Error("stop timeout")), 5000)),
      ]);
    } catch (err) {
      log.warn({ err }, "spectrum stop failed or timed out");
    }
    spectrumAgent = null;
    webhookAgent = null;
  }

  pacer.dispose();
  for (const threadID of typingKeepers.keys()) stopTypingKeeper(threadID);

  await spaceStore.flush();
  await cardStore.flush();
  await outboundQueue.flush();

  if (httpServer) {
    await new Promise<void>((resolve) => httpServer!.close(() => resolve()));
    httpServer = null;
  }
  process.exit(0);
}

process.once("SIGTERM", () => void shutdown("SIGTERM"));
process.once("SIGINT", () => void shutdown("SIGINT"));

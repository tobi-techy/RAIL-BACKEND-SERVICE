import { readFile, writeFile, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { childLogger } from "./logger";
import type { OutboundMessage } from "./handler";

const log = childLogger({ module: "outbound-queue" });

export type MessageCategory = "critical" | "normal";

export interface QueuedMessage {
  id: string;
  threadId: string;
  msg: OutboundMessage;
  attempts: number;
  nextRetryAt: number;
  expiresAt: number;
  category: MessageCategory;
  createdAt: number;
  /** Stable idempotency key across retries (best-practices/recovery-and-state).
   *  Assigned at enqueue when the producer didn't supply one, so a crash
   *  between provider-ack and bookkeeping still dedups on retry. */
  clientGuid: string;
}

export interface QueueStats {
  totalQueued: number;
  byThread: Record<string, number>;
  oldestMessage?: number;
}

export interface PersistentOutboundQueueOptions {
  maxRetries?: number;
  baseDelayMs?: number;
  criticalTtlMs?: number;
  normalTtlMs?: number;
  autoSaveIntervalMs?: number;
  maxSize?: number;
}

const DEFAULT_OPTIONS: Required<PersistentOutboundQueueOptions> = {
  maxRetries: 5,
  baseDelayMs: 2_000,
  criticalTtlMs: 24 * 60 * 60 * 1000, // 24 hours
  normalTtlMs: 4 * 60 * 60 * 1000,    // 4 hours
  autoSaveIntervalMs: 30_000,
  maxSize: 10_000,
};

function isValidOutboundMessage(msg: unknown): msg is OutboundMessage {
  if (!msg || typeof msg !== "object") return false;
  const m = msg as Record<string, unknown>;
  return typeof m.thread_id === "string" && m.thread_id.length > 0 && typeof m.text === "string";
}

/**
 * PersistentOutboundQueue survives bridge restarts so proactive messages are
 * not lost when the live Space handle is cold. Messages are queued to disk and
 * flushed when the user texts again and the space warms up.
 */
export class PersistentOutboundQueue {
  private records: Map<string, QueuedMessage> = new Map();
  private filePath: string;
  private dirty = false;
  private opts: Required<PersistentOutboundQueueOptions>;
  private saveInterval: ReturnType<typeof setInterval> | null = null;

  constructor(filePath?: string, opts: PersistentOutboundQueueOptions = {}) {
    this.filePath = filePath || path.resolve(process.cwd(), "data", "outbound-queue.json");
    this.opts = { ...DEFAULT_OPTIONS, ...opts };
  }

  async load(): Promise<void> {
    try {
      if (!existsSync(this.filePath)) {
        log.info("no existing outbound queue, starting fresh");
        return;
      }
      const raw = await readFile(this.filePath, "utf-8");
      const parsed = JSON.parse(raw) as QueuedMessage[];
      let dropped = 0;
      const now = Date.now();
      for (const rec of parsed) {
        if (!rec.id || !rec.threadId || !isValidOutboundMessage(rec.msg)) {
          dropped++;
          continue;
        }
        // Drop messages that already expired while the bridge was down.
        if (rec.expiresAt <= now) {
          dropped++;
          continue;
        }
        // Backfill pre-guid records so old queue files stay loadable.
        if (!rec.clientGuid) {
          rec.clientGuid =
            typeof (rec.msg as { client_guid?: unknown }).client_guid === "string" &&
            (((rec.msg as { client_guid?: string }).client_guid as string).length > 0)
              ? ((rec.msg as { client_guid?: string }).client_guid as string)
              : rec.id;
          (rec.msg as { client_guid?: string }).client_guid = rec.clientGuid;
        }
        this.records.set(rec.id, rec);
      }
      log.info({ count: this.records.size, dropped }, "loaded outbound queue from disk");
    } catch (err) {
      log.warn({ err }, "failed to load outbound queue, starting fresh");
      this.records.clear();
    }
  }

  /**
   * Enqueue a message for later delivery. `notBefore` (epoch ms) floors the
   * first attempt — used for backend-scheduled delivery via `send_after`.
   * Returns the queued message id.
   */
  enqueue(msg: OutboundMessage, category: MessageCategory = "normal", opts?: { notBefore?: number }): string {
    if (!isValidOutboundMessage(msg)) {
      log.warn({ msg }, "refusing to enqueue invalid outbound message");
      return "";
    }

    // Enforce a bounded queue: drop the oldest message when over capacity.
    if (this.records.size >= this.opts.maxSize) {
      let oldest: QueuedMessage | undefined;
      for (const rec of this.records.values()) {
        if (!oldest || rec.createdAt < oldest.createdAt) {
          oldest = rec;
        }
      }
      if (oldest) {
        this.records.delete(oldest.id);
        log.warn({ thread_id: oldest.threadId }, "dropped oldest queued message to enforce max size");
      }
    }

    const id = randomUUID();
    const now = Date.now();
    const ttl = category === "critical" ? this.opts.criticalTtlMs : this.opts.normalTtlMs;
    // Stable client GUID: prefer the producer's (retry of the same logical
    // send reuses it), else mint one so queue-level retries stay idempotent.
    const clientGuid =
      typeof (msg as { client_guid?: unknown }).client_guid === "string" &&
      ((msg as { client_guid?: string }).client_guid as string).length > 0
        ? ((msg as { client_guid?: string }).client_guid as string)
        : randomUUID();
    (msg as { client_guid?: string }).client_guid = clientGuid;
    const rec: QueuedMessage = {
      id,
      threadId: msg.thread_id,
      msg,
      attempts: 0,
      nextRetryAt: Math.max(now + this.opts.baseDelayMs, opts?.notBefore ?? 0),
      expiresAt: now + ttl,
      category,
      createdAt: now,
      clientGuid,
    };
    this.records.set(id, rec);
    this.dirty = true;
    log.info(
      { id, thread_id: msg.thread_id, category, expires_in_ms: ttl },
      "message queued for cold space",
    );
    return id;
  }

  /**
   * Remove a message from the queue after successful delivery or max retries.
   */
  remove(id: string): boolean {
    const had = this.records.delete(id);
    if (had) {
      this.dirty = true;
    }
    return had;
  }

  /**
   * Return messages that are ready for a delivery attempt, in insertion order.
   * Expired messages are dropped as a side effect — they can also expire while
   * waiting on a cold space, where no recordAttempt() would ever reap them.
   */
  getReady(now = Date.now()): QueuedMessage[] {
    for (const [id, rec] of this.records) {
      if (rec.expiresAt <= now) {
        this.records.delete(id);
        this.dirty = true;
        log.info({ id, thread_id: rec.threadId, category: rec.category }, "message expired");
      }
    }
    return Array.from(this.records.values())
      .filter((r) => r.nextRetryAt <= now)
      .sort((a, b) => a.createdAt - b.createdAt);
  }

  /**
   * Return all pending messages for a thread, in insertion order.
   */
  getByThread(threadId: string): QueuedMessage[] {
    return Array.from(this.records.values())
      .filter((r) => r.threadId === threadId)
      .sort((a, b) => a.createdAt - b.createdAt);
  }

  /**
   * Record a failed delivery attempt and compute the next retry time.
   * Returns the updated record, or undefined if it has exceeded max retries
   * or expired.
   */
  recordAttempt(id: string, now = Date.now()): QueuedMessage | undefined {
    const rec = this.records.get(id);
    if (!rec) return undefined;

    rec.attempts += 1;

    if (rec.attempts > this.opts.maxRetries || rec.expiresAt <= now) {
      this.records.delete(id);
      this.dirty = true;
      log.warn(
        { id, thread_id: rec.threadId, attempts: rec.attempts, category: rec.category },
        rec.expiresAt <= now ? "message expired" : "message dropped after max retries",
      );
      return undefined;
    }

    const delay = this.opts.baseDelayMs * Math.pow(2, rec.attempts - 1);
    rec.nextRetryAt = now + Math.min(delay, 300_000); // cap at 5 minutes
    this.dirty = true;
    return rec;
  }

  /**
   * Push the next attempt out WITHOUT consuming the retry budget. Used when a
   * send fails for a reason a retry cannot fix — a cold space handle. Burning
   * attempts on that condition used to drop messages ~1 minute after a restart
   * even though their TTL (4h normal / 24h critical) was designed to carry
   * them until the space rehydrates or the user texts.
   *
   * Returns true if the message is still queued, false if it had already
   * expired (in which case it is dropped here).
   */
  defer(id: string, delayMs: number, now = Date.now()): boolean {
    const rec = this.records.get(id);
    if (!rec) return false;
    if (rec.expiresAt <= now) {
      this.records.delete(id);
      this.dirty = true;
      log.info({ id, thread_id: rec.threadId, category: rec.category }, "message expired");
      return false;
    }
    rec.nextRetryAt = now + delayMs;
    this.dirty = true;
    return true;
  }

  /**
   * Promote any ready messages to immediate retry, e.g. when a space warms.
   */
  bumpThread(threadId: string, now = Date.now()): QueuedMessage[] {
    const bumped: QueuedMessage[] = [];
    for (const rec of this.records.values()) {
      if (rec.threadId === threadId && rec.nextRetryAt > now) {
        rec.nextRetryAt = now;
        bumped.push(rec);
        this.dirty = true;
      }
    }
    return bumped;
  }

  getStats(): QueueStats {
    const byThread: Record<string, number> = {};
    let oldestMessage: number | undefined;
    for (const rec of this.records.values()) {
      byThread[rec.threadId] = (byThread[rec.threadId] || 0) + 1;
      if (oldestMessage === undefined || rec.createdAt < oldestMessage) {
        oldestMessage = rec.createdAt;
      }
    }
    return {
      totalQueued: this.records.size,
      byThread,
      oldestMessage,
    };
  }

  startAutoSave(intervalMs = this.opts.autoSaveIntervalMs): void {
    if (this.saveInterval) return;
    this.saveInterval = setInterval(() => this.save(), intervalMs);
  }

  stopAutoSave(): void {
    if (this.saveInterval) {
      clearInterval(this.saveInterval);
      this.saveInterval = null;
    }
  }

  async save(): Promise<void> {
    if (!this.dirty) return;
    try {
      const dir = path.dirname(this.filePath);
      if (!existsSync(dir)) {
        await mkdir(dir, { recursive: true });
      }
      const payload = Array.from(this.records.values()).sort(
        (a, b) => a.createdAt - b.createdAt,
      );
      await writeFile(this.filePath, JSON.stringify(payload, null, 2), "utf-8");
      this.dirty = false;
    } catch (err) {
      log.warn({ err }, "failed to save outbound queue");
    }
  }

  async flush(): Promise<void> {
    await this.save();
  }
}

import type { Space } from "spectrum-ts";
import { childLogger } from "./logger";

const log = childLogger({ module: "space-resolver" });

/** The subset of SpaceRecord the resolver needs. */
export interface SpaceRecordLike {
  space_id: string;
  phone?: string;
  platform?: string;
}

export interface SpaceResolverOptions {
  /**
   * Rebuild a Space handle from a persisted space id. Backed by the SDK's
   * platform-space resolver: "construct a space from a known platform space
   * id — e.g. one persisted from an earlier event" (`im.space.get`). The
   * remote iMessage provider implements it as a purely local construction —
   * no network call — so rehydration is cheap and safe to do lazily.
   */
  get: (id: string, params?: { phone?: string }) => Promise<Space>;
  /** Persisted records, so outbound sends can re-resolve cold threads. */
  lookup: (threadId: string) => SpaceRecordLike | undefined;
  /**
   * Live-handle cache cap. The cache is a convenience (inbound handles are
   * free), not a correctness requirement — evicted entries rehydrate on
   * demand, so FIFO eviction is fine.
   */
  maxLive?: number;
  /** How long a failed rehydration blocks another attempt for that thread. */
  negativeTtlMs?: number;
}

const DEFAULT_MAX_LIVE = 1_000;
const DEFAULT_NEGATIVE_TTL_MS = 60_000;

/**
 * Owns Space handles for outbound sends.
 *
 * Historically the bridge could only send to spaces the user had messaged from
 * in the current process lifetime — proactive messages for any other thread
 * sat in the outbound queue until the user texted again. The SDK can rebuild
 * an iMessage Space from the chat-guid id the SpaceStore already persists, so
 * a handle is never truly lost: `resolve()` serves the live cache, falls back
 * to rehydrating from the persisted record, and caches the result.
 *
 * Rehydration is iMessage-only. The Telegram/WhatsApp providers don't expose a
 * space resolver, so cold threads there still warm on the user's next message.
 * Legacy SpaceStore records predate the `platform` field and are treated as
 * iMessage — they could only have been created by the iMessage-only era.
 */
export class SpaceResolver {
  private readonly live = new Map<string, Space>();
  private readonly failedUntil = new Map<string, number>();
  private readonly maxLive: number;
  private readonly negativeTtlMs: number;
  private readonly opts: Omit<SpaceResolverOptions, "maxLive" | "negativeTtlMs">;

  constructor(opts: SpaceResolverOptions) {
    this.opts = opts;
    this.maxLive = opts.maxLive ?? DEFAULT_MAX_LIVE;
    this.negativeTtlMs = opts.negativeTtlMs ?? DEFAULT_NEGATIVE_TTL_MS;
  }

  /**
   * Swap the rehydration fetcher after construction. The bridge builds the
   * resolver before `Spectrum()` exists (no `im.space.get` yet) and binds the
   * real fetcher once the agent is up. Until then `resolve()` serves the live
   * cache only.
   */
  setFetcher(get: SpaceResolverOptions["get"]): void {
    this.opts.get = get;
    this.failedUntil.clear();
  }

  /** Cache a live handle (inbound stream) or a freshly rehydrated one. */
  prime(threadId: string, space: Space): void {
    // Delete-then-set so re-resolution refreshes recency under FIFO eviction.
    this.live.delete(threadId);
    this.live.set(threadId, space);
    while (this.live.size > this.maxLive) {
      const oldest = this.live.keys().next().value;
      if (oldest === undefined) break;
      this.live.delete(oldest);
    }
  }

  /**
   * Return a Space handle for the thread, rehydrating from the persisted
   * record when the live cache misses. Resolves undefined when the thread is
   * unknown, belongs to a platform without a space resolver, or rehydration
   * recently failed (negative cache — don't hammer the provider between
   * polling ticks).
   */
  async resolve(threadId: string): Promise<Space | undefined> {
    const cached = this.live.get(threadId);
    if (cached) return cached;

    const record = this.opts.lookup(threadId);
    if (!record) return undefined;
    if (record.platform && record.platform !== "imessage") return undefined;

    const blockedUntil = this.failedUntil.get(threadId);
    if (blockedUntil && blockedUntil > Date.now()) return undefined;

    try {
      const space = await this.opts.get(record.space_id, record.phone ? { phone: record.phone } : undefined);
      this.failedUntil.delete(threadId);
      this.prime(threadId, space);
      return space;
    } catch (err) {
      this.failedUntil.set(threadId, Date.now() + this.negativeTtlMs);
      log.warn({ err, thread_id: threadId, space_id: record.space_id }, "space rehydration failed");
      return undefined;
    }
  }

  /** Number of live cached handles (exposed for tests and /health). */
  get size(): number {
    return this.live.size;
  }

  /** Peek the live cache without rehydrating (typing keepers, health). */
  cached(threadId: string): Space | undefined {
    return this.live.get(threadId);
  }
}

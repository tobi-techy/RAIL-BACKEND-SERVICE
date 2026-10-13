// Inbound rate limiting at the bridge edge.
//
// The Go platform only ever sees the bridge's IP, so its per-IP limiter is one
// bucket for every sender. Limiting here, on the provider's sender id, is the
// only place a single noisy sender can be told apart from everyone else.
//
// Two token buckets, checked in this order:
//   1. per sender: one sender's burst is shed before it touches anything shared;
//   2. per bridge: caps the whole instance below the Go limiter's 100/min, so the
//      bridge sheds load itself instead of letting Go answer 429 and retrying.
// A message refused by the sender bucket never spends a bridge token.

export interface RateLimitConfig {
  senderBurst: number;
  senderPerMinute: number;
  bridgeBurst: number;
  bridgePerMinute: number;
}

export type RateDecision = "ok" | "sender_limited" | "bridge_limited";

// Bounds memory: a flood of distinct sender ids cannot grow the map forever.
const MAX_TRACKED_SENDERS = 10_000;

interface Bucket {
  tokens: number;
  updatedAt: number;
}

export class InboundRateLimiter {
  private readonly senders = new Map<string, Bucket>();
  private readonly bridge: Bucket;

  constructor(
    private readonly cfg: RateLimitConfig,
    private readonly now: () => number = Date.now,
  ) {
    this.bridge = { tokens: cfg.bridgeBurst, updatedAt: now() };
  }

  /** Spends one token for `senderKey`, or says which bucket refused. */
  check(senderKey: string): RateDecision {
    const now = this.now();
    const bucket = this.senderBucket(senderKey, now);
    this.refill(bucket, this.cfg.senderBurst, this.cfg.senderPerMinute, now);
    if (bucket.tokens < 1) return "sender_limited";

    this.refill(this.bridge, this.cfg.bridgeBurst, this.cfg.bridgePerMinute, now);
    if (this.bridge.tokens < 1) return "bridge_limited";

    bucket.tokens -= 1;
    this.bridge.tokens -= 1;
    return "ok";
  }

  get trackedSenders(): number {
    return this.senders.size;
  }

  private senderBucket(key: string, now: number): Bucket {
    const existing = this.senders.get(key);
    if (existing) return existing;
    if (this.senders.size >= MAX_TRACKED_SENDERS) this.evictIdle(now);
    const created = { tokens: this.cfg.senderBurst, updatedAt: now };
    this.senders.set(key, created);
    return created;
  }

  // A bucket that has refilled to full is indistinguishable from a new one, so
  // dropping it loses nothing. If none is idle the oldest goes, which can only
  // hand a sender a fresh bucket while the map is under a flood of new ids.
  private evictIdle(now: number): void {
    for (const [key, bucket] of this.senders) {
      this.refill(bucket, this.cfg.senderBurst, this.cfg.senderPerMinute, now);
      if (bucket.tokens >= this.cfg.senderBurst) this.senders.delete(key);
    }
    if (this.senders.size < MAX_TRACKED_SENDERS) return;
    const oldest = this.senders.keys().next().value;
    if (oldest !== undefined) this.senders.delete(oldest);
  }

  private refill(bucket: Bucket, burst: number, perMinute: number, now: number): void {
    const elapsedMs = Math.max(0, now - bucket.updatedAt);
    bucket.tokens = Math.min(burst, bucket.tokens + (elapsedMs * perMinute) / 60_000);
    bucket.updatedAt = now;
  }
}

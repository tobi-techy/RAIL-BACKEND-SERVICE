import crypto from "node:crypto";
import type { NextFunction, Request, Response } from "express";

const FRESHNESS_WINDOW_MS = 5 * 60 * 1000;
const MAX_SEEN_NONCES = 10_000;

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

export interface HmacAuth {
  sign(payload: string, timestamp: string, nonce: string): string;
  headers(payload: string): Record<string, string>;
  verify(payload: string, signature: string, timestamp: string, nonce: string): boolean;
  sweep(): void;
  onEviction?: (dropped: number, remaining: number) => void;
}

/** Timestamped, nonce-protected HMAC-SHA256 over `${timestamp}.${nonce}.${payload}`. */
export function createHmacAuth(secret: string, now: () => number = Date.now): HmacAuth {
  const seenNonces = new Map<string, number>(); // nonce -> expiration timestamp

  const auth: HmacAuth = {
    sign(payload, timestamp, nonce) {
      return crypto.createHmac("sha256", secret).update(`${timestamp}.${nonce}.${payload}`).digest("hex");
    },
    headers(payload) {
      const timestamp = String(Math.floor(now() / 1000));
      const nonce = crypto.randomUUID();
      return {
        "X-HMAC-Timestamp": timestamp,
        "X-HMAC-Nonce": nonce,
        "X-HMAC-SHA256": auth.sign(payload, timestamp, nonce),
      };
    },
    verify(payload, signature, timestamp, nonce) {
      try {
        const ts = parseInt(timestamp, 10);
        if (!Number.isFinite(ts)) return false;
        const tsMs = ts * 1000;
        if (tsMs < now() - FRESHNESS_WINDOW_MS || tsMs > now() + FRESHNESS_WINDOW_MS) return false;
        const expected = auth.sign(payload, timestamp, nonce);
        if (!crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature))) return false;
        // Replay rejection runs only after the HMAC signature is verified so
        // that unauthenticated requests cannot exhaust the nonce store.
        if (seenNonces.has(nonce)) return false;
        evictOldestNoncesIfNeeded();
        seenNonces.set(nonce, tsMs + FRESHNESS_WINDOW_MS);
        return true;
      } catch {
        return false;
      }
    },
    sweep() {
      const t = now();
      for (const [nonce, expiresAt] of seenNonces) {
        if (expiresAt < t) seenNonces.delete(nonce);
      }
    },
  };

  function evictOldestNoncesIfNeeded(): void {
    if (seenNonces.size < MAX_SEEN_NONCES) return;
    // Emergency cleanup: remove oldest nonces by expiration time, keeping ~80%
    // of the limit to avoid thrashing.
    const entries = Array.from(seenNonces.entries());
    const keepCount = Math.floor(MAX_SEEN_NONCES * 0.8);
    const dropCount = Math.max(0, entries.length - keepCount);
    if (dropCount === 0) return;
    quickselect(entries, dropCount);
    for (let i = 0; i < dropCount; i++) seenNonces.delete(entries[i][0]);
    auth.onEviction?.(dropCount, seenNonces.size);
  }

  return auth;
}

/**
 * Express middleware that admits only a request carrying a fresh, unreplayed
 * HMAC signature. `rawBody` is the signed payload: the raw request body for
 * writes, the empty string for bodiless GETs.
 */
export function requireHmac(
  auth: HmacAuth,
  rawBody: (req: Request) => string,
  onReject?: (req: Request) => void,
) {
  return (req: Request, res: Response, next: NextFunction): void => {
    const sig = req.headers["x-hmac-sha256"] as string | undefined;
    const timestamp = req.headers["x-hmac-timestamp"] as string | undefined;
    const nonce = req.headers["x-hmac-nonce"] as string | undefined;
    if (!sig || !timestamp || !nonce || !auth.verify(rawBody(req), sig, timestamp, nonce)) {
      onReject?.(req);
      res.status(401).json({ error: "invalid or missing HMAC signature" });
      return;
    }
    next();
  };
}

/**
 * Mounts GET /health/failures. The records carry job ids, provider error text
 * and payload shapes, so the route answers only to a caller holding the shared
 * HMAC secret (sign an empty body, as /send signs its raw body). Liveness
 * probes use /health, which exposes only the failure count.
 */
export function mountFailureAuditRoute(
  app: Pick<import("express").Express, "get">,
  auth: HmacAuth,
  failures: { recent(limit?: number): unknown[] },
  onReject?: (req: Request) => void,
): void {
  app.get("/health/failures", requireHmac(auth, () => "", onReject), (_req, res) => {
    res.json({ failures: failures.recent(20) });
  });
}

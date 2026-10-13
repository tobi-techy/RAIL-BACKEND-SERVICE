import type { Express, Request } from "express";
import type { HmacAuth } from "./hmac";
import { requireHmac } from "./hmac";
import type { FailureAudit } from "./failure-audit";
import type { QueueStats } from "./outbound-queue";

export interface HealthDeps {
  hmacAuth: HmacAuth;
  failures: Pick<FailureAudit, "recent" | "count">;
  onReject?: (req: Request) => void;
  isShuttingDown: () => boolean;
  transportMode: string;
  webhookEnabled: boolean;
  pollWatcherEnabled: boolean;
  spaces: () => number;
  knownThreads: () => number;
  confirmationCards: () => number;
  queueStats: () => QueueStats;
  pacer: { available(): number; readonly pending: number };
  deliverability: { stats(): unknown };
  inboundBuffers: () => { entries: unknown[]; carried?: unknown[] }[];
  turns: { readonly isEnabled: boolean; readonly trackedThreads: number };
  pollStats: () => Record<string, unknown> | undefined;
}

const EMPTY_POLL_STATS = {
  clients: 0,
  connected: 0,
  tracked: 0,
  votes_forwarded: 0,
  votes_dropped: 0,
  reconciles: 0,
};

/**
 * Registers the probe routes. `/` and `/health` are open for liveness probes
 * and carry counts only: no thread, chat or message identifiers (the queue is
 * reported as a total and a thread count, never keyed by thread id).
 * `/health/failures` returns job ids, provider error text and payload shapes,
 * so it answers only to a caller holding the shared HMAC secret (sign an empty
 * body, as /send signs its raw body).
 */
export function mountHealthRoutes(app: Pick<Express, "get">, deps: HealthDeps): void {
  app.get(["/", "/health"], (_req, res) => {
    const stats = deps.queueStats();
    const inboundBuffered = deps.inboundBuffers();
    res.json({
      status: deps.isShuttingDown() ? "draining" : "ok",
      transport_mode: deps.transportMode,
      webhook_enabled: deps.webhookEnabled,
      spaces: deps.spaces(),
      known_threads: deps.knownThreads(),
      confirmation_cards: deps.confirmationCards(),
      queued_outbound: stats.totalQueued,
      queued_threads: Object.keys(stats.byThread).length,
      queued_oldest_ms: stats.oldestMessage ? Date.now() - stats.oldestMessage : undefined,
      pacer: { available: deps.pacer.available(), pending: deps.pacer.pending },
      deliverability: deps.deliverability.stats(),
      inbound_spool: {
        buffers: inboundBuffered.length,
        entries: inboundBuffered.reduce((n, b) => n + b.entries.length, 0),
        carried: inboundBuffered.reduce((n, b) => n + (b.carried?.length ?? 0), 0),
      },
      turn_supersession: {
        enabled: deps.turns.isEnabled,
        tracked_threads: deps.turns.trackedThreads,
      },
      poll_watcher: {
        enabled: deps.pollWatcherEnabled,
        ...(deps.pollStats() ?? EMPTY_POLL_STATS),
      },
      recent_failures: deps.failures.count(),
      uptime_sec: Math.floor(process.uptime()),
    });
  });

  app.get("/health/failures", requireHmac(deps.hmacAuth, () => "", deps.onReject), (_req, res) => {
    res.json({ failures: deps.failures.recent(20) });
  });
}

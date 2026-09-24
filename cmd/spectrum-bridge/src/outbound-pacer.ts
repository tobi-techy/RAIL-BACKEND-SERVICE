/**
 * Global outbound pacer (token bucket).
 *
 * Photon's iMessage deliverability guidance is blunt: bursty sending gets the
 * line flagged ("bursts of 100/min look automated because they are"), and the
 * platform enforces hard daily caps. Per-conversation pacing — the handler's
 * typing simulation and inter-bubble delays — already humanizes a single
 * reply; this bucket shapes traffic ACROSS conversations so a fan-out
 * (anomaly alerts to many users, a post-restart queue flush) never hits the
 * wire as a burst.
 *
 * Quiet-hours and per-user frequency policy deliberately live in the Go
 * backend (platform.ProactiveGuard), which knows what each message IS — a
 * reply, a briefing, a risk alert. The bridge can't tell those apart, so it
 * only shapes transport-level rate.
 */
export interface OutboundPacerOptions {
  /** Bucket size — sends that fit in the burst go out immediately. */
  capacity: number;
  /** One token is restored every refillIntervalMs. */
  refillIntervalMs: number;
}

export class OutboundPacer {
  private tokens: number;
  private waiters: Array<() => void> = [];
  private refillTimer: NodeJS.Timeout;
  private readonly opts: OutboundPacerOptions;

  constructor(opts: OutboundPacerOptions) {
    this.opts = opts;
    this.tokens = opts.capacity;
    // unref'd: the pacer must never keep the process alive on its own.
    this.refillTimer = setInterval(() => this.refill(), opts.refillIntervalMs);
    this.refillTimer.unref?.();
  }

  private refill(): void {
    this.tokens = Math.min(this.opts.capacity, this.tokens + 1);
    // Invariant: tokens > 0 implies no waiters, so one admission per token.
    if (this.tokens > 0 && this.waiters.length > 0) {
      const wake = this.waiters.shift();
      this.tokens -= 1;
      wake?.();
    }
  }

  /** Resolve once a send slot is available, consuming it. FIFO fairness. */
  async acquire(): Promise<void> {
    if (this.tokens > 0) {
      this.tokens -= 1;
      return;
    }
    await new Promise<void>((resolve) => this.waiters.push(resolve));
  }

  /** Tokens currently available (exposed for tests and /health). */
  available(): number {
    return this.tokens;
  }

  /** Outstanding waiters (exposed for tests and /health). */
  get pending(): number {
    return this.waiters.length;
  }

  dispose(): void {
    clearInterval(this.refillTimer);
    for (const wake of this.waiters.splice(0)) wake();
  }
}

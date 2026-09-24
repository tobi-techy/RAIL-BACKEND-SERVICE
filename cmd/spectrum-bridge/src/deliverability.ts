import { childLogger } from "./logger";

const log = childLogger({ module: "deliverability" });

export interface DeliverabilityOptions {
  /** Hard cap: every outbound send across all chats per server per day. */
  dailyOutboundCap?: number;
  /** New conversations a single line may initiate per day. */
  newConvosPerLinePerDay?: number;
}

const DEFAULT_DAILY_CAP = 5_000;
const DEFAULT_NEW_CONVOS = 50;

function dayKey(d = new Date()): string {
  return d.toISOString().slice(0, 10);
}

/**
 * Transport-level guardrails from Photon's iMessage deliverability guidance.
 * Apple/Photon enforce 5,000 outbound/server/day (hard, ban risk) and 50 new
 * conversations/line/day. The bridge can't judge message *value* (that lives
 * in the Go ProactiveGuard) — it only stops the wire from ever bursting past
 * the platform caps. Over-cap sends are rejected so the caller queues them
 * instead of burning the line.
 */
export class DeliverabilityTracker {
  private day = dayKey();
  private outboundToday = 0;
  /** line(phone|platform) -> {day, count} */
  private newConvos = new Map<string, { day: string; count: number }>();
  private readonly dailyCap: number;
  private readonly newConvoCap: number;

  constructor(opts: DeliverabilityOptions = {}) {
    this.dailyCap = opts.dailyOutboundCap ?? DEFAULT_DAILY_CAP;
    this.newConvoCap = opts.newConvosPerLinePerDay ?? DEFAULT_NEW_CONVOS;
  }

  private rollover(): void {
    const today = dayKey();
    if (today !== this.day) {
      this.day = today;
      this.outboundToday = 0;
      this.newConvos.clear();
    }
  }

  /** True when another outbound send fits under the daily server cap. */
  checkOutbound(): boolean {
    this.rollover();
    return this.outboundToday < this.dailyCap;
  }

  /** Record a completed provider send. */
  recordOutbound(count = 1): void {
    this.rollover();
    this.outboundToday += count;
    if (this.outboundToday >= this.dailyCap * 0.8) {
      log.warn({ outbound_today: this.outboundToday, cap: this.dailyCap }, "approaching daily outbound cap");
    }
  }

  /**
   * True when `line` may still open new conversations today. Replies inside
   * existing conversations never consult this — only first-contact sends.
   */
  checkNewConvo(line: string): boolean {
    this.rollover();
    const rec = this.newConvos.get(line);
    if (!rec || rec.day !== this.day) return true;
    return rec.count < this.newConvoCap;
  }

  recordNewConvo(line: string): void {
    this.rollover();
    const rec = this.newConvos.get(line);
    if (!rec || rec.day !== this.day) {
      this.newConvos.set(line, { day: this.day, count: 1 });
      return;
    }
    rec.count += 1;
  }

  stats(): { day: string; outbound_today: number; daily_cap: number; new_convo_cap: number } {
    this.rollover();
    return {
      day: this.day,
      outbound_today: this.outboundToday,
      daily_cap: this.dailyCap,
      new_convo_cap: this.newConvoCap,
    };
  }
}

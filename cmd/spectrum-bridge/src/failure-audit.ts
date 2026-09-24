import { childLogger } from "./logger";

const log = childLogger({ module: "failure-audit" });

export interface FailureRecord {
  at: string;
  queue: string;
  job_id: string;
  /** Truncated payload summary — never full base64 bodies. */
  payload_summary: string;
  error: string;
}

const MAX_RECORDS = 500;
const RETENTION_MS = 30 * 24 * 60 * 60 * 1000;

function summarizePayload(payload: unknown): string {
  try {
    const s = JSON.stringify(payload, (k, v) =>
      typeof v === "string" && v.length > 200 ? `${k}:<${v.length} chars>` : v,
    );
    return s.slice(0, 1_000);
  } catch {
    return "<unserializable payload>";
  }
}

/**
 * Durable failure audit log (best-practices/recovery-and-state). Every error
 * path records which queue/job failed, with what payload summary, and why —
 * so "are all failures one chat / one stage?" is answerable without grepping
 * rotating logs. Bounded (500 entries) with 30-day retention. Fail-safe: never
 * throws — a failed failure-record must not take down the worker.
 */
export class FailureAudit {
  private records: FailureRecord[] = [];

  record(queue: string, jobId: string, payload: unknown, err: unknown): void {
    try {
      const error = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
      this.records.push({
        at: new Date().toISOString(),
        queue: queue.slice(0, 64),
        job_id: jobId.slice(0, 128),
        payload_summary: summarizePayload(payload),
        error: error.slice(0, 500),
      });
      const cutoff = Date.now() - RETENTION_MS;
      this.records = this.records
        .filter((r) => Date.parse(r.at) >= cutoff)
        .slice(-MAX_RECORDS);
    } catch (e) {
      log.warn({ e }, "failure-audit record failed (fail-safe)");
    }
  }

  recent(limit = 20): FailureRecord[] {
    return this.records.slice(-limit).reverse();
  }

  count(): number {
    return this.records.length;
  }
}

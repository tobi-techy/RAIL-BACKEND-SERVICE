import { readFile, writeFile, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { childLogger } from "./logger";
import type { PersistedInboundBuffer } from "./inbound";

const log = childLogger({ module: "inbound-spool" });

/**
 * Durable spool for the inbound debounce buffer (best-practices/recovery-and-state).
 *
 * The debouncer holds the user's burst in memory until the quiet window closes.
 * A hard crash in that window used to lose it. This spool mirrors the
 * outbound-queue shape — load on boot, autosave while running, flush on
 * shutdown — so a restarted bridge re-arms the batch and the words still reach
 * the backend instead of vanishing.
 *
 * Writes are de-duplicated against the last serialized value, so the autosave
 * interval is cheap even though the debouncer is snapshotted on every tick.
 */
export class InboundSpool {
  private filePath: string;
  private lastJson: string | null = null;
  private saveInterval: ReturnType<typeof setInterval> | null = null;

  constructor(filePath?: string) {
    this.filePath =
      filePath || path.resolve(process.cwd(), "data", "inbound-spool.json");
  }

  /** Read the last persisted buffers. A missing/corrupt file is a fresh start. */
  async load(): Promise<PersistedInboundBuffer[]> {
    try {
      if (!existsSync(this.filePath)) {
        log.info("no existing inbound spool, starting fresh");
        return [];
      }
      const raw = await readFile(this.filePath, "utf-8");
      this.lastJson = raw;
      const parsed = JSON.parse(raw) as PersistedInboundBuffer[];
      const kept = Array.isArray(parsed)
        ? parsed.filter(
            (b) =>
              b &&
              typeof b.key === "string" &&
              ((Array.isArray(b.entries) && b.entries.length > 0) ||
                (Array.isArray(b.carried) && b.carried.length > 0)),
          )
        : [];
      log.info(
        {
          buffers: kept.length,
          entries: kept.reduce((n, b) => n + (b.entries?.length ?? 0), 0),
          carried: kept.reduce((n, b) => n + (b.carried?.length ?? 0), 0),
        },
        "loaded inbound spool from disk",
      );
      return kept;
    } catch (err) {
      log.warn({ err }, "failed to load inbound spool, starting fresh");
      return [];
    }
  }

  /** Persist a snapshot, skipping the write when nothing changed. */
  async save(buffers: PersistedInboundBuffer[]): Promise<void> {
    let json: string;
    try {
      json = JSON.stringify(buffers, null, 2);
    } catch (err) {
      log.warn({ err }, "failed to serialize inbound spool");
      return;
    }
    if (json === this.lastJson) return;
    try {
      const dir = path.dirname(this.filePath);
      if (!existsSync(dir)) await mkdir(dir, { recursive: true });
      await writeFile(this.filePath, json, "utf-8");
      this.lastJson = json;
    } catch (err) {
      log.warn({ err }, "failed to save inbound spool");
    }
  }

  /**
   * Persist `snapshot()` on an interval. The interval is short (default 2s)
   * because the debounce window itself is only a few seconds — we want the
   * buffered words on disk well before the flush fires.
   */
  startAutoSave(
    snapshot: () => PersistedInboundBuffer[],
    intervalMs = 2_000,
  ): void {
    if (this.saveInterval) return;
    this.saveInterval = setInterval(() => void this.save(snapshot()), intervalMs);
  }

  stopAutoSave(): void {
    if (this.saveInterval) {
      clearInterval(this.saveInterval);
      this.saveInterval = null;
    }
  }

  /** Snapshot and persist in one step (shutdown path). */
  async flush(snapshot: PersistedInboundBuffer[]): Promise<void> {
    await this.save(snapshot);
  }
}

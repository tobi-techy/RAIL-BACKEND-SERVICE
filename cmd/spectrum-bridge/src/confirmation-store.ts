import { readFile, writeFile, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { childLogger } from "./logger";

const log = childLogger({ module: "confirmation-store" });

export interface ConfirmationCardRecord {
  action_id: string;
  thread_id: string;
  /** Provider message id of the live card bubble (for space.getMessage + edit). */
  message_id: string;
  state: string;
  updated_at: string;
}

/**
 * Persists live-card handles by action_id so approve / reject / expire / fill
 * edits can mutate the SAME card — including after a bridge restart. Without
 * this, an edit after a restart would have no target and the backend would be
 * forced to choose between a stale card and a duplicate bubble.
 */
export class ConfirmationCardStore {
  private records: Map<string, ConfirmationCardRecord> = new Map();
  private filePath: string;
  private dirty = false;
  private saveInterval: ReturnType<typeof setInterval> | null = null;

  constructor(filePath?: string) {
    this.filePath = filePath || path.resolve(process.cwd(), "data", "confirmation-cards.json");
  }

  async load(): Promise<void> {
    try {
      if (!existsSync(this.filePath)) {
        log.info("no existing confirmation card store, starting fresh");
        return;
      }
      const raw = await readFile(this.filePath, "utf-8");
      const parsed = JSON.parse(raw) as ConfirmationCardRecord[];
      for (const rec of parsed) {
        if (rec?.action_id && rec?.message_id) this.records.set(rec.action_id, rec);
      }
      log.info({ count: this.records.size }, "loaded confirmation card handles from disk");
    } catch (err) {
      log.warn({ err }, "failed to load confirmation card store, starting fresh");
      this.records.clear();
    }
  }

  record(actionId: string, threadId: string, messageId: string, state: string): void {
    this.records.set(actionId, {
      action_id: actionId,
      thread_id: threadId,
      message_id: messageId,
      state,
      updated_at: new Date().toISOString(),
    });
    this.dirty = true;
  }

  get(actionId: string): ConfirmationCardRecord | undefined {
    return this.records.get(actionId);
  }

  count(): number {
    return this.records.size;
  }

  startAutoSave(intervalMs = 30_000): void {
    if (this.saveInterval) return;
    this.saveInterval = setInterval(() => void this.save(), intervalMs);
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
      if (!existsSync(dir)) await mkdir(dir, { recursive: true });
      await writeFile(this.filePath, JSON.stringify(Array.from(this.records.values()), null, 2), "utf-8");
      this.dirty = false;
    } catch (err) {
      log.warn({ err }, "failed to save confirmation card store");
    }
  }

  async flush(): Promise<void> {
    await this.save();
  }
}

import { readFile, writeFile, mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { childLogger } from "./logger";

const log = childLogger({ module: "space-store" });

export interface SpaceRecord {
  thread_id: string;
  space_id: string;
  /** Platform key ("imessage" | "telegram" | "whatsapp"). Legacy records
   *  predate this field and are always iMessage. */
  platform?: string;
  /** The bot line this chat is pinned to (iMessage). The SDK's space resolver
   *  needs it to rebuild a handle once the project grows past one line. */
  phone?: string;
  first_seen: string;
  last_active: string;
}

export class SpaceStore {
  private records: Map<string, SpaceRecord> = new Map();
  private filePath: string;
  private dirty = false;
  private saveInterval: ReturnType<typeof setInterval> | null = null;

  constructor(filePath?: string) {
    this.filePath = filePath || path.resolve(process.cwd(), "data", "spaces.json");
  }

  async load(): Promise<void> {
    try {
      if (!existsSync(this.filePath)) {
        log.info("no existing space store, starting fresh");
        return;
      }
      const raw = await readFile(this.filePath, "utf-8");
      const parsed = JSON.parse(raw) as SpaceRecord[];
      for (const rec of parsed) {
        this.records.set(rec.thread_id, rec);
      }
      log.info({ count: this.records.size }, "loaded known spaces from disk");
    } catch (err) {
      log.warn({ err }, "failed to load space store, starting fresh");
      this.records.clear();
    }
  }

  register(
    threadID: string,
    spaceID: string,
    meta?: { platform?: string; phone?: string },
  ): boolean {
    const now = new Date().toISOString();
    const existing = this.records.get(threadID);
    if (existing) {
      existing.last_active = now;
      existing.space_id = spaceID;
      if (meta?.platform) existing.platform = meta.platform;
      if (meta?.phone) existing.phone = meta.phone;
      return false;
    }
    this.records.set(threadID, {
      thread_id: threadID,
      space_id: spaceID,
      platform: meta?.platform,
      phone: meta?.phone,
      first_seen: now,
      last_active: now,
    });
    this.dirty = true;
    return true;
  }

  get(threadID: string): SpaceRecord | undefined {
    return this.records.get(threadID);
  }

  has(threadID: string): boolean {
    return this.records.has(threadID);
  }

  getAll(): SpaceRecord[] {
    return Array.from(this.records.values());
  }

  count(): number {
    return this.records.size;
  }

  startAutoSave(intervalMs = 30_000): void {
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
      await writeFile(this.filePath, JSON.stringify(this.getAll(), null, 2), "utf-8");
      this.dirty = false;
    } catch (err) {
      log.warn({ err }, "failed to save space store");
    }
  }

  async flush(): Promise<void> {
    await this.save();
  }
}

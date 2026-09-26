import { describe, expect, it } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { InboundSpool } from "./inbound-spool";
import type { PersistedInboundBuffer } from "./inbound";

function buffer(text: string): PersistedInboundBuffer {
  return {
    key: "chat-1",
    entries: [
      {
        platform: "imessage",
        user_id: "+15551234567",
        thread_id: "chat-1",
        space_id: "chat-1",
        text,
        msg_id: "m1",
      },
    ],
    firstAt: Date.now(),
    retries: 0,
  };
}

describe("InboundSpool", () => {
  it("round-trips buffered batches across an instance", async () => {
    const dir = await mkdtemp(path.join(tmpdir(), "inbound-spool-"));
    const file = path.join(dir, "spool.json");
    try {
      const s1 = new InboundSpool(file);
      expect(await s1.load()).toEqual([]);
      await s1.save([buffer("survives a restart")]);

      const s2 = new InboundSpool(file);
      const loaded = await s2.load();
      expect(loaded.length).toBe(1);
      expect(loaded[0].entries[0].text).toBe("survives a restart");
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("skips the write when the snapshot is unchanged", async () => {
    const dir = await mkdtemp(path.join(tmpdir(), "inbound-spool-"));
    const file = path.join(dir, "spool.json");
    try {
      const s = new InboundSpool(file);
      await s.load();
      const buffers = [buffer("first")];
      await s.save(buffers);
      const before = await readFile(file, "utf-8");
      await s.save(buffers); // identical snapshot → no-op write
      const after = await readFile(file, "utf-8");
      expect(after).toBe(before);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("drops empty buffers on load", async () => {
    const dir = await mkdtemp(path.join(tmpdir(), "inbound-spool-"));
    const file = path.join(dir, "spool.json");
    try {
      const s1 = new InboundSpool(file);
      await s1.save([{ key: "chat-1", entries: [], firstAt: Date.now(), retries: 0 }]);
      const s2 = new InboundSpool(file);
      expect(await s2.load()).toEqual([]);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
});

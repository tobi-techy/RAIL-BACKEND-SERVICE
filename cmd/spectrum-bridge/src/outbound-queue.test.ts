import { describe, expect, it, beforeEach, afterEach } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { PersistentOutboundQueue, type MessageCategory } from "./outbound-queue";
import type { OutboundMessage } from "./handler";

function makeMsg(threadId: string, text: string): OutboundMessage {
  return {
    platform: "imessage",
    user_id: "user-1",
    thread_id: threadId,
    text,
    content_type: "markdown",
  };
}

describe("PersistentOutboundQueue", () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = mkdtempSync(path.join(tmpdir(), "bridge-queue-"));
  });

  afterEach(() => {
    rmSync(tmpDir, { recursive: true, force: true });
  });

  it("enqueues, loads, and removes messages", async () => {
    const filePath = path.join(tmpDir, "queue.json");
    const q1 = new PersistentOutboundQueue(filePath, {
      normalTtlMs: 60_000,
      criticalTtlMs: 60_000,
    });
    await q1.load();

    const msg = makeMsg("thread-a", "hello");
    const id = q1.enqueue(msg, "normal");
    await q1.flush();

    const q2 = new PersistentOutboundQueue(filePath, {
      normalTtlMs: 60_000,
      criticalTtlMs: 60_000,
    });
    await q2.load();

    expect(q2.getStats().totalQueued).toBe(1);
    expect(q2.getByThread("thread-a")).toHaveLength(1);
    expect(q2.getByThread("thread-a")[0].id).toBe(id);

    q2.remove(id);
    expect(q2.getStats().totalQueued).toBe(0);
  });

  it("drops expired messages on load", async () => {
    const filePath = path.join(tmpDir, "queue.json");
    const q1 = new PersistentOutboundQueue(filePath, {
      normalTtlMs: 1,
      criticalTtlMs: 1,
    });
    await q1.load();
    q1.enqueue(makeMsg("thread-a", "expired"), "normal");
    await q1.flush();

    // Small sleep so the message expires before reload.
    await new Promise((resolve) => setTimeout(resolve, 10));

    const q2 = new PersistentOutboundQueue(filePath, {
      normalTtlMs: 1,
      criticalTtlMs: 1,
    });
    await q2.load();
    expect(q2.getStats().totalQueued).toBe(0);
  });

  it("returns ready messages sorted by creation time", async () => {
    const q = new PersistentOutboundQueue(path.join(tmpDir, "queue.json"), {
      baseDelayMs: 0,
      normalTtlMs: 60_000,
    });
    await q.load();

    q.enqueue(makeMsg("thread-a", "first"), "normal");
    await new Promise((resolve) => setTimeout(resolve, 5));
    q.enqueue(makeMsg("thread-a", "second"), "normal");

    const ready = q.getReady();
    expect(ready).toHaveLength(2);
    expect(ready[0].msg.text).toBe("first");
    expect(ready[1].msg.text).toBe("second");
  });

  it("records attempts with exponential backoff and drops after max retries", async () => {
    const q = new PersistentOutboundQueue(path.join(tmpDir, "queue.json"), {
      maxRetries: 2,
      baseDelayMs: 1,
      normalTtlMs: 60_000,
    });
    await q.load();

    const id = q.enqueue(makeMsg("thread-a", "retry me"), "normal");
    const now = Date.now();

    const rec1 = q.recordAttempt(id, now);
    expect(rec1).toBeDefined();
    expect(rec1!.attempts).toBe(1);
    expect(rec1!.nextRetryAt).toBeGreaterThan(now);

    const rec2 = q.recordAttempt(id, now + 10);
    expect(rec2).toBeDefined();
    expect(rec2!.attempts).toBe(2);

    const rec3 = q.recordAttempt(id, now + 20);
    expect(rec3).toBeUndefined();
    expect(q.getStats().totalQueued).toBe(0);
  });

  it("bumps all thread messages to immediate retry", async () => {
    const q = new PersistentOutboundQueue(path.join(tmpDir, "queue.json"), {
      baseDelayMs: 60_000,
      normalTtlMs: 120_000,
    });
    await q.load();

    const id = q.enqueue(makeMsg("thread-a", "bump me"), "normal");
    const bumped = q.bumpThread("thread-a");

    expect(bumped).toHaveLength(1);
    expect(bumped[0].id).toBe(id);
    expect(bumped[0].nextRetryAt).toBeLessThanOrEqual(Date.now());
  });

  it("does not bump messages for other threads", async () => {
    const q = new PersistentOutboundQueue(path.join(tmpDir, "queue.json"), {
      baseDelayMs: 60_000,
      normalTtlMs: 120_000,
    });
    await q.load();

    q.enqueue(makeMsg("thread-a", "a"), "normal");
    const bumped = q.bumpThread("thread-b");
    expect(bumped).toHaveLength(0);
  });

  it("uses longer TTL for critical messages", async () => {
    const q = new PersistentOutboundQueue(path.join(tmpDir, "queue.json"), {
      baseDelayMs: 0,
      normalTtlMs: 1_000,
      criticalTtlMs: 60_000,
    });
    await q.load();

    const normalId = q.enqueue(makeMsg("thread-a", "normal"), "normal");
    const criticalId = q.enqueue(makeMsg("thread-a", "critical"), "critical");

    const normalRec = q.getReady().find((r) => r.id === normalId)!;
    const criticalRec = q.getReady().find((r) => r.id === criticalId)!;

    expect(criticalRec.expiresAt - criticalRec.createdAt).toBe(60_000);
    expect(normalRec.expiresAt - normalRec.createdAt).toBe(1_000);
  });
});

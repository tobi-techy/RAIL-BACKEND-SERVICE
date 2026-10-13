import { afterEach, describe, expect, it } from "bun:test";
import express from "express";
import { readFileSync } from "node:fs";
import type { Server } from "node:http";
import { FailureAudit } from "./failure-audit";
import { mountHealthRoutes, type HealthDeps } from "./health";
import { createHmacAuth } from "./hmac";

const SECRET = "test-only-hmac-secret-0123456789abcdef";
const CHAT_GUID = "iMessage;-;+15551234567";

let server: Server | undefined;
afterEach(() => {
  server?.close();
  server = undefined;
});

async function start(overrides: Partial<HealthDeps> = {}) {
  const hmacAuth = createHmacAuth(SECRET);
  const failures = new FailureAudit();
  failures.record("outbound", "job-1", { to: "x", body: "y".repeat(500) }, new Error("provider said no"));
  const app = express();
  mountHealthRoutes(app, {
    hmacAuth,
    failures,
    isShuttingDown: () => false,
    transportMode: "webhook",
    webhookEnabled: true,
    pollWatcherEnabled: true,
    spaces: () => 2,
    knownThreads: () => 3,
    confirmationCards: () => 1,
    queueStats: () => ({ totalQueued: 4, byThread: { [CHAT_GUID]: 3, "iMessage;-;+15557654321": 1 } }),
    pacer: { available: () => 10, pending: 0 },
    deliverability: { stats: () => ({ outbound_today: 7 }) },
    inboundBuffers: () => [{ entries: [1, 2], carried: [3] }],
    turns: { isEnabled: false, trackedThreads: 0 },
    pollStats: () => undefined,
    ...overrides,
  });
  await new Promise<void>((resolve) => {
    server = app.listen(0, resolve);
  });
  const port = (server!.address() as { port: number }).port;
  return { base: `http://127.0.0.1:${port}`, hmacAuth };
}

describe("GET /health/failures (real route registration)", () => {
  it("refuses an unauthenticated request and leaks no record", async () => {
    const { base } = await start();
    const res = await fetch(`${base}/health/failures`);
    expect(res.status).toBe(401);
    const text = await res.text();
    expect(text).not.toContain("job-1");
    expect(text).not.toContain("provider said no");
  });

  it("refuses a wrong signature, a stale timestamp, a replayed nonce and another secret", async () => {
    const { base, hmacAuth } = await start();

    const bad = hmacAuth.headers("");
    bad["X-HMAC-SHA256"] = "0".repeat(64);
    expect((await fetch(`${base}/health/failures`, { headers: bad })).status).toBe(401);

    const staleTs = String(Math.floor(Date.now() / 1000) - 3600);
    const stale = {
      "X-HMAC-Timestamp": staleTs,
      "X-HMAC-Nonce": "stale-nonce",
      "X-HMAC-SHA256": hmacAuth.sign("", staleTs, "stale-nonce"),
    };
    expect((await fetch(`${base}/health/failures`, { headers: stale })).status).toBe(401);

    const once = hmacAuth.headers("");
    expect((await fetch(`${base}/health/failures`, { headers: once })).status).toBe(200);
    expect((await fetch(`${base}/health/failures`, { headers: once })).status).toBe(401);

    const other = createHmacAuth("some-other-secret-0123456789abcdef-xyz");
    expect((await fetch(`${base}/health/failures`, { headers: other.headers("") })).status).toBe(401);
  });

  it("serves the records to a correctly signed request", async () => {
    const { base, hmacAuth } = await start();
    const res = await fetch(`${base}/health/failures`, { headers: hmacAuth.headers("") });
    expect(res.status).toBe(200);
    const body = (await res.json()) as { failures: { job_id: string }[] };
    expect(body.failures[0].job_id).toBe("job-1");
  });
});

describe("GET /health and / (the open liveness probe)", () => {
  it("answers 200 with counts only, never a thread or chat identifier", async () => {
    const { base } = await start();
    for (const path of ["/health", "/"]) {
      const res = await fetch(`${base}${path}`);
      expect(res.status).toBe(200);
      const text = await res.text();
      const body = JSON.parse(text) as Record<string, unknown>;
      expect(body.status).toBe("ok");
      expect(body.queued_outbound).toBe(4);
      expect(body.queued_threads).toBe(2);
      expect(body).not.toHaveProperty("queued_by_thread");
      expect(text).not.toContain(CHAT_GUID);
      expect(text).not.toContain("+1555");
      expect(text).not.toContain("job-1");
      expect(text).not.toContain("provider said no");
    }
  });
});

// index.ts has side effects at import (config, Spectrum SDK, timers), so the
// real routes are tested above through the module index.ts mounts them with.
// This pins that wiring: index.ts must mount through mountHealthRoutes and must
// not register its own /health* routes beside it.
describe("index.ts wiring", () => {
  const source = readFileSync(new URL("./index.ts", import.meta.url), "utf8");

  it("mounts the probe routes through mountHealthRoutes with the HMAC auth", () => {
    expect(source).toMatch(/mountHealthRoutes\(app,\s*\{[\s\S]*?hmacAuth,/);
  });

  it("does not register an ungated /health or /health/failures route itself", () => {
    expect(source).not.toMatch(/app\.get\(\s*\[?\s*["']\/health/);
    expect(source).not.toMatch(/app\.get\(\s*\[?\s*["']\/["']/);
  });
});

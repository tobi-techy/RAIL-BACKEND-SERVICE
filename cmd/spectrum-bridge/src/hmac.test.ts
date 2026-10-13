import { afterEach, describe, expect, it } from "bun:test";
import express from "express";
import type { Server } from "node:http";
import { FailureAudit } from "./failure-audit";
import { createHmacAuth, mountFailureAuditRoute } from "./hmac";

const SECRET = "test-only-hmac-secret-0123456789abcdef";

let server: Server | undefined;
afterEach(() => {
  server?.close();
  server = undefined;
});

async function start(auth = createHmacAuth(SECRET)) {
  const failures = new FailureAudit();
  failures.record("outbound", "job-1", { to: "x", body: "y".repeat(500) }, new Error("provider said no"));
  const app = express();
  // Mirrors index.ts: /health is open, /health/failures is mounted via the shared helper.
  app.get("/health", (_req, res) => res.json({ status: "ok", recent_failures: failures.count() }));
  mountFailureAuditRoute(app, auth, failures);
  await new Promise<void>((resolve) => {
    server = app.listen(0, resolve);
  });
  const port = (server!.address() as { port: number }).port;
  return { base: `http://127.0.0.1:${port}`, auth };
}

describe("GET /health/failures", () => {
  it("refuses an unauthenticated request and leaks no record", async () => {
    const { base } = await start();
    const res = await fetch(`${base}/health/failures`);
    expect(res.status).toBe(401);
    const text = await res.text();
    expect(text).not.toContain("job-1");
    expect(text).not.toContain("provider said no");
  });

  it("refuses a wrong signature, a stale timestamp and a replayed nonce", async () => {
    const { base, auth } = await start();

    const bad = auth.headers("");
    bad["X-HMAC-SHA256"] = "0".repeat(64);
    expect((await fetch(`${base}/health/failures`, { headers: bad })).status).toBe(401);

    const staleTs = String(Math.floor(Date.now() / 1000) - 3600);
    const staleHeaders = {
      "X-HMAC-Timestamp": staleTs,
      "X-HMAC-Nonce": "stale-nonce",
      "X-HMAC-SHA256": auth.sign("", staleTs, "stale-nonce"),
    };
    expect((await fetch(`${base}/health/failures`, { headers: staleHeaders })).status).toBe(401);

    const once = auth.headers("");
    expect((await fetch(`${base}/health/failures`, { headers: once })).status).toBe(200);
    expect((await fetch(`${base}/health/failures`, { headers: once })).status).toBe(401);
  });

  it("refuses a signature made with a different secret", async () => {
    const { base } = await start();
    const other = createHmacAuth("some-other-secret-0123456789abcdef-xyz");
    expect((await fetch(`${base}/health/failures`, { headers: other.headers("") })).status).toBe(401);
  });

  it("serves the records to a correctly signed request", async () => {
    const { base, auth } = await start();
    const res = await fetch(`${base}/health/failures`, { headers: auth.headers("") });
    expect(res.status).toBe(200);
    const body = (await res.json()) as { failures: { job_id: string }[] };
    expect(body.failures[0].job_id).toBe("job-1");
  });

  it("keeps the /health liveness probe open and free of record bodies", async () => {
    const { base } = await start();
    const res = await fetch(`${base}/health`);
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(JSON.parse(text)).toEqual({ status: "ok", recent_failures: 1 });
    expect(text).not.toContain("job-1");
  });
});

describe("createHmacAuth", () => {
  it("accepts a fresh signature once and rejects the replay", () => {
    const auth = createHmacAuth(SECRET);
    const h = auth.headers("payload");
    expect(auth.verify("payload", h["X-HMAC-SHA256"], h["X-HMAC-Timestamp"], h["X-HMAC-Nonce"])).toBe(true);
    expect(auth.verify("payload", h["X-HMAC-SHA256"], h["X-HMAC-Timestamp"], h["X-HMAC-Nonce"])).toBe(false);
  });

  it("rejects a payload that differs from the signed one", () => {
    const auth = createHmacAuth(SECRET);
    const h = auth.headers("payload");
    expect(auth.verify("tampered", h["X-HMAC-SHA256"], h["X-HMAC-Timestamp"], h["X-HMAC-Nonce"])).toBe(false);
  });
});

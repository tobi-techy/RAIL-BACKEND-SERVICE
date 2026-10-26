import { afterAll, beforeAll, describe, expect, it } from "bun:test";
import {
  DOCUMENT_TIMEOUT_MS,
  backendTimeoutMs,
  fetchWithBudget,
  maxAttemptsFor,
} from "./backend-timeout";

const cfg = { textMs: 15_000, photoMs: 90_000 };

describe("backendTimeoutMs", () => {
  it("gives a photo its own budget, longer than text", () => {
    expect(backendTimeoutMs({ is_image: true }, cfg)).toBe(90_000);
    expect(backendTimeoutMs({ text: "hi" }, cfg)).toBe(15_000);
    expect(backendTimeoutMs({ is_image: true }, cfg)).toBeGreaterThan(
      backendTimeoutMs({ text: "hi" }, cfg),
    );
  });

  it("keeps the statement budget and lets a document win over an image flag", () => {
    expect(backendTimeoutMs({ is_document: true }, cfg)).toBe(DOCUMENT_TIMEOUT_MS);
    expect(backendTimeoutMs({ is_document: true, is_image: true }, cfg)).toBe(DOCUMENT_TIMEOUT_MS);
  });

  it("leaves voice notes, poll votes and non-objects on the text budget", () => {
    expect(backendTimeoutMs({ is_voice: true }, cfg)).toBe(15_000);
    expect(backendTimeoutMs({ is_poll_vote: true, text: "Confirm" }, cfg)).toBe(15_000);
    expect(backendTimeoutMs(null, cfg)).toBe(15_000);
    expect(backendTimeoutMs([{ is_image: true }], cfg)).toBe(15_000);
  });

  it("follows the configured photo timeout", () => {
    expect(backendTimeoutMs({ is_image: true }, { textMs: 15_000, photoMs: 45_000 })).toBe(45_000);
  });
});

describe("maxAttemptsFor", () => {
  it("retries media once (each retry re-runs the OCR) and text twice", () => {
    expect(maxAttemptsFor({ is_image: true })).toBe(2);
    expect(maxAttemptsFor({ is_document: true })).toBe(2);
    expect(maxAttemptsFor({ text: "hi" })).toBe(3);
  });
});

// A backend that answers after `delay` ms, standing in for a slow OCR read.
describe("a slow OCR call", () => {
  let server: ReturnType<typeof Bun.serve>;
  let url: string;
  beforeAll(() => {
    server = Bun.serve({
      port: 0,
      async fetch(req) {
        const delay = Number(new URL(req.url).searchParams.get("delay") ?? 0);
        await new Promise((r) => setTimeout(r, delay));
        return new Response("{}");
      },
    });
    url = `http://127.0.0.1:${server.port}/`;
  });
  afterAll(() => server.stop(true));

  const post = (delay: number, body: object, c = { textMs: 150, photoMs: 900 }) =>
    fetchWithBudget(`${url}?delay=${delay}`, { method: "POST", body: JSON.stringify(body) }, body, c);

  it("is cut off by the text timeout when sent as text", async () => {
    const started = Date.now();
    await expect(post(400, { text: "a receipt, described" })).rejects.toThrow();
    expect(Date.now() - started).toBeLessThan(380);
  });

  it("completes under the photo timeout when sent as a photo", async () => {
    const resp = await post(400, { is_image: true });
    expect(resp.ok).toBe(true);
  });

  it("hits the photo timeout, and not the text one, when it is slower than the photo budget", async () => {
    const started = Date.now();
    await expect(post(2_000, { is_image: true })).rejects.toMatchObject({ name: "TimeoutError" });
    const elapsed = Date.now() - started;
    expect(elapsed).toBeGreaterThanOrEqual(850);
    expect(elapsed).toBeLessThan(1_500);
  });
});

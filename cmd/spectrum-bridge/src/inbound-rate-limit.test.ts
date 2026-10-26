import { describe, expect, it } from "bun:test";
import { InboundRateLimiter, type RateLimitConfig } from "./inbound-rate-limit";

const cfg: RateLimitConfig = {
  senderBurst: 5,
  senderPerMinute: 6,
  bridgeBurst: 20,
  bridgePerMinute: 60,
};

function limiter(overrides: Partial<RateLimitConfig> = {}) {
  let t = 1_000_000;
  const l = new InboundRateLimiter({ ...cfg, ...overrides }, () => t);
  return { l, advance: (ms: number) => (t += ms) };
}

describe("InboundRateLimiter", () => {
  it("lets a sender burst up to its bucket and refuses the next message", () => {
    const { l } = limiter();
    for (let i = 0; i < 5; i++) expect(l.check("whatsapp:234800000001")).toBe("ok");
    expect(l.check("whatsapp:234800000001")).toBe("sender_limited");
  });

  it("one sender's burst does not touch a second sender", () => {
    const { l } = limiter();
    for (let i = 0; i < 50; i++) l.check("whatsapp:noisy");

    expect(l.check("whatsapp:noisy")).toBe("sender_limited");
    for (let i = 0; i < 5; i++) expect(l.check("whatsapp:quiet")).toBe("ok");
  });

  it("a refused sender does not spend the bridge's shared tokens", () => {
    const { l } = limiter({ bridgeBurst: 8 });
    for (let i = 0; i < 100; i++) l.check("whatsapp:noisy");

    // noisy spent 5 of the 8 bridge tokens and was refused on the rest without
    // spending any, so three other messages still fit.
    expect(l.check("whatsapp:a")).toBe("ok");
    expect(l.check("whatsapp:b")).toBe("ok");
    expect(l.check("whatsapp:c")).toBe("ok");
    expect(l.check("whatsapp:d")).toBe("bridge_limited");
  });

  it("many senders together cannot exceed the per-bridge cap", () => {
    const { l } = limiter({ bridgeBurst: 10, senderBurst: 5 });
    let accepted = 0;
    for (let s = 0; s < 20; s++) {
      for (let i = 0; i < 3; i++) if (l.check(`whatsapp:s${s}`) === "ok") accepted++;
    }
    expect(accepted).toBe(10);
  });

  it("refills over time at the configured rate", () => {
    const { l, advance } = limiter();
    for (let i = 0; i < 5; i++) l.check("whatsapp:x");
    expect(l.check("whatsapp:x")).toBe("sender_limited");

    advance(9_999); // 6/min is one token per 10s
    expect(l.check("whatsapp:x")).toBe("sender_limited");
    advance(1);
    expect(l.check("whatsapp:x")).toBe("ok");
    expect(l.check("whatsapp:x")).toBe("sender_limited");
  });

  it("never refills past the burst", () => {
    const { l, advance } = limiter();
    l.check("whatsapp:x");
    advance(60 * 60_000);
    for (let i = 0; i < 5; i++) expect(l.check("whatsapp:x")).toBe("ok");
    expect(l.check("whatsapp:x")).toBe("sender_limited");
  });

  it("keys on the platform as well as the id", () => {
    const { l } = limiter();
    for (let i = 0; i < 5; i++) l.check("whatsapp:+2348000");
    expect(l.check("whatsapp:+2348000")).toBe("sender_limited");
    expect(l.check("imessage:+2348000")).toBe("ok");
  });

  it("bounds the number of tracked senders", () => {
    const { l } = limiter({ bridgeBurst: 1_000_000, bridgePerMinute: 1_000_000 });
    for (let i = 0; i < 12_000; i++) l.check(`whatsapp:flood-${i}`);
    expect(l.trackedSenders).toBeLessThanOrEqual(10_000);
  });
});

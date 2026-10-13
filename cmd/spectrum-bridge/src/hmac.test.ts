import { describe, expect, it } from "bun:test";
import { createHmacAuth } from "./hmac";

const SECRET = "test-only-hmac-secret-0123456789abcdef";

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

import { describe, expect, it } from "bun:test";
import { isProviderDailyCap } from "./send-hold";

describe("isProviderDailyCap", () => {
  it("holds Photon's daily send limit", () => {
    const err = {
      name: "RateLimitError",
      message: "[upstream] Daily send limit exceeded",
      grpcCode: 8,
      retryable: false,
    };
    expect(isProviderDailyCap(err)).toBe(true);
  });

  it("does not hold an ordinary send failure", () => {
    expect(isProviderDailyCap(new Error("socket hang up"))).toBe(false);
    expect(isProviderDailyCap(undefined)).toBe(false);
  });
});

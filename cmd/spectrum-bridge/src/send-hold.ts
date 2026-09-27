/** How long to hold a reply Photon rejected for a daily send cap. */
export const PROVIDER_CAP_HOLD_MS = 15 * 60_000;

/**
 * Photon answered RESOURCE_EXHAUSTED / "Daily send limit exceeded".
 * That rejection does not clear in a few seconds. Retrying it burns the
 * outbound budget and then drops the reply. Hold it instead.
 */
export function isProviderDailyCap(err: unknown): boolean {
  if (typeof err === "string") {
    return /daily send limit exceeded|resource_exhausted/i.test(err);
  }
  if (!err || typeof err !== "object") return false;
  const e = err as { message?: unknown; grpcCode?: unknown; name?: unknown };
  if (e.grpcCode === 8 || e.name === "RateLimitError") {
    const message = typeof e.message === "string" ? e.message : "";
    if (!message) return e.name === "RateLimitError" || e.grpcCode === 8;
    return /daily send limit|resource_exhausted/i.test(message);
  }
  const message = typeof e.message === "string" ? e.message : "";
  return /daily send limit exceeded|resource_exhausted/i.test(message);
}

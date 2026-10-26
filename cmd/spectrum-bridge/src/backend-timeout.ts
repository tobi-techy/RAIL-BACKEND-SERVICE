// Per-request timeout and attempt budget for the bridge → Go inbound POST.
//
// Text keeps a short budget: the backend answers a typed message in a few
// seconds. A photo is read before the reply is sent (OCR, extraction, then
// Miriam's answer), so it gets its own, longer budget instead of inheriting the
// text one and being cut off mid-read and re-sent.

export interface TimeoutConfig {
  textMs: number;
  photoMs: number;
}

// A statement PDF: extraction plus one model pass plus Miriam's answer.
export const DOCUMENT_TIMEOUT_MS = 150_000;
export const DEFAULT_MAX_ATTEMPTS = 3;
// Document scans are not safe to retry blindly (each attempt mints a fresh
// ScanGuest + Offer nonce set), and a timeout on media means the backend is slow, not down; every retry re-runs
// the OCR it already paid for. Media gets one retry.
const MEDIA_MAX_ATTEMPTS = 2;

type Body = { is_document?: boolean; is_image?: boolean } | null | undefined;

function flags(body: unknown): Body {
  if (body && typeof body === "object" && !Array.isArray(body)) return body as Body;
  return undefined;
}

export function backendTimeoutMs(body: unknown, cfg: TimeoutConfig): number {
  const b = flags(body);
  if (b?.is_document) return DOCUMENT_TIMEOUT_MS;
  if (b?.is_image) return cfg.photoMs;
  // Must stay above the backend's whole-turn budget (which bounds the guest
  // onboarding turn), or we hang up on a turn it is about to answer.
  return cfg.textMs;
}

export function maxAttemptsFor(body: unknown): number {
  const b = flags(body);
  if (b?.is_document || b?.is_image) return MEDIA_MAX_ATTEMPTS;
  return DEFAULT_MAX_ATTEMPTS;
}

/** fetch() that gives up after the budget for this body, and no sooner. */
export function fetchWithBudget(
  url: string,
  init: RequestInit,
  body: unknown,
  cfg: TimeoutConfig,
): Promise<Response> {
  return fetch(url, { ...init, signal: AbortSignal.timeout(backendTimeoutMs(body, cfg)) });
}

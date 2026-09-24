/**
 * Reusable live iMessage confirmation card for Miriam-style money actions.
 *
 * Two-layer system:
 *  1. Photon / Spectrum card (this file + handler.ts): delivery + in-place
 *     mutation via `customizedMiniApp({ live: true })` (own extension) or the
 *     `app(url, { live: true })` stopgap (Spectrum-hosted).
 *  2. Our Messages extension UI (see FlipMessagesExtension/STUB): the actual
 *     Face ID button. Photon layout is Apple MSMessageTemplateLayout slots
 *     ONLY (caption, subcaption, trailing captions, JPEG image, summary).
 *     Never draw a Face ID button in the JPEG and pretend it is interactive.
 *
 * One ConfirmationCard for every action type. Action-specific renderers are
 * data (copy + amount fields from the backend payload), not new infrastructure.
 */
import { readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { app, edit, type ContentBuilder, type Message, type Space } from "spectrum-ts";
import { customizedMiniApp } from "spectrum-ts/providers/imessage";
import { childLogger } from "./logger";

const log = childLogger({ module: "confirmation-card" });

export type ConfirmationAction =
  | "invest.buy"
  | "invest.sell"
  | "invest.cancel"
  | "transfer.send"
  | "transfer.request"
  | "save.sweep"
  | "save.pause"
  | "limit.change"
  | "account.link"
  | "account.unlink"
  | "mandate.approve"
  | "mandate.revoke";

export type ConfirmationState =
  | "pending" // waiting for Face ID
  | "authenticating"
  | "approved" // biometrics passed, backend executing
  | "completed" // filled / sent / saved
  | "rejected" // user cancelled Face ID
  | "failed" // backend error
  | "expired"; // TTL hit, card dead

const TERMINAL_STATES: ReadonlySet<ConfirmationState> = new Set([
  "completed",
  "rejected",
  "failed",
  "expired",
]);

export function isTerminalConfirmationState(state: ConfirmationState): boolean {
  return TERMINAL_STATES.has(state);
}

const LEGAL_EDGES: Readonly<Record<ConfirmationState, ReadonlySet<ConfirmationState>>> = {
  pending: new Set(["authenticating", "rejected", "expired"]),
  authenticating: new Set(["approved", "rejected", "failed", "expired"]),
  approved: new Set(["completed", "failed", "expired"]),
  completed: new Set([]),
  rejected: new Set([]),
  failed: new Set([]),
  expired: new Set([]),
};

export function validConfirmationTransition(from: ConfirmationState, to: ConfirmationState): boolean {
  return LEGAL_EDGES[from]?.has(to) ?? false;
}

/** Wire payload from the Go backend (platform.ConfirmationCardPayload). */
export interface ConfirmationCardPayload {
  action_id: string;
  action: ConfirmationAction | string;
  state: ConfirmationState | string;
  confirm_url: string;
  title: string;
  subtitle?: string;
  amount?: string;
  asset?: string;
  destination?: string;
  fee?: string;
  risk_line?: string;
  expires_at?: string;
  caption?: string;
  subcaption?: string;
  trailing_caption?: string;
  trailing_subcaption?: string;
  image?: string;
  summary?: string;
}

export interface MiniAppConfig {
  /** Display name on the card (default "Miriam"). */
  appName: string;
  /** Our Messages extension bundle id. Empty = Spectrum app() stopgap. */
  extensionBundleId?: string;
  /** Apple Team ID. Required with extensionBundleId for customizedMiniApp. */
  teamId?: string;
}

/** True when we own a published extension and can render the native Face ID button. */
export function hasNativeExtension(cfg: MiniAppConfig): boolean {
  return Boolean(cfg.extensionBundleId && cfg.teamId);
}

/**
 * Branded JPEG background per state family. Designers drop files here:
 *   <assetsDir>/confirm-pending.jpg  — "Approve with Face ID"
 *   <assetsDir>/confirm-working.jpg  — approved/authenticating (executing)
 *   <assetsDir>/confirm-done.jpg     — completed
 *   <assetsDir>/confirm-muted.jpg    — rejected / expired
 *   <assetsDir>/confirm-error.jpg    — failed
 * Bubble chrome = JPEG + Apple text slots. Button, Face ID, motion = extension.
 */
export function previewImageName(state: string): string {
  switch (state) {
    case "completed":
      return "confirm-done.jpg";
    case "rejected":
    case "expired":
      return "confirm-muted.jpg";
    case "failed":
      return "confirm-error.jpg";
    case "authenticating":
    case "approved":
      return "confirm-working.jpg";
    case "pending":
    default:
      return "confirm-pending.jpg";
  }
}

/** Load the branded JPEG for a state. Undefined when the asset is missing — the card still renders from captions. */
export async function loadPreviewImage(
  assetsDir: string,
  state: string,
): Promise<Uint8Array<ArrayBuffer> | undefined> {
  if (!assetsDir) return undefined;
  const file = path.join(assetsDir, previewImageName(state));
  try {
    if (!existsSync(file)) {
      log.debug({ file }, "confirmation preview asset missing, captions-only card");
      return undefined;
    }
    const buf = await readFile(file);
    return new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength) as Uint8Array<ArrayBuffer>;
  } catch (err) {
    log.warn({ err, file }, "failed to read confirmation preview asset");
    return undefined;
  }
}

function layoutFor(card: ConfirmationCardPayload) {
  return {
    caption: card.caption || card.title,
    subcaption: card.subcaption || card.subtitle,
    trailingCaption: card.trailing_caption,
    trailingSubcaption: card.trailing_subcaption,
    summary: card.summary || `${card.title}${card.subtitle ? ` — ${card.subtitle}` : ""}`,
  };
}

/**
 * Build the card content. Preferred path (own extension): customizedMiniApp
 * so tapping opens OUR extension with the native Face ID button.
 * Stopgap: Spectrum-hosted app(url) — works this week, weaker native
 * control. Never fake Face ID in a webview. Liveness comes from in-place
 * edits (same card mutated on approve/reject/expire), not a flag — the
 * 8.2.1 SDK has no `live` option on either builder.
 */
export function buildConfirmationContent(
  card: ConfirmationCardPayload,
  cfg: MiniAppConfig,
  imageBytes?: Uint8Array<ArrayBuffer>,
): ContentBuilder {
  const base = layoutFor(card);
  const layout = imageBytes ? { ...base, image: imageBytes } : base;
  if (hasNativeExtension(cfg)) {
    return customizedMiniApp({
      appName: cfg.appName,
      extensionBundleId: cfg.extensionBundleId!,
      teamId: cfg.teamId!,
      url: card.confirm_url,
      layout,
    });
  }
  return app(card.confirm_url);
}

/**
 * Send a live mini-app card into the transcript. Returns the message record —
 * KEEP IT: every later state change edits this same card, never a new bubble.
 */
export async function sendConfirmationCard(
  space: Space,
  card: ConfirmationCardPayload,
  cfg: MiniAppConfig,
  imageBytes?: Uint8Array<ArrayBuffer>,
): Promise<Message> {
  const content = buildConfirmationContent(card, cfg, imageBytes);
  const sent = (await space.send(content)) as Message;
  log.info(
    { action_id: card.action_id, action: card.action, state: card.state },
    "sent live confirmation card",
  );
  return sent;
}

/**
 * Edit the SAME card after approve / reject / expire / fill. No second bubble.
 * Throws when the original handle is unresolvable — the caller (backend) must
 * persist state anyway and retry the edit, never send a duplicate bubble.
 */
export async function editConfirmationCard(
  space: Space,
  cardMessage: Message | undefined,
  card: ConfirmationCardPayload,
  cfg: MiniAppConfig,
  imageBytes?: Uint8Array<ArrayBuffer>,
): Promise<Message> {
  if (!cardMessage) {
    throw new Error(`no recorded card handle for action ${card.action_id} (refusing duplicate bubble)`);
  }
  const content = buildConfirmationContent(card, cfg, imageBytes);
  const updated = (await space.send(edit(content, cardMessage))) as Message;
  log.info(
    { action_id: card.action_id, action: card.action, state: card.state },
    "edited live confirmation card in place",
  );
  return updated;
}

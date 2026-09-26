import { z } from "zod";

const envSchema = z.object({
  SPECTRUM_PROJECT_ID: z.string().min(1),
  SPECTRUM_PROJECT_SECRET: z.string().min(1),

  RAIL_BACKEND_URL: z.string().url().default("http://localhost:8080"),
  RAIL_HMAC_SECRET: z.string().min(1),

  BRIDGE_PORT: z.coerce.number().default(3000),

  SPECTRUM_WEBHOOK_SECRET: z.string().optional(),
  SPECTRUM_WEBHOOK_PATH: z.string().default("/spectrum/webhook"),

  // Telegram (optional). When TELEGRAM_BOT_TOKEN is set the bridge starts a
  // Telegram provider alongside iMessage; otherwise it's iMessage-only.
  TELEGRAM_BOT_TOKEN: z.string().optional(),
  TELEGRAM_WEBHOOK_SECRET: z.string().optional(),

  // WhatsApp Business (optional). When WHATSAPP_ACCESS_TOKEN and
  // WHATSAPP_PHONE_NUMBER_ID are set the bridge starts a WhatsApp Business
  // provider alongside iMessage (and Telegram, if configured).
  WHATSAPP_ACCESS_TOKEN: z.string().optional(),
  WHATSAPP_PHONE_NUMBER_ID: z.string().optional(),
  WHATSAPP_APP_SECRET: z.string().optional(),

  NODE_ENV: z.enum(["development", "production"]).default("development"),
  LOG_LEVEL: z.enum(["trace", "debug", "info", "warn", "error", "fatal"]).default("info"),

  // Global outbound token bucket (see outbound-pacer.ts). Burst = sends that
  // go out immediately; refill = one more send slot per interval.
  PACER_BURST: z.coerce.number().int().min(1).default(10),
  PACER_REFILL_MS: z.coerce.number().int().min(50).default(1500),

  // Inbound transport. The SDK's webhook() and app.messages are independent
  // at-least-once paths — running both doubles delivery and dedup surface.
  // Prod uses webhooks (stateless, survives restarts); local dev may use the
  // streaming iterator. "both" is legacy and logs a warning.
  SPECTRUM_TRANSPORT_MODE: z.enum(["webhook", "stream", "both"]).default("webhook"),

  // Live confirmation cards (Face ID money actions). Bundle ID + team ID gate
  // the native Face ID button (customizedMiniApp + our Messages extension);
  // when unset the bridge ships the Spectrum app(url, { live: true }) stopgap.
  IMESSAGE_APP_NAME: z.string().default("Miriam"),
  IMESSAGE_EXTENSION_BUNDLE_ID: z.string().optional(),
  APPLE_TEAM_ID: z.string().optional(),
  CONFIRM_CARD_ASSETS_DIR: z.string().default("./assets/confirmation-cards"),

  // Outbound bubble cap: one backend turn renders at most this many message
  // bubbles (quota: every bubble counts toward the 5,000/day server cap).
  OUTBOUND_MAX_BUBBLES: z.coerce.number().int().min(1).max(10).default(3),

  // Transport-level deliverability caps (see deliverability.ts). The Go
  // ProactiveGuard owns message value/quiet-hours; these are the platform
  // hard caps that must never be crossed on the wire.
  DELIVERY_DAILY_CAP: z.coerce.number().int().min(1).default(5000),
  DELIVERY_NEW_CONVOS_PER_LINE: z.coerce.number().int().min(1).default(50),

  // Inbound turn supersession: drop a reply whose inbound turn a newer message
  // has already superseded (docs/miriam-inbound-supersession.md). Off by
  // default. Accepts "1"/"true"/"yes"/"on" (case-insensitive). Must be paired
  // with the backend's PLATFORM_TURN_SUPERSESSION — the bridge mints turn ids
  // and applies gate 2; the backend stamps replies and applies gate 1.
  MIRIAM_TURN_SUPERSESSION: z
    .string()
    .optional()
    .transform((v) => /^(1|true|yes|on)$/i.test(v ?? "")),
});

export type Env = z.infer<typeof envSchema>;

export function loadConfig(): Env {
  const result = envSchema.safeParse(process.env);
  if (!result.success) {
    console.error("Invalid configuration:", result.error.flatten());
    process.exit(1);
  }
  return result.data;
}

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

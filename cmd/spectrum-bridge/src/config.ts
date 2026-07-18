import { z } from "zod";

const envSchema = z.object({
  SPECTRUM_PROJECT_ID: z.string().min(1),
  SPECTRUM_PROJECT_SECRET: z.string().min(1),

  AMQP_URL: z.string().min(1),
  AMQP_EXCHANGE: z.string().default("miriam"),
  AMQP_INBOUND_QUEUE: z.string().default("miriam.inbound"),
  AMQP_INBOUND_ROUTING_KEY: z.string().default("message.inbound"),
  AMQP_OUTBOUND_QUEUE: z.string().default("miriam.outbound"),
  AMQP_OUTBOUND_ROUTING_KEY: z.string().default("message.outbound"),
  AMQP_ACTION_ROUTING_KEY: z.string().default("action.postback"),

  BRIDGE_PORT: z.coerce.number().default(3000),
  RAIL_BACKEND_URL: z.string().default("http://localhost:8080"),
  RAIL_HMAC_SECRET: z.string().min(1),

  SPECTRUM_WEBHOOK_SECRET: z.string().optional(),
  SPECTRUM_WEBHOOK_PATH: z.string().default("/spectrum/webhook"),

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

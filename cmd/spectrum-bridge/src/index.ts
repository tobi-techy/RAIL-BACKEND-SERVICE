import { Spectrum, type Space, type Message } from "spectrum-ts";
import { imessage } from "spectrum-ts/providers/imessage";
import express from "express";
import crypto from "node:crypto";
import { loadConfig } from "./config";
import { createRabbitMQ, InboundMessage, ActionEvent, RetryableOutboundError } from "./rabbitmq";
import { MessageHandler, OutboundMessage } from "./handler";
import { getLogger, childLogger } from "./logger";
import { SpaceStore } from "./space-store";

const config = loadConfig();
const log = getLogger();

const app = express();

app.use(
  express.json({
    verify: (req, _res, buf) => {
      (req as express.Request & { rawBody?: string }).rawBody = buf.toString("utf-8");
    },
  }),
);

const rabbit = createRabbitMQ(config.AMQP_URL, config.AMQP_EXCHANGE);
const handler = new MessageHandler();

// Persistent space store — survives bridge restarts so we know which threads exist.
const spaceStore = new SpaceStore();
await spaceStore.load();
spaceStore.startAutoSave();

// Registry of Space handles seen on inbound messages, so the outbound consumer
// can send to a conversation by id. Spectrum only surfaces Space objects through
// the inbound stream, so we can only send to spaces the user has messaged from —
// which covers every reply/confirmation path.
const spaces = new Map<string, Space>();

function verifyHMAC(payload: string, signature: string): boolean {
  try {
    const expected = crypto
      .createHmac("sha256", config.RAIL_HMAC_SECRET)
      .update(payload)
      .digest("hex");
    return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature));
  } catch {
    return false;
  }
}

async function sendToSpace(msg: OutboundMessage): Promise<void> {
  const space = spaces.get(msg.thread_id);
  if (!space) {
    // Track that we had a miss for observability, then surface as retryable so
    // the message is requeued — when the user messages again, the space will be
    // rediscovered and the message will be delivered.
    const known = spaceStore.has(msg.thread_id);
    log.warn(
      { thread_id: msg.thread_id, known_space: known },
      `no active space for thread_id="${msg.thread_id}" — will requeue`,
    );
    throw new RetryableOutboundError(`no active space for thread_id="${msg.thread_id}"`);
  }
  await handler.handleOutbound(space, msg);
}

// HTTP fallback (primary path is the RabbitMQ outbound queue). Signature required.
app.post("/send", (req, res) => {
  const raw =
    (req as express.Request & { rawBody?: string }).rawBody ?? JSON.stringify(req.body);
  const sig = req.headers["x-hmac-sha256"] as string | undefined;
  if (!sig || !verifyHMAC(raw, sig)) {
    log.warn({ ip: req.ip }, "invalid HMAC signature on /send");
    res.status(401).json({ error: "invalid or missing HMAC signature" });
    return;
  }
  sendToSpace(req.body as OutboundMessage).catch((err) => {
    log.error({ err }, "HTTP /send failed");
  });
  res.json({ status: "queued" });
});

app.get(["/", "/health"], (_req, res) => {
  res.json({
    status: "ok",
    spaces: spaces.size,
    known_threads: spaceStore.count(),
    rabbitmq_connected: rabbit.isConnected(),
    uptime_sec: Math.floor(process.uptime()),
  });
});

// Outbound consumer — primary path.
rabbit.consumeOutbound(async (raw: string) => {
  let msg: OutboundMessage;
  try {
    msg = JSON.parse(raw) as OutboundMessage;
  } catch {
    log.error({ raw: raw.slice(0, 200) }, "invalid outbound message JSON — dead-lettering");
    // Throw to trigger DLQ in the consumer
    throw new Error("invalid outbound message JSON");
  }
  await sendToSpace(msg);
}, config.AMQP_OUTBOUND_QUEUE);

async function handleInbound(space: Space, message: Message): Promise<void> {
  const threadID = space.id;
  spaces.set(threadID, space);
  spaceStore.register(threadID, space.id);
  handler.registerInboundMessage(message);

  const senderId = message.sender?.id;
  if (!senderId) return;

  const content = message.content;
  const log = childLogger({ sender: senderId, thread: threadID, msg_type: content.type });

  if ((message as any).isFromMe) {
    log.debug("skipping self-send echo");
    return;
  }

  // A poll vote is how users confirm/cancel actions (iMessage has no buttons).
  // poll_option also fires on de-select — only act when an option is selected.
  if (content.type === "poll_option") {
    if (!content.selected) return;
    const choice = content.option.title.trim().toLowerCase();
    const event: ActionEvent = {
      action: choice === "confirm" ? "confirm" : "cancel",
      poll_title: content.poll.title,
      user_id: senderId,
      space_id: space.id,
      platform: message.platform,
    };
    await rabbit.publishAction(event);
    log.info({ action: event.action }, "published poll vote");
    return;
  }

  // Voice note — ship the audio to the backend for transcription.
  if (content.type === "voice") {
    let audioB64: string;
    try {
      const buf = await content.read();
      audioB64 = Buffer.from(buf).toString("base64");
    } catch (err) {
      log.error({ err }, "failed to read voice note");
      return;
    }
    const inbound: InboundMessage = {
      platform: message.platform,
      user_id: senderId,
      thread_id: space.id,
      text: "",
      space_id: space.id,
      msg_id: message.id,
      is_voice: true,
      audio_b64: audioB64,
      audio_mime: content.mimeType,
    };
    await rabbit.publishInbound(inbound);
    log.info({ audio_len: audioB64.length }, "published voice note");
    return;
  }

  if (content.type === "text") {
    const text = content.text?.trim();
    if (!text) return;
    const inbound: InboundMessage = {
      platform: message.platform,
      user_id: senderId,
      thread_id: space.id,
      text,
      space_id: space.id,
      msg_id: message.id,
    };
    await rabbit.publishInbound(inbound);
    log.info({ text: text.slice(0, 60) }, "published inbound message");
  }
}

async function start() {
  log.info(
    {
      exchange: config.AMQP_EXCHANGE,
      inboundQueue: config.AMQP_INBOUND_QUEUE,
      outboundQueue: config.AMQP_OUTBOUND_QUEUE,
    },
    "starting spectrum bridge",
  );

  const agent = await Spectrum({
    projectId: config.SPECTRUM_PROJECT_ID,
    projectSecret: config.SPECTRUM_PROJECT_SECRET,
    providers: [imessage.config()],
    ...(config.SPECTRUM_WEBHOOK_SECRET ? { webhookSecret: config.SPECTRUM_WEBHOOK_SECRET } : {}),
  });

  // Webhook route — register after agent is initialized so the closure works.
  // Must use raw body so the SDK can verify HMAC/protobuf decode.
  app.post(
    config.SPECTRUM_WEBHOOK_PATH,
    express.raw({ type: "*/*" }),
    async (req, res) => {
      try {
        const result = await agent.webhook(
          {
            body: req.body as Uint8Array,
            headers: req.headers as Record<string, string>,
          },
          async (space: Space, message: Message) => {
            await handleInbound(space, message);
          },
        );
        res.status(result.status).set(result.headers).send(Buffer.from(result.body));
      } catch (err) {
        log.error({ err }, "webhook handler failed");
        res.status(500).json({ error: "internal error" });
      }
    },
  );

  app.listen(config.BRIDGE_PORT, () => {
    log.info({ port: config.BRIDGE_PORT }, "bridge HTTP server listening");
  });

  log.info("waiting for iMessage...");

  for await (const [space, message] of agent.messages) {
    handleInbound(space, message).catch((err) => log.error({ err }, "inbound handling error"));
  }
}

start().catch((err) => {
  log.fatal({ err }, "bridge failed to start");
  process.exit(1);
});

process.on("SIGTERM", async () => {
  log.info("shutting down...");
  await spaceStore.flush();
  spaceStore.stopAutoSave();
  await rabbit.close();
  process.exit(0);
});

process.on("SIGINT", async () => {
  log.info("shutting down (SIGINT)...");
  await spaceStore.flush();
  spaceStore.stopAutoSave();
  await rabbit.close();
  process.exit(0);
});

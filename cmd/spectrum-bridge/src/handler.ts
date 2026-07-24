import { type Space, type Message, markdown, reply, typing, richlink, app, poll, voice } from "spectrum-ts";
import { effect, imessage, type IMessageMessageEffect } from "spectrum-ts/providers/imessage";
import { childLogger } from "./logger";
import { RetryableOutboundError } from "./rabbitmq";

const log = childLogger({ module: "handler" });

export interface OutboundMessage {
  platform: string;
  user_id: string;
  thread_id: string;
  text: string;

  content_type?:
    | "typing"
    | "markdown"
    | "reply"
    | "effect"
    | "appcard"
    | "richlink"
    | "poll"
    | "voice"
    | "text";

  // reply
  reply_to?: string;

  // effect (iMessage only)
  effect?: string;

  // app card / rich link
  card_url?: string;

  // poll (used for Confirm / Cancel action prompts)
  poll_title?: string;
  poll_options?: string[];

  // voice note (base64 TTS audio)
  audio_b64?: string;
  audio_mime?: string;
  duration_sec?: number;
}

const EFFECTS: Record<string, IMessageMessageEffect> = {
  celebration: imessage.effect.message.celebration,
  confetti: imessage.effect.message.confetti,
  fireworks: imessage.effect.message.fireworks,
  balloons: imessage.effect.message.balloons,
  heart: imessage.effect.message.heart,
  lasers: imessage.effect.message.lasers,
  sparkles: imessage.effect.message.sparkles,
  spotlight: imessage.effect.message.spotlight,
  echo: imessage.effect.message.echo,
};

export class MessageHandler {
  private seen = new Map<string, number>();
  private readonly dedupWindowMs = 2000;
  private messageStore = new Map<string, Message>();
  private pendingReplyLookups = new Map<string, Promise<Message | undefined>>();

  constructor() {
    setInterval(() => this.evictStaleMessages(), 60_000);
  }

  registerInboundMessage(msg: Message): void {
    this.messageStore.set(msg.id, msg);
  }

  private evictStaleMessages(): void {
    if (this.messageStore.size > 500) {
      const entries = [...this.messageStore.entries()];
      for (const [id] of entries.slice(0, entries.length - 250)) {
        this.messageStore.delete(id);
      }
      log.debug({ remaining: this.messageStore.size }, "evicted stale messages");
    }
  }

  private async resolveParentMessage(space: Space, replyTo: string): Promise<Message | undefined> {
    const stored = this.messageStore.get(replyTo);
    if (stored) return stored;

    const existing = this.pendingReplyLookups.get(replyTo);
    if (existing) return existing;

    const promise = (async () => {
      try {
        const msg = await space.getMessage(replyTo);
        if (msg) this.messageStore.set(replyTo, msg);
        return msg;
      } catch {
        return undefined;
      }
    })();

    this.pendingReplyLookups.set(replyTo, promise);
    const result = await promise;
    this.pendingReplyLookups.delete(replyTo);
    return result;
  }

  async handleOutbound(space: Space, msg: OutboundMessage): Promise<void> {
    const contentType = msg.content_type || "text";

    if (contentType === "typing") {
      await space.send(typing());
      return;
    }

    // Voice notes are always distinct audio — send before the text dedup guard.
    if (contentType === "voice") {
      if (!msg.audio_b64) {
        log.warn({ thread_id: msg.thread_id }, "voice message with no audio data");
        return;
      }
      const buf = Buffer.from(msg.audio_b64, "base64");
      await space.send(
        voice(buf, {
          name: "miriam.mp3",
          mimeType: msg.audio_mime || "audio/mpeg",
          duration: msg.duration_sec,
        }),
      );
      return;
    }

    const dedupKey = `${msg.user_id}:${contentType}:${msg.text}:${msg.reply_to || ""}`;
    const now = Date.now();
    const last = this.seen.get(dedupKey);
    if (last && now - last < this.dedupWindowMs) {
      log.debug({ dedupKey }, "deduplicated outbound message");
      return;
    }
    this.seen.set(dedupKey, now);

    // Polls (Confirm/Cancel) are iMessage-only. On platforms without poll
    // support (Telegram, WhatsApp), fall back to a YES/NO text prompt that the
    // backend matches against the same pending action.
    const supportsPoll = msg.platform === "imessage";

    switch (contentType) {
      case "poll": {
        if (!supportsPoll) {
          const prompt = `${msg.poll_title || msg.text}\n\nReply YES to confirm or NO to cancel.`;
          await this.sendWithPacing(space, prompt, "text");
          return;
        }
        const options = msg.poll_options?.length ? msg.poll_options : ["Confirm", "Cancel"];
        await space.send(poll(msg.poll_title || msg.text, options));
        return;
      }

      case "reply": {
        if (msg.reply_to) {
          const parent = await this.resolveParentMessage(space, msg.reply_to);
          if (parent) {
            await space.send(typing());
            await this.delay(this.typingDurationMs(msg.text));
            await space.send(reply(markdown(msg.text), parent));
          } else {
            log.warn({ reply_to: msg.reply_to }, "parent message not found, sending as markdown");
            await this.sendWithPacing(space, msg.text, "markdown");
          }
          return;
        }
        await this.sendWithPacing(space, msg.text, "markdown");
        return;
      }

      case "effect": {
        // Effects are iMessage-only; degrade to a plain message elsewhere.
        const id = msg.platform === "imessage" && msg.effect ? EFFECTS[msg.effect] : undefined;
        await space.send(typing());
        await this.delay(this.typingDurationMs(msg.text));
        await space.send(id ? effect(markdown(msg.text), id) : markdown(msg.text));
        return;
      }

      case "appcard": {
        if (msg.text) await this.sendWithPacing(space, msg.text, "markdown");
        if (msg.card_url) await space.send(app(msg.card_url));
        return;
      }

      case "richlink": {
        if (msg.text) await this.sendWithPacing(space, msg.text, "markdown");
        if (msg.card_url) await space.send(richlink(msg.card_url));
        return;
      }

      case "markdown":
        await this.sendWithPacing(space, msg.text, "markdown");
        return;

      default:
        await this.sendWithPacing(space, msg.text, "text");
    }
  }

  private async sendWithPacing(space: Space, text: string, format: "markdown" | "text"): Promise<void> {
    const bubbles = text.split(/\n\s*\n/).map((s) => s.trim()).filter((s) => s.length > 0);

    if (bubbles.length === 0) return;

    for (let i = 0; i < bubbles.length; i++) {
      await this.typeThenSend(space, bubbles[i], format);

      if (i < bubbles.length - 1) {
        await this.delay(this.interBubbleDelayMs(bubbles[i + 1]));
      }
    }
  }

  private async typeThenSend(space: Space, bubble: string, format: "markdown" | "text"): Promise<void> {
    await space.send(typing());
    await this.delay(this.typingDurationMs(bubble));

    if (format === "markdown") {
      await space.send(markdown(bubble));
    } else {
      await space.send(bubble);
    }
  }

  // Simulate a human typing speed: ~30ms/char, floored so even one-word
  // replies get a beat of "typing", capped so long bubbles don't stall.
  private typingDurationMs(text: string): number {
    return Math.min(Math.max(text.length * 30, 700), 2500);
  }

  // Short pause between bubbles so the next "typing" bubble feels like a
  // fresh thought rather than a burst.
  private interBubbleDelayMs(nextBubble: string): number {
    return Math.min(Math.max(nextBubble.length * 8, 300), 700);
  }

  private delay(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}

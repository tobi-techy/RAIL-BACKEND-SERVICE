import { type Space, type Message, markdown, typing, richlink, app, poll, voice } from "spectrum-ts";
import { effect, imessage, type IMessageMessageEffect } from "spectrum-ts/providers/imessage";
import { childLogger } from "./logger";
import {
  editConfirmationCard,
  loadPreviewImage,
  sendConfirmationCard,
  type ConfirmationCardPayload,
  type MiniAppConfig,
} from "./confirmation-card";
import type { ConfirmationCardStore } from "./confirmation-store";

const log = childLogger({ module: "handler" });

export interface InsightCard {
  type: string;
  title: string;
  subtitle?: string;
  sentiment?: string;
  data?: unknown;
}

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
    | "cards"
    | "reaction"
    | "confirmationcard"
    | "confirmationcard_edit"
    | "text";

  // reply
  reply_to?: string;

  // reaction (tapback on the user's message — iMessage renders universal emoji
  // as native tapbacks; other platforms no-op silently)
  reaction_emoji?: string;

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

  // Live confirmation card (Face ID money actions — one primitive, every action).
  // The initial send records the card handle by action_id; edits mutate it.
  confirmation_card?: ConfirmationCardPayload;

  // structured insight cards (rendered as per-platform card text)
  cards?: InsightCard[];

  // delivery category: critical messages survive longer in the bridge's
  // persistent outbound queue when the Space handle is cold.
  category?: "critical" | "normal";

  // Stable idempotency key across retries (best-practices/recovery-and-state).
  // The queue mints one when absent; retries of the same logical send reuse it
  // so a crash between provider-ack and bookkeeping still dedups.
  client_guid?: string;

  // True when this is the first outbound ever to this thread. Links/media are
  // suppressed on first contact (deliverability: Apple suppresses link taps
  // until a reply lands) — the text goes out, the link is dropped with a warn.
  is_first?: boolean;

  // Epoch ms: hold the message in the bridge's persistent queue until this
  // time. The Go backend's ProactiveGuard already enforces per-user quiet
  // hours; this is the escape hatch for any send the backend wants the
  // bridge to time.
  send_after?: number;
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
  slam: imessage.effect.message.slam,
  loud: imessage.effect.message.loud,
  gentle: imessage.effect.message.gentle,
  invisible: imessage.effect.message.invisible,
};

export class MessageHandler {
  private seen = new Map<string, number>();
  private readonly dedupWindowMs = 2000;
  /** Stable-GUID dedup for idempotent retries: survives the 2s text window. */
  private seenGuids = new Map<string, number>();
  private readonly guidDedupWindowMs = 10 * 60 * 1000;
  private readonly maxBubbles: number;
  private readonly miniApp: MiniAppConfig;
  private readonly cardStore?: ConfirmationCardStore;
  private readonly cardAssetsDir: string;
  private messageStore = new Map<string, Message>();
  private lastInboundByThread = new Map<string, Message>();
  private pendingReplyLookups = new Map<string, Promise<Message | undefined>>();

  constructor(
    opts: {
      maxBubbles?: number;
      miniApp?: MiniAppConfig;
      cardStore?: ConfirmationCardStore;
      cardAssetsDir?: string;
    } = {},
  ) {
    this.maxBubbles = Math.min(Math.max(opts.maxBubbles ?? 3, 1), 10);
    this.miniApp = opts.miniApp ?? { appName: "Miriam" };
    this.cardStore = opts.cardStore;
    this.cardAssetsDir = opts.cardAssetsDir ?? "";
    setInterval(() => this.evictStaleMessages(), 60_000);
  }

  registerInboundMessage(msg: Message): void {
    this.messageStore.set(msg.id, msg);

    // Track the most recent inbound per thread for read receipts.
    // Skip messages without a space id to avoid collisions on the empty key,
    // and delete-then-set so Map insertion order reflects most-recent activity.
    const threadId = msg.space?.id;
    if (!threadId) return;
    this.lastInboundByThread.delete(threadId);
    this.lastInboundByThread.set(threadId, msg);
  }

  getLastInboundMessage(threadId: string): Message | undefined {
    return this.lastInboundByThread.get(threadId);
  }

  private evictStaleMessages(): void {
    // The outbound dedup window is tiny (2s), so anything older is dead weight:
    // without this sweep `seen` grew unbounded for the life of the process.
    const now = Date.now();
    for (const [key, at] of this.seen) {
      if (now - at >= this.dedupWindowMs) this.seen.delete(key);
    }
    for (const [guid, at] of this.seenGuids) {
      if (now - at >= this.guidDedupWindowMs) this.seenGuids.delete(guid);
    }
    if (this.messageStore.size > 500) {
      const entries = [...this.messageStore.entries()];
      for (const [id] of entries.slice(0, entries.length - 250)) {
        this.messageStore.delete(id);
      }
      log.debug({ remaining: this.messageStore.size }, "evicted stale messages");
    }
    // Keep lastInboundByThread bounded — only retain the most recent 100 threads
    if (this.lastInboundByThread.size > 100) {
      const entries = [...this.lastInboundByThread.entries()];
      for (const [id] of entries.slice(0, entries.length - 100)) {
        this.lastInboundByThread.delete(id);
      }
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

    // Reactions are native tapbacks on a specific inbound message. We resolve the
    // target from the stored inbound message (or a provider fetch) and call
    // react() on that handle — on platforms without reaction support this is a
    // silent no-op, so it never blocks the rest of the turn.
    if (contentType === "reaction") {
      if (!msg.reaction_emoji) {
        log.warn({ thread_id: msg.thread_id }, "reaction message with no emoji");
        return;
      }
      let target: Message | undefined;
      if (msg.reply_to) {
        target = await this.resolveParentMessage(space, msg.reply_to);
      } else {
        target = this.getLastInboundMessage(msg.thread_id);
      }
      if (!target) {
        log.warn(
          { thread_id: msg.thread_id, reply_to: msg.reply_to || undefined },
          "reaction target message not found",
        );
        return;
      }
      try {
        await target.react(msg.reaction_emoji);
      } catch (err) {
        log.warn({ err, emoji: msg.reaction_emoji }, "reaction send failed");
      }
      return;
    }

    const dedupKey = `${msg.user_id}:${contentType}:${msg.text}:${msg.reply_to || ""}:${msg.client_guid || ""}`;
    const now = Date.now();
    const last = this.seen.get(dedupKey);
    if (last && now - last < this.dedupWindowMs) {
      log.debug({ dedupKey }, "deduplicated outbound message");
      return;
    }
    this.seen.set(dedupKey, now);
    // Stable-GUID idempotency: a retry of the same logical send (queue retry,
    // crash between ack and bookkeeping) reuses client_guid and is dropped
    // even past the 2s text window.
    if (msg.client_guid) {
      const guidLast = this.seenGuids.get(msg.client_guid);
      if (guidLast && now - guidLast < this.guidDedupWindowMs) {
        log.debug({ client_guid: msg.client_guid }, "deduplicated outbound retry by client_guid");
        return;
      }
      this.seenGuids.set(msg.client_guid, now);
    }

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
        const rawTitle = (msg.poll_title || "").trim();
        const rawText = (msg.text || "").trim();
        // Guard against empty poll titles (Zod requires title >=1). If both
        // are empty, fall back to a safe question instead of caching a broken
        // poll that can never be resolved (the bug that caused Zod title
        // too_small and failed-to-resolve errors).
        let title = rawTitle || rawText || "Your call";
        // Extra guard: single ellipsis or whitespace-only still fails Zod
        if (!title || title === "…" || title.replace(/[…\s]/g, "").length === 0) {
          title = "Your call";
          log.warn({ rawTitle, rawText, thread_id: msg.thread_id }, "poll title was empty/ellipsis, fell back to Your call");
        }
        log.info({ title, options, thread_id: msg.thread_id }, "sending poll");
        // Always send the question as a message bubble BEFORE the poll so the
        // user sees words even when the poll title duplicates the text. The
        // backend's pollWords now always sets leadIn when text present, but
        // this is the last defense for any legacy payload that still bundles
        // text onto the poll.
        if (rawText) {
          await this.sendWithPacing(space, rawText, "text");
        } else if (rawTitle && rawTitle !== title) {
          await this.sendWithPacing(space, rawTitle, "text");
        }
        // The guard above keeps the title valid; a build failure here surfaces
        // through sendToSpace's catch like any other send error. (The old
        // probe-build-for-logging built every poll twice.)
        await space.send(poll(title, options));
        return;
      }

      case "reply": {
        if (msg.reply_to) {
          const parent = await this.resolveParentMessage(space, msg.reply_to);
          if (parent) {
            // Prefer the message-level sugar; on platforms without thread
            // support reply() resolves as a no-op or throws UnsupportedError —
            // fall back to a guaranteed plain send so the words still land.
            try {
              await parent.reply(markdown(msg.text));
            } catch {
              log.warn({ reply_to: msg.reply_to }, "threaded reply unsupported, sending as markdown");
              await this.sendWithPacing(space, msg.text, "markdown");
            }
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
        // Deliverability: no links/media in the first message — Apple
        // suppresses link taps until a reply lands. Text goes out, link drops.
        if (msg.is_first && msg.card_url) {
          log.warn({ thread_id: msg.thread_id }, "suppressed app link on first-contact message");
          return;
        }
        if (msg.card_url) await space.send(app(msg.card_url));
        return;
      }

      case "richlink": {
        if (msg.text) await this.sendWithPacing(space, msg.text, "markdown");
        if (msg.is_first && msg.card_url) {
          log.warn({ thread_id: msg.thread_id }, "suppressed rich link on first-contact message");
          return;
        }
        if (msg.card_url) await space.send(richlink(msg.card_url));
        return;
      }

      case "cards": {
        // Narrative text first (paced), then each insight card as its own bubble.
        if (msg.text) await this.sendWithPacing(space, msg.text, "markdown");
        for (const card of msg.cards ?? []) {
          const bubble = renderInsightCard(card);
          if (bubble) await this.sendWithPacing(space, bubble, "markdown");
        }
        return;
      }

      case "confirmationcard":
      case "confirmationcard_edit": {
        await this.handleConfirmationCard(space, msg, contentType === "confirmationcard_edit");
        return;
      }

      case "markdown":
        await this.sendWithPacing(space, msg.text, "markdown");
        return;

      default:
        await this.sendWithPacing(space, msg.text, "text");
    }
  }

  /**
   * Live confirmation cards (Face ID money actions). Initial sends render ONE
   * live mini-app card and record its handle by action_id; edits mutate that
   * same card in place — never a second bubble. Non-iMessage platforms get a
   * text fallback with the confirm URL (the Face ID extension is iMessage-only).
   */
  private async handleConfirmationCard(space: Space, msg: OutboundMessage, isEdit: boolean): Promise<void> {
    const card = msg.confirmation_card;
    if (!card?.action_id || !card?.confirm_url) {
      log.warn({ thread_id: msg.thread_id }, "confirmation card with no action_id/confirm_url, dropping (no duplicate bubble)");
      return;
    }
    if (msg.platform !== "imessage") {
      const fallback = `${card.title}${card.subtitle ? `\n${card.subtitle}` : ""}\nApprove: ${card.confirm_url}`;
      await this.sendWithPacing(space, fallback, "text");
      return;
    }
    const image = await loadPreviewImage(this.cardAssetsDir, card.state);
    if (!isEdit) {
      // One live card. No text recap alongside it — the card IS the message.
      const sent = await sendConfirmationCard(space, card, this.miniApp, image);
      if (sent?.id) this.messageStore.set(sent.id, sent);
      this.cardStore?.record(card.action_id, msg.thread_id, sent?.id ?? "", card.state);
      return;
    }
    const record = this.cardStore?.get(card.action_id);
    let original: Message | undefined;
    if (record?.message_id) {
      original = this.messageStore.get(record.message_id);
      if (!original) {
        try {
          original = await space.getMessage(record.message_id);
          if (original) this.messageStore.set(record.message_id, original);
        } catch (err) {
          log.warn({ err, action_id: card.action_id }, "confirmation edit target unresolvable");
        }
      }
    }
    // Throws when unresolvable: the backend persists state anyway and retries
    // the edit. Never fall back to a fresh send — that would duplicate the card.
    const updated = await editConfirmationCard(space, original, card, this.miniApp, image);
    if (updated?.id && record) {
      this.messageStore.set(updated.id, updated);
      this.cardStore?.record(card.action_id, msg.thread_id, record.message_id, card.state);
    }
  }

  private async sendWithPacing(space: Space, text: string, format: "markdown" | "text"): Promise<void> {    const all = text.split(/\n\s*\n/).map((s) => s.trim()).filter((s) => s.length > 0);

    if (all.length === 0) return;

    // Bubble cap: every bubble counts toward the 5,000/day server cap and adds
    // seconds of typing delay. Merge the overflow into the last bubble so one
    // backend turn never fans out into N sends.
    let bubbles = all;
    if (all.length > this.maxBubbles) {
      bubbles = [...all.slice(0, this.maxBubbles - 1), all.slice(this.maxBubbles - 1).join("\n\n")];
      log.warn({ bubbles_before: all.length, bubbles_after: bubbles.length }, "capped outbound bubbles");
    }

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

interface CardRow {
  label: string;
  value: string;
}

// renderInsightCard turns an engine InsightCard into a compact, portable
// markdown bubble that reads well on iMessage, WhatsApp, and Telegram. It is
// deliberately tolerant of the card shapes the tool pipeline emits: arrays of
// stat/breakdown items, structured data objects, chart points, and tips.
export function renderInsightCard(card: InsightCard): string {
  const lines: string[] = [];
  if (card.title) lines.push(`**${card.title}**`);
  if (card.subtitle) lines.push(card.subtitle);

  const rows = extractCardRows(card.data);
  for (const row of rows) {
    if (row.label) {
      lines.push(`• ${row.label}: **${row.value}**`);
    } else if (row.value) {
      lines.push(row.value);
    }
  }

  return lines.join("\n").trim();
}

export function extractCardRows(data: unknown): CardRow[] {
  if (Array.isArray(data)) {
    return data.flatMap(extractItemRows);
  }
  if (!data || typeof data !== "object") return [];

  const obj = data as Record<string, unknown>;

  if (Array.isArray(obj.items)) {
    return (obj.items as unknown[]).flatMap(extractItemRows);
  }

  // tip / empty_state: free-form message
  if (typeof obj.message === "string" && obj.message.length > 0) {
    return [{ label: "", value: obj.message }];
  }

  // chart: y_label + the most recent points
  if (Array.isArray(obj.points)) {
    const rows = (obj.points as Array<Record<string, unknown>>)
      .slice(-5)
      .map((p) => ({ label: str(p?.label), value: fmtValue(p?.value) }))
      .filter((r) => r.label || r.value);
    if (rows.length === 0) return [];
    if (typeof obj.y_label === "string" && obj.y_label) {
      return [{ label: obj.y_label, value: rows[rows.length - 1].value }, ...rows];
    }
    return rows;
  }

  // structured data (runway, yield_summary, comparison, subscription_audit, …):
  // scalar string/number fields render as rows; anything nested is skipped.
  return Object.entries(obj)
    .filter(([, v]) => typeof v === "string" || typeof v === "number")
    .map(([k, v]) => ({ label: prettyKey(k), value: fmtValue(v) }));
}

function extractItemRows(item: unknown): CardRow[] {
  if (!item || typeof item !== "object") return [];
  const o = item as Record<string, unknown>;

  // StatItem: { label, value }  |  BreakdownItem: { label, amount }
  const label = str(o.label);
  const value = o.value !== undefined ? fmtValue(o.value) : o.amount !== undefined ? fmtValue(o.amount) : "";
  if (!label && !value) return [];

  const rows: CardRow[] = [{ label, value }];
  if (typeof o.change === "string" && o.change) {
    rows.push({ label: label ? `${label} (change)` : "change", value: o.change });
  }
  return rows;
}

function prettyKey(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

function fmtValue(v: unknown): string {
  if (typeof v === "number") {
    return Number.isFinite(v) ? String(v) : "";
  }
  if (typeof v === "string") return v;
  return "";
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

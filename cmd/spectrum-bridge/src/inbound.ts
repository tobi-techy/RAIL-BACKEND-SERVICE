import type { Content, Message } from "spectrum-ts";
import type { Logger } from "pino";
import {
  contactFromSpectrum,
  isVCardMime,
  type SharedContact,
} from "./contact";

/**
 * Inbound pipeline: content router + per-space burst debouncer.
 *
 * handleInbound (index.ts) owns dedup, space registration, and the echo
 * guard; everything content-shaped lives here so the routing rules are
 * testable in isolation with fake post/debounce timings.
 */

/** Payload posted to the Go backend at /api/v1/platform/inbound. */
export interface InboundPayload {
  platform: string;
  user_id: string;
  thread_id: string;
  text: string;
  space_id: string;
  msg_id?: string;

  is_voice?: boolean;
  audio_b64?: string;
  audio_mime?: string;

  is_image?: boolean;
  image_b64?: string;
  image_mime?: string;

  is_document?: boolean;
  document_b64?: string;
  document_mime?: string;
  document_name?: string;

  is_contact?: boolean;
  vcard_text?: string;
  contact?: SharedContact;

  is_poll_vote?: boolean;

  is_reaction?: boolean;
  reaction_emoji?: string;

  reply_to?: string;
  reply_to_text?: string;
  edit_of?: string;
}

/** Extra fields threaded through reply/edit/effect unwrapping. */
export interface InboundExtras {
  reply_to?: string;
  reply_to_text?: string;
  edit_of?: string;
}

const REPLY_QUOTE_MAX_CHARS = 200;
const MAX_STATEMENT_BYTES = 4 * 1024 * 1024;

/**
 * Spectrum's Message carries `direction: "inbound" | "outbound"`. Poll votes,
 * tapbacks and our own sends can echo back through the inbound stream; only
 * true inbound messages may reach the backend. (The old guard checked a
 * nonexistent `isFromMe` field and let echoes through.)
 */
export function isOutboundEcho(message: Message): boolean {
  return message.direction === "outbound";
}

// ---------------------------------------------------------------------------
// Burst debouncer
// ---------------------------------------------------------------------------

export interface DebouncerOptions {
  /** Called once per flush with the merged payload. */
  post: (key: string, payload: InboundPayload) => Promise<void> | void;
  /** Quiet-window length; each new text resets it. */
  debounceMs: number;
  /** Hard cap on buffer age, measured from the first buffered message. */
  maxWaitMs: number;
  /** Hard cap on buffered message count. */
  maxBuffer: number;
  /** Called when the first message enters an empty buffer (typing keeper). */
  onBufferStart?: (key: string) => void;
  onError?: (key: string, err: unknown) => void;
}

interface BufferState {
  entries: InboundPayload[];
  firstAt: number;
  timer: ReturnType<typeof setTimeout> | null;
}

export class InboundDebouncer {
  private buffers = new Map<string, BufferState>();

  constructor(private readonly opts: DebouncerOptions) {}

  /** Buffer a text-ish payload; schedules or triggers a flush. */
  add(key: string, payload: InboundPayload): void {
    let buf = this.buffers.get(key);
    if (!buf) {
      buf = { entries: [], firstAt: Date.now(), timer: null };
      this.buffers.set(key, buf);
      this.opts.onBufferStart?.(key);
    }
    buf.entries.push(payload);

    if (buf.timer) {
      clearTimeout(buf.timer);
      buf.timer = null;
    }

    const elapsed = Date.now() - buf.firstAt;
    if (
      buf.entries.length >= this.opts.maxBuffer ||
      elapsed >= this.opts.maxWaitMs
    ) {
      // Hard caps hit — flush now, synchronously (async post is fire-and-forget
      // here; callers that need ordering use `await flush(key)`).
      void this.flush(key);
      return;
    }

    // Fire at the sooner of the quiet window or the hard max-wait deadline.
    // Clamp to >= 0 so elapsed slightly exceeding maxWaitMs doesn't fire
    // the timeout immediately with a negative delay.
    const wait = Math.max(
      0,
      Math.min(this.opts.debounceMs, this.opts.maxWaitMs - elapsed),
    );
    buf.timer = setTimeout(() => void this.flush(key), wait);
  }

  hasPending(key: string): boolean {
    const buf = this.buffers.get(key);
    return !!buf && buf.entries.length > 0;
  }

  /**
   * Post any buffered texts for `key` as ONE payload: texts joined with "\n",
   * `msg_id` from the LAST message, base metadata (platform/user/thread/space)
   * from the first. Reply/edit extras from the most recent entry that set them
   * win. No-op when the buffer is empty.
   */
  async flush(key: string): Promise<void> {
    const buf = this.buffers.get(key);
    if (!buf) return;
    if (buf.timer) clearTimeout(buf.timer);
    this.buffers.delete(key);
    if (buf.entries.length === 0) return;

    const merged: InboundPayload = { ...buf.entries[0] };
    delete merged.reply_to;
    delete merged.reply_to_text;
    delete merged.edit_of;

    const texts: string[] = [];
    for (const entry of buf.entries) {
      texts.push(entry.text);
      if (entry.reply_to) merged.reply_to = entry.reply_to;
      if (entry.reply_to_text) merged.reply_to_text = entry.reply_to_text;
      if (entry.edit_of) merged.edit_of = entry.edit_of;
    }
    merged.text = texts.join("\n");
    merged.msg_id = buf.entries[buf.entries.length - 1].msg_id;

    try {
      await this.opts.post(key, merged);
    } catch (err) {
      this.opts.onError?.(key, err);
    }
  }

  /** Clear every pending timer (shutdown / tests). */
  dispose(): void {
    for (const buf of this.buffers.values()) {
      if (buf.timer) clearTimeout(buf.timer);
    }
    this.buffers.clear();
  }
}

export function createDebouncer(opts: DebouncerOptions): InboundDebouncer {
  return new InboundDebouncer(opts);
}

// ---------------------------------------------------------------------------
// Content router
// ---------------------------------------------------------------------------

export interface InboundRouterDeps {
  postToBackend: (path: string, body: InboundPayload) => Promise<void>;
  debouncer: InboundDebouncer;
  log: Logger;
}

export interface InboundContext {
  platform: string;
  senderId: string;
  threadID: string;
  spaceId: string;
}

const INBOUND_PATH = "/api/v1/platform/inbound";

function basePayload(
  ctx: InboundContext,
  msgId: string | undefined,
): InboundPayload {
  return {
    platform: ctx.platform,
    user_id: ctx.senderId,
    thread_id: ctx.threadID,
    text: "",
    space_id: ctx.spaceId,
    msg_id: msgId,
  };
}

/** Extract human-readable text from text-like content (for reply quotes). */
function textOfContent(content: Content): string | undefined {
  if (content.type === "text") return content.text;
  if (content.type === "markdown") return content.markdown;
  return undefined;
}

/**
 * Route one piece of inbound content. Recursive: reply/edit/effect unwrap
 * their inner content and re-enter with extras attached; group iterates its
 * member messages. Nothing is dropped silently — unknown types warn-log.
 */
export async function routeInboundContent(
  deps: InboundRouterDeps,
  ctx: InboundContext,
  message: Message,
  content: Content,
  extras: InboundExtras = {},
): Promise<void> {
  const { postToBackend, debouncer, log } = deps;

  switch (content.type) {
    case "reply": {
      const next: InboundExtras = { ...extras, reply_to: content.target.id };
      const quoted = textOfContent(content.target.content);
      if (quoted) next.reply_to_text = quoted.slice(0, REPLY_QUOTE_MAX_CHARS);
      await routeInboundContent(
        deps,
        ctx,
        message,
        content.content as Content,
        next,
      );
      return;
    }

    case "edit": {
      const next: InboundExtras = { ...extras, edit_of: content.target.id };
      await routeInboundContent(
        deps,
        ctx,
        message,
        content.content as Content,
        next,
      );
      return;
    }

    case "effect": {
      await routeInboundContent(
        deps,
        ctx,
        message,
        content.content as Content,
        extras,
      );
      return;
    }

    case "group": {
      for (const item of content.items) {
        await routeInboundContent(deps, ctx, item, item.content, extras);
      }
      return;
    }

    case "reaction": {
      // Tapbacks are their own turn — flush any pending text first so the
      // backend sees the words before the reaction to them.
      await debouncer.flush(ctx.threadID);
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        is_reaction: true,
        reaction_emoji: content.emoji,
        reply_to: content.target.id,
      };
      await postToBackend(INBOUND_PATH, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: "reaction",
          debounced: false,
        },
        "accepted inbound",
      );
      return;
    }

    case "poll_option": {
      if (!content.selected) {
        log.debug({ type: "poll_option" }, "ignoring unselected poll option");
        return;
      }
      const text = content.option?.title?.trim();
      if (!text) return;
      await debouncer.flush(ctx.threadID);
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        ...extras,
        text,
        is_poll_vote: true,
      };
      await postToBackend(INBOUND_PATH, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: "poll_option",
          debounced: false,
          text: text.slice(0, 60),
        },
        "accepted inbound",
      );
      return;
    }

    case "contact": {
      await debouncer.flush(ctx.threadID);
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        ...extras,
        is_contact: true,
        contact: contactFromSpectrum(content),
      };
      await postToBackend(INBOUND_PATH, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: "contact",
          debounced: false,
        },
        "accepted inbound",
      );
      return;
    }

    case "voice": {
      await debouncer.flush(ctx.threadID);
      let audioB64: string;
      try {
        const buf = await content.read();
        audioB64 = Buffer.from(buf).toString("base64");
      } catch (err) {
        log.error({ err }, "failed to read voice note");
        return;
      }
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        ...extras,
        is_voice: true,
        audio_b64: audioB64,
        audio_mime: content.mimeType,
      };
      await postToBackend(INBOUND_PATH, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: "voice",
          debounced: false,
          audio_len: audioB64.length,
        },
        "accepted inbound",
      );
      return;
    }

    case "attachment": {
      await debouncer.flush(ctx.threadID);
      const filename = content.name;
      if (isVCardMime(content.mimeType, filename)) {
        let vcardText = "";
        try {
          const buf = await content.read();
          vcardText = Buffer.from(buf).toString("utf8");
        } catch (err) {
          log.error({ err }, "failed to read vcard attachment");
          return;
        }
        const inbound: InboundPayload = {
          ...basePayload(ctx, message.id),
          ...extras,
          is_contact: true,
          vcard_text: vcardText,
        };
        await postToBackend(INBOUND_PATH, inbound);
        log.info(
          {
            sender: ctx.senderId,
            thread: ctx.threadID,
            type: "attachment",
            debounced: false,
            bytes: vcardText.length,
          },
          "accepted inbound",
        );
        return;
      }
      if (
        content.mimeType?.toLowerCase() === "application/pdf" ||
        filename?.toLowerCase().endsWith(".pdf")
      ) {
        let documentB64: string;
        try {
          const buf = await content.read();
          if (buf.byteLength > MAX_STATEMENT_BYTES) {
            log.warn(
              { filename, bytes: buf.byteLength },
              "ignoring oversized statement attachment",
            );
            return;
          }
          documentB64 = Buffer.from(buf).toString("base64");
        } catch (err) {
          log.error({ err }, "failed to read statement attachment");
          return;
        }
        const inbound: InboundPayload = {
          ...basePayload(ctx, message.id),
          ...extras,
          is_document: true,
          document_b64: documentB64,
          document_mime: "application/pdf",
          document_name: filename,
        };
        await postToBackend(INBOUND_PATH, inbound);
        log.info(
          {
            sender: ctx.senderId,
            thread: ctx.threadID,
            type: "attachment",
            document: "pdf",
            debounced: false,
            bytes: documentB64.length,
          },
          "accepted inbound",
        );
        return;
      }
      if (!content.mimeType?.startsWith("image/")) {
        log.debug({ mime: content.mimeType }, "ignoring non-image attachment");
        return;
      }
      let imageB64: string;
      try {
        const buf = await content.read();
        imageB64 = Buffer.from(buf).toString("base64");
      } catch (err) {
        log.error({ err }, "failed to read attachment");
        return;
      }
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        ...extras,
        is_image: true,
        image_b64: imageB64,
        image_mime: content.mimeType,
      };
      await postToBackend(INBOUND_PATH, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: "attachment",
          debounced: false,
          image_len: imageB64.length,
        },
        "accepted inbound",
      );
      return;
    }

    case "text":
    case "markdown": {
      const raw = content.type === "text" ? content.text : content.markdown;
      const text = raw?.trim();
      if (!text) return;
      const inbound: InboundPayload = {
        ...basePayload(ctx, message.id),
        ...extras,
        text,
      };
      debouncer.add(ctx.threadID, inbound);
      log.info(
        {
          sender: ctx.senderId,
          thread: ctx.threadID,
          type: content.type,
          debounced: true,
          text: text.slice(0, 60),
        },
        "accepted inbound",
      );
      return;
    }

    // Control signals, echo-only content, and membership events never reach
    // the backend.
    case "typing":
    case "read":
    case "poll":
    case "rename":
    case "avatar":
    case "unsend":
    case "richlink":
    case "app":
    case "custom":
      log.debug({ type: content.type }, "skipping non-message inbound content");
      return;

    default: {
      const type = (content as { type?: string })?.type;
      log.warn({ type }, "unhandled inbound content type");
      return;
    }
  }
}

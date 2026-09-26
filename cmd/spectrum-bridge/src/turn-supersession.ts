import { randomUUID } from "node:crypto";

/**
 * Inbound turn supersession (docs/miriam-inbound-supersession.md).
 *
 * One "turn" is one batch of user content the backend answers. The bridge mints
 * a turn id per inbound post, the backend echoes it on every reply generated
 * for that turn, and we drop a reply whose turn is no longer the thread's
 * latest — the user has moved on, so the older answer is stale.
 *
 * Safety rules, in order of importance:
 *  - Untagged replies (proactive sends, confirm/cancel acknowledgements) always
 *    deliver. A money action's receipt must never be dropped.
 *  - Only content-bearing inbound payloads mint a turn. A read receipt or an
 *    unsend carries no words and must never supersede a reply still in flight.
 *  - Everything fails open: disabled, unknown thread, or unknown turn ⇒ deliver.
 */
export interface TurnTaggablePayload {
  thread_id?: string;
  text?: string;
  turn_id?: string;
  is_voice?: boolean;
  is_image?: boolean;
  is_document?: boolean;
  is_contact?: boolean;
  is_poll_vote?: boolean;
}

/** True when a payload carries something the user actually sent. */
export function carriesUserContent(body: TurnTaggablePayload): boolean {
  if (typeof body.text === "string" && body.text.trim().length > 0) return true;
  return Boolean(
    body.is_voice ||
      body.is_image ||
      body.is_document ||
      body.is_contact ||
      body.is_poll_vote,
  );
}

export class TurnSupersession {
  private latest = new Map<string, string>();

  constructor(private readonly enabled: boolean) {}

  get isEnabled(): boolean {
    return this.enabled;
  }

  /** Mint (and record) a turn id for a thread. */
  mint(threadId: string): string {
    const turnId = randomUUID();
    this.latest.set(threadId, turnId);
    return turnId;
  }

  /**
   * Tag an inbound payload with a fresh turn id, but only when it carries user
   * content. Returns the turn id, or undefined when the payload is a lifecycle
   * signal that must not supersede anything. Idempotent: a payload that already
   * carries a turn id is left alone (retries reuse their original turn).
   */
  tagInbound(body: TurnTaggablePayload): string | undefined {
    if (!this.enabled) return undefined;
    if (!body.thread_id) return undefined;
    if (body.turn_id) return body.turn_id;
    if (!carriesUserContent(body)) return undefined;
    const turnId = this.mint(body.thread_id);
    body.turn_id = turnId;
    return turnId;
  }

  /**
   * True when a reply's turn has been superseded by a newer one. Fail-open:
   * disabled, untagged, or unknown-thread replies are never stale.
   */
  isStale(threadId: string | undefined, turnId: string | undefined): boolean {
    if (!this.enabled || !turnId || !threadId) return false;
    const latest = this.latest.get(threadId);
    if (latest === undefined) return false;
    return latest !== turnId;
  }

  /** Forget one thread's turn state, or all of it when no thread is given. */
  clear(threadId?: string): void {
    if (threadId === undefined) this.latest.clear();
    else this.latest.delete(threadId);
  }

  get trackedThreads(): number {
    return this.latest.size;
  }
}

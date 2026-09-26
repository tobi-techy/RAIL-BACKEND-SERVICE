import type { Logger } from "pino";
import { pollVotePayload, type InboundPayload } from "./inbound";

/**
 * iMessage poll votes, read from the provider instead of the webhook.
 *
 * Spectrum webhooks only ever emit the `messages` event, and a poll vote is not
 * one of the content arms they forward — a tap never arrives at our HTTP route.
 * The SDK does build votes (`poll_option` content) from the provider's poll
 * event stream, but that stream is merged into `app.messages`, which the
 * webhook transport deliberately never consumes; and even on the stream the SDK
 * drops a vote whose poll it cannot cache, because the Photon server returns
 * `PollCreated`/`PollInfo` without a title and the SDK's cache requires a
 * non-empty one.
 *
 * So the bridge reads votes from the raw provider client directly. That client
 * (and its `polls` resource) is already reachable through the SDK's platform
 * registry — the same registry `index.ts` taps to fix the webhook platform
 * alias — and it exposes the poll state without the fragile cache in between:
 *
 *   polls.get(pollMessageGuid)  -> { chatGuid, title, options[], votes[] }
 *   polls.subscribeEvents()     -> poll.changed { delta: voted/unvoted }
 *
 * Two paths feed one outcome: the live stream for prompt delivery, and an
 * on-demand `polls.get` reconciliation for anything that changed while the
 * stream was down (the provider's durable log makes `get` the source of truth).
 * Both funnel into the shared poll-vote payload, so the backend and the guest
 * brain need no change.
 *
 * Recovery runs at three moments, because a tap with no new inbound message has
 * nothing else to ride on: polling the poll just after we sent it, re-reading the
 * thread's polls when the person sends something, and re-reading every
 * outstanding poll when a dropped event stream comes back.
 */

/** One poll option, as the provider reports it. */
export interface RawPollOption {
  readonly optionIdentifier?: string;
  readonly text?: string;
}

/** Result of `polls.get(pollMessageGuid)`. */
export interface RawPoll {
  readonly pollMessageGuid?: string;
  readonly chatGuid?: string;
  readonly title?: string;
  readonly options?: readonly RawPollOption[];
  readonly votes?: readonly {
    readonly optionIdentifier?: string;
    readonly participant?: { readonly address?: string };
  }[];
}

/** The `delta` of a `poll.changed` event. */
export interface RawPollDelta {
  readonly type: string;
  readonly title?: string;
  readonly options?: readonly RawPollOption[];
  readonly optionIdentifier?: string;
}

/** A `poll.changed` event envelope. */
export interface RawPollEvent {
  readonly type?: string;
  readonly pollMessageGuid?: string;
  readonly chatGuid?: string;
  readonly sequence?: number;
  readonly isFromMe?: boolean;
  readonly occurredAt?: Date | string;
  readonly actor?: { readonly address?: string };
  readonly delta?: RawPollDelta;
}

/** The provider's poll API (structural — `@photon-ai/advanced-imessage` is a
 *  transitive dependency, so nothing is imported from it at build time). */
export interface RawPollsResource {
  get(pollMessage: string): Promise<RawPoll>;
  subscribeEvents(filter?: { pollMessage?: string }):
    | AsyncIterable<RawPollEvent>
    | { on(cb: (event: RawPollEvent) => unknown, onError?: (error: unknown) => void): () => void };
}

export interface RawIMessageClient {
  readonly polls?: RawPollsResource;
}

/** A provider client and the line it is bound to. */
export interface ResolvedPollClient {
  readonly phone: string;
  readonly client: RawIMessageClient;
}

/**
 * Pull the raw provider clients out of the Spectrum instance. Returns an empty
 * list when the registry shape is not what we expect — the watcher then stays
 * inert rather than breaking the bridge.
 */
export function resolveIMessageClients(spectrum: unknown): ResolvedPollClient[] {
  const platforms = (spectrum as { __internal?: { platforms?: Map<string, unknown> } } | null)
    ?.__internal?.platforms;
  if (!platforms || typeof platforms.get !== "function") return [];
  // The map is keyed by the provider's display name; live envelopes use the
  // lowercased form (the same discrepancy index.ts aliases for webhook routing).
  const runtime = platforms.get("iMessage") ?? platforms.get("imessage");
  if (!runtime) return [];
  const raw = (runtime as { client?: unknown }).client;
  return normalizeClients(raw);
}

function normalizeClients(raw: unknown): ResolvedPollClient[] {
  const hasPolls = (value: unknown): value is RawIMessageClient =>
    typeof value === "object" &&
    value !== null &&
    typeof (value as RawIMessageClient).polls?.get === "function" &&
    typeof (value as RawIMessageClient).polls?.subscribeEvents === "function";

  if (Array.isArray(raw)) {
    const out: ResolvedPollClient[] = [];
    for (const entry of raw) {
      const candidate = entry as { phone?: unknown; client?: unknown };
      if (!hasPolls(candidate?.client)) continue;
      out.push({
        phone: typeof candidate.phone === "string" ? candidate.phone : "shared",
        client: candidate.client,
      });
    }
    return out;
  }
  if (hasPolls(raw)) return [{ phone: "shared", client: raw }];
  return [];
}

/** A poll the bridge sent and is still waiting on a tap for. */
export interface PollRegistrationInput {
  pollGuid: string;
  threadId: string;
  senderId: string;
  platform: string;
  pollTitle: string;
  options: readonly string[];
}

interface TrackedPoll {
  pollGuid: string;
  threadId: string;
  senderId: string;
  platform: string;
  pollTitle: string;
  sentAt: number;
  /** optionIdentifier -> option text, learned from the provider. */
  optionsById: Map<string, string>;
  /** Option identifiers already handed to the backend (stream + reconcile). */
  forwarded: Set<string>;
  /**
   * Serializes vote handling for this one poll, so the choices reach the backend
   * in the order they were made. Resolving an option's text awaits the provider,
   * which would otherwise let a later tap's POST overtake an earlier one — on a
   * Confirm/Cancel poll that is the difference between confirming and cancelling.
   */
  queue: Promise<void>;
}

export interface PollWatcherDeps {
  log: Logger;
  clients: readonly ResolvedPollClient[];
  /** Post a poll vote as an inbound turn (the caller flushes the thread first). */
  postVote: (payload: InboundPayload) => Promise<void>;
  now?: () => number;
  /** How long a sent poll stays eligible for a vote. */
  ttlMs?: number;
  /** Backoff floor for a dropped event stream (tests shorten it). */
  reconnectMinMs?: number;
}

export interface PollWatcherStats {
  clients: number;
  connected: number;
  tracked: number;
  votes_forwarded: number;
  votes_dropped: number;
  /** Times the provider was re-read because the live stream had dropped. */
  reconciles: number;
}

const DEFAULT_POLL_TTL_MS = 24 * 60 * 60 * 1000;
const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 30_000;
const SWEEP_INTERVAL_MS = 10 * 60_000;

/**
 * Canonical form of a provider address, so "+234 916 490 4178",
 * "+2349164904178" and "2349164904178" all match, while an email address still
 * compares as itself (the provider reports whichever the participant uses).
 */
function normalizeAddress(value: string | undefined): string {
  return (value ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
}

export class PollWatcher {
  private readonly tracked = new Map<string, TrackedPoll>();
  private readonly byThread = new Map<string, Set<string>>();
  private readonly subscriptions = new Map<string, { stop: () => void }>();
  private readonly reconnectTimers = new Set<NodeJS.Timeout>();
  /** Lines whose next successful connect owes a state re-read. */
  private readonly reconnectRecovery = new Set<string>();
  private sweepTimer: NodeJS.Timeout | null = null;
  private started = false;
  private disposed = false;
  private reconnectAttempt = 0;
  private votesForwarded = 0;
  private votesDropped = 0;
  private reconciles = 0;

  private readonly log: Logger;
  private readonly ttlMs: number;

  constructor(private readonly deps: PollWatcherDeps) {
    this.log = deps.log;
    this.ttlMs = deps.ttlMs ?? DEFAULT_POLL_TTL_MS;
  }

  get enabled(): boolean {
    return this.deps.clients.length > 0;
  }

  get stats(): PollWatcherStats {
    return {
      clients: this.deps.clients.length,
      connected: this.subscriptions.size,
      tracked: this.tracked.size,
      votes_forwarded: this.votesForwarded,
      votes_dropped: this.votesDropped,
      reconciles: this.reconciles,
    };
  }

  start(): void {
    if (this.started || this.disposed) return;
    this.started = true;
    if (this.deps.clients.length === 0) {
      this.log.warn(
        "poll watcher found no iMessage poll client; taps on polls will not be delivered",
      );
      return;
    }
    for (const entry of this.deps.clients) this.connect(entry);
    this.sweepTimer = setInterval(() => this.sweep(), SWEEP_INTERVAL_MS);
    this.log.info(
      { clients: this.deps.clients.map((c) => c.phone) },
      "poll watcher subscribed to provider poll events",
    );
  }

  /**
   * Remember a poll the bridge just sent, so a vote on it can be attributed to
   * this thread. `pollGuid` is the sent iMessage message id — the SDK's
   * outbound poll record uses `poll.pollMessageGuid` as the message id, so the
   * value `space.send(poll(...))` resolves to IS the poll's identifier.
   */
  registerPoll(input: PollRegistrationInput): void {
    if (this.disposed || this.deps.clients.length === 0) return;
    const pollGuid = (input.pollGuid ?? "").trim();
    if (!pollGuid) return;
    const tracked: TrackedPoll = {
      pollGuid,
      threadId: input.threadId,
      senderId: input.senderId,
      platform: input.platform,
      pollTitle: input.pollTitle,
      sentAt: this.now(),
      optionsById: new Map(),
      forwarded: new Set(),
      queue: Promise.resolve(),
    };
    this.tracked.set(pollGuid, tracked);
    let guids = this.byThread.get(tracked.threadId);
    if (!guids) {
      guids = new Set();
      this.byThread.set(tracked.threadId, guids);
    }
    guids.add(pollGuid);
    this.log.debug(
      { poll_guid: pollGuid, thread_id: tracked.threadId },
      "watching a sent poll for a vote",
    );
    // Learn the option identifiers and pick up a vote that landed before the
    // stream (or the bridge) was up.
    void this.prime(pollGuid).catch((err) =>
      this.log.warn({ err, poll_guid: pollGuid }, "poll priming failed"),
    );
  }

  hasTrackedPoll(threadId: string): boolean {
    return (this.byThread.get(threadId)?.size ?? 0) > 0;
  }

  /**
   * Re-read every poll we are still waiting on in this thread. Called when the
   * user sends something new, so a tap that happened while the live stream was
   * down is still counted (and counted before their message, so onboarding
   * state advances in the order the person actually acted).
   */
  async reconcileThread(threadId: string): Promise<void> {
    const guids = this.byThread.get(threadId);
    if (!guids || guids.size === 0) return;
    for (const pollGuid of Array.from(guids)) {
      const tracked = this.tracked.get(pollGuid);
      if (!tracked) continue;
      await this.reconcilePoll(tracked);
    }
  }

  /**
   * Re-read every outstanding poll, regardless of thread. Runs when a live
   * stream (re)connects: a tap that landed while the stream was down produces no
   * event, and if the person typed nothing there is no inbound message to
   * piggyback `reconcileThread` on — so without this the tap would sit
   * unanswered until the poll aged out.
   *
   * State-based on purpose. `im.events.catchUp(sequence)` is the documented
   * recovery for missing the exact transitions, but for our purposes the current
   * vote state is what the backend needs (a tap is an answer, and the last choice
   * the person made is the answer), and unlike a replay it cannot double-count a
   * tap that is later seen arriving live as well.
   */
  async reconcileAll(reason: string): Promise<void> {
    const outstanding = Array.from(this.tracked.values());
    if (outstanding.length === 0) return;
    this.reconciles++;
    this.log.info(
      { polls: outstanding.length, reason },
      "reconciling outstanding polls against the provider",
    );
    for (const tracked of outstanding) {
      await this.reconcilePoll(tracked);
    }
  }

  /** One poll's authoritative state, with any unforwarded votes delivered. */
  private async reconcilePoll(tracked: TrackedPoll): Promise<void> {
    const poll = await this.fetchPoll(tracked.pollGuid);
    if (!poll) return;
    this.applyPollState(tracked, poll.title, poll.options);
    for (const vote of poll.votes ?? []) {
      await this.forwardVote(tracked, vote.optionIdentifier, vote.participant?.address);
    }
  }

  dispose(): void {
    this.disposed = true;
    if (this.sweepTimer) clearInterval(this.sweepTimer);
    this.sweepTimer = null;
    for (const timer of this.reconnectTimers) clearTimeout(timer);
    this.reconnectTimers.clear();
    for (const sub of this.subscriptions.values()) {
      try {
        sub.stop();
      } catch {
        // Closing an already-dead stream is not an error worth surfacing.
      }
    }
    this.subscriptions.clear();
    this.tracked.clear();
    this.byThread.clear();
  }

  // -- live stream ---------------------------------------------------------

  private connect(entry: ResolvedPollClient): void {
    if (this.disposed) return;
    const key = entry.phone;
    const existing = this.subscriptions.get(key);
    if (existing) {
      try {
        existing.stop();
      } catch {
        // Ignore; replaced below.
      }
      this.subscriptions.delete(key);
    }

    const polls = entry.client.polls;
    if (!polls) return;

    let stream: ReturnType<RawPollsResource["subscribeEvents"]>;
    try {
      stream = polls.subscribeEvents();
    } catch (err) {
      this.log.warn({ err, phone: key }, "poll event subscribe failed");
      this.scheduleReconnect(entry);
      return;
    }

    let stopped = false;
    const onEvent = (event: RawPollEvent): void => {
      if (stopped || this.disposed) return;
      void this.handleEvent(event).catch((err) =>
        this.log.warn({ err, phone: key }, "poll event handling failed"),
      );
    };
    const onStreamError = (err: unknown): void => {
      if (stopped || this.disposed) return;
      this.log.warn({ err, phone: key }, "poll event stream failed; reconnecting");
      this.scheduleReconnect(entry);
    };

    if (typeof (stream as { on?: unknown }).on === "function") {
      const stopFn = (
        stream as { on: (cb: (e: RawPollEvent) => unknown, onErr?: (e: unknown) => void) => () => void }
      ).on(onEvent, onStreamError);
      this.subscriptions.set(key, {
        stop: () => {
          stopped = true;
          stopFn();
        },
      });
      this.reconnectAttempt = 0;
      this.recoverAfterReconnect(key);
      return;
    }

    // Async-iterable stream (the SDK's TypedEventStream supports both shapes).
    const readable = stream as AsyncIterable<RawPollEvent> & { close?: () => void };
    this.subscriptions.set(key, {
      stop: () => {
        stopped = true;
        try {
          readable.close?.();
        } catch {
          // Already closed.
        }
      },
    });
    this.reconnectAttempt = 0;
    this.recoverAfterReconnect(key);
    void (async () => {
      try {
        for await (const event of readable) {
          if (stopped || this.disposed) break;
          await this.handleEvent(event);
        }
      } catch (err) {
        onStreamError(err);
        return;
      }
      if (!stopped && !this.disposed) onStreamError(new Error("poll event stream ended"));
    })();
  }

  /**
   * A stream that just came back may have missed a tap entirely — the provider
   * emits nothing for what happened while we were disconnected, and if the person
   * typed nothing there is no inbound message for `reconcileThread` to ride on.
   * Re-read the provider's state instead of waiting for an event. Runs alongside
   * the live stream, never before it.
   */
  private recoverAfterReconnect(key: string): void {
    if (!this.reconnectRecovery.delete(key)) return;
    void this.reconcileAll("stream-reconnect").catch((err) =>
      this.log.warn({ err, phone: key }, "poll reconciliation after reconnect failed"),
    );
  }

  private scheduleReconnect(entry: ResolvedPollClient): void {
    if (this.disposed) return;
    const delay = Math.min(
      (this.deps.reconnectMinMs ?? RECONNECT_MIN_MS) * 2 ** this.reconnectAttempt,
      RECONNECT_MAX_MS,
    );
    this.reconnectAttempt++;
    // The next successful connect for this line owes us a state re-read.
    this.reconnectRecovery.add(entry.phone);
    this.log.warn({ phone: entry.phone, retry_in_ms: delay }, "reconnecting poll event stream");
    const timer = setTimeout(() => {
      this.reconnectTimers.delete(timer);
      this.connect(entry);
    }, delay);
    this.reconnectTimers.add(timer);
  }

  private async handleEvent(event: RawPollEvent): Promise<void> {
    // Our own vote (or a vote from the account's other device) is not an answer
    // to the question we asked.
    if (event.isFromMe) return;
    const pollGuid = event.pollMessageGuid;
    const delta = event.delta;
    if (!pollGuid || !delta) return;
    const tracked = this.tracked.get(pollGuid);
    if (!tracked) return;

    switch (delta.type) {
      case "created":
      case "optionAdded":
        // The server reports these without a title; the identifiers are the only
        // part we need, so an empty title is a non-event here.
        this.applyPollState(tracked, delta.title, delta.options);
        return;
      case "voted":
        await this.forwardVote(tracked, delta.optionIdentifier, event.actor?.address);
        return;
      case "unvoted":
        // The user took their choice back. The staged action stays pending; they
        // can vote again (a new `voted` event) and that still forwards.
        this.log.debug(
          { poll_guid: pollGuid, thread_id: tracked.threadId },
          "poll vote retracted",
        );
        return;
      default:
        return;
    }
  }

  // -- vote attribution ----------------------------------------------------

  private applyPollState(
    tracked: TrackedPoll,
    title: string | undefined,
    options: readonly RawPollOption[] | undefined,
  ): void {
    for (const option of options ?? []) {
      const identifier = (option.optionIdentifier ?? "").trim();
      const text = (option.text ?? "").trim();
      if (identifier && text) tracked.optionsById.set(identifier, text);
    }
    const trimmedTitle = (title ?? "").trim();
    if (trimmedTitle) tracked.pollTitle = trimmedTitle;
  }

  private async prime(pollGuid: string): Promise<void> {
    const tracked = this.tracked.get(pollGuid);
    if (!tracked) return;
    await this.reconcilePoll(tracked);
  }

  /** Ask each configured line for the poll; only one of them owns it. */
  private async fetchPoll(pollGuid: string): Promise<RawPoll | undefined> {
    for (const entry of this.deps.clients) {
      const polls = entry.client.polls;
      if (!polls) continue;
      try {
        const poll = await polls.get(pollGuid);
        if (poll) return poll;
      } catch (err) {
        this.log.debug(
          { err, poll_guid: pollGuid, phone: entry.phone },
          "poll state fetch failed on this line",
        );
      }
    }
    return undefined;
  }

  /**
   * Hand one sighting of a vote to the backend, behind this poll's queue. Never
   * rejects: a failed forward is retried by the next reconciliation, and a
   * rejection here would surface as an unhandled error in the event stream.
   */
  private forwardVote(
    tracked: TrackedPoll,
    optionIdentifier: string | undefined,
    actorAddress: string | undefined,
  ): Promise<void> {
    const next = tracked.queue.then(() =>
      this.forwardVoteOnce(tracked, optionIdentifier, actorAddress),
    );
    tracked.queue = next.catch(() => undefined);
    return next;
  }

  private async forwardVoteOnce(
    tracked: TrackedPoll,
    optionIdentifier: string | undefined,
    actorAddress: string | undefined,
  ): Promise<void> {
    const optionId = (optionIdentifier ?? "").trim();
    if (!optionId) return;

    // A vote with a named voter who is not this thread's user (the bot, or
    // another participant in a group) is not an answer to our question. An
    // unnamed voter in a DM is assumed to be the person we asked.
    const voter = normalizeAddress(actorAddress);
    const expected = normalizeAddress(tracked.senderId);
    if (voter && expected && voter !== expected) {
      this.log.debug(
        { poll_guid: tracked.pollGuid, thread_id: tracked.threadId },
        "ignoring poll vote from another participant",
      );
      return;
    }

    let text = tracked.optionsById.get(optionId);
    if (!text) {
      // An option added after we sent the poll, or identifiers we never primed.
      const poll = await this.fetchPoll(tracked.pollGuid);
      if (poll) {
        this.applyPollState(tracked, poll.title, poll.options);
        text = tracked.optionsById.get(optionId);
      }
    }
    if (!text) {
      this.votesDropped++;
      this.log.warn(
        { poll_guid: tracked.pollGuid, thread_id: tracked.threadId, option_identifier: optionId },
        "poll vote could not be resolved to its option text; dropping",
      );
      return;
    }

    if (tracked.forwarded.has(optionId)) {
      this.log.debug(
        { poll_guid: tracked.pollGuid, thread_id: tracked.threadId, option_identifier: optionId },
        "poll vote already forwarded",
      );
      return;
    }
    tracked.forwarded.add(optionId);

    const payload = pollVotePayload({
      platform: tracked.platform,
      senderId: tracked.senderId,
      threadId: tracked.threadId,
      optionText: text,
      pollTitle: tracked.pollTitle,
    });

    try {
      await this.deps.postVote(payload);
      this.votesForwarded++;
      this.log.info(
        {
          sender: tracked.senderId,
          thread: tracked.threadId,
          type: "poll_option",
          poll_guid: tracked.pollGuid,
          debounced: false,
          text: text.slice(0, 60),
        },
        "accepted inbound",
      );
    } catch (err) {
      // Let a later reconcile retry this vote rather than dropping the tap.
      tracked.forwarded.delete(optionId);
      this.log.error(
        { err, poll_guid: tracked.pollGuid, thread_id: tracked.threadId },
        "poll vote forward failed",
      );
    }
  }

  // -- upkeep --------------------------------------------------------------

  private sweep(): void {
    const cutoff = this.now() - this.ttlMs;
    for (const [pollGuid, tracked] of Array.from(this.tracked.entries())) {
      if (tracked.sentAt > cutoff) continue;
      this.tracked.delete(pollGuid);
      const guids = this.byThread.get(tracked.threadId);
      if (guids) {
        guids.delete(pollGuid);
        if (guids.size === 0) this.byThread.delete(tracked.threadId);
      }
    }
  }

  private now(): number {
    return this.deps.now ? this.deps.now() : Date.now();
  }
}

import { describe, expect, it } from "bun:test";
import {
  PollWatcher,
  resolveIMessageClients,
  type RawPoll,
  type RawPollEvent,
  type RawPollsResource,
  type ResolvedPollClient,
} from "./poll-watcher";
import type { InboundPayload } from "./inbound";
import { getLogger } from "./logger";

const log = getLogger();

/** One tick of the event loop, so the watcher's fire-and-forget work settles. */
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 1));

const POLL_GUID = "spc-msg-poll-1";
const THREAD = "any;-;+15551234567";
const SENDER = "+15551234567";

function pollState(overrides: Partial<RawPoll> = {}): RawPoll {
  return {
    pollMessageGuid: POLL_GUID,
    chatGuid: THREAD,
    title: "Which one usually eats it most?",
    options: [
      { optionIdentifier: "option-1", text: "Food and snacks" },
      { optionIdentifier: "option-2", text: "Rides/transport" },
    ],
    votes: [],
    ...overrides,
  };
}

/**
 * Minimal stand-in for `client.polls`. `getResults` is a scripted queue so a
 * test can model "the provider had no state yet" then "it does now".
 */
class FakePolls implements RawPollsResource {
  private readonly handlers = new Set<(event: RawPollEvent) => void>();
  private readonly errorHandlers = new Set<(error: unknown) => void>();
  getCalls = 0;
  subscribeCalls = 0;

  constructor(
    private readonly getResults: Array<RawPoll | Error>,
    private readonly fallback: RawPoll = pollState(),
  ) {}

  async get(_pollMessage: string): Promise<RawPoll> {
    this.getCalls++;
    const next = this.getResults.shift();
    const result = next ?? this.fallback;
    if (result instanceof Error) throw result;
    return result;
  }

  subscribeEvents(): { on(cb: (e: RawPollEvent) => unknown, onError?: (e: unknown) => void): () => void } {
    this.subscribeCalls++;
    return {
      on: (cb, onError) => {
        this.handlers.add(cb);
        if (onError) this.errorHandlers.add(onError);
        return () => {
          this.handlers.delete(cb);
        };
      },
    };
  }

  emit(event: RawPollEvent): void {
    for (const handler of this.handlers) handler(event);
  }

  /** Simulate the provider dropping the stream. */
  fail(error: unknown): void {
    for (const handler of this.errorHandlers) handler(error);
  }
}

function votedEvent(optionIdentifier: string, actorAddress: string | undefined = SENDER): RawPollEvent {
  return {
    type: "poll.changed",
    pollMessageGuid: POLL_GUID,
    chatGuid: THREAD,
    sequence: 4,
    isFromMe: false,
    ...(actorAddress ? { actor: { address: actorAddress } } : {}),
    delta: { type: "voted", optionIdentifier },
  };
}

function makeWatcher(polls: FakePolls, posts: InboundPayload[], reconnectMinMs?: number) {
  const client: ResolvedPollClient = { phone: "+15550000000", client: { polls } };
  const watcher = new PollWatcher({
    log,
    clients: [client],
    postVote: async (payload) => {
      posts.push(payload);
    },
    ...(reconnectMinMs === undefined ? {} : { reconnectMinMs }),
  });
  return { watcher, client };
}

function register(watcher: PollWatcher, senderId: string = SENDER): void {
  watcher.registerPoll({
    pollGuid: POLL_GUID,
    threadId: THREAD,
    senderId,
    platform: "imessage",
    pollTitle: "Which one usually eats it most?",
    options: ["Food and snacks", "Rides/transport"],
  });
}

describe("resolveIMessageClients", () => {
  it("reads the provider client out of the SDK platform registry", () => {
    const polls = new FakePolls([]);
    const spectrum = {
      __internal: {
        platforms: new Map([["iMessage", { client: [{ phone: "+15550000000", client: { polls } }] }]]),
      },
    };

    const clients = resolveIMessageClients(spectrum);

    expect(clients).toHaveLength(1);
    expect(clients[0].phone).toBe("+15550000000");
  });

  it("returns nothing when the registry or the poll API is absent", () => {
    expect(resolveIMessageClients(undefined)).toEqual([]);
    expect(resolveIMessageClients({})).toEqual([]);
    expect(
      resolveIMessageClients({
        __internal: { platforms: new Map([["iMessage", { client: [{ phone: "+1", client: {} }] }]]) },
      }),
    ).toEqual([]);
  });
});

describe("PollWatcher", () => {
  it("forwards a tap on a poll the bridge sent", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-1"));
    await tick();

    expect(posts).toHaveLength(1);
    expect(posts[0].is_poll_vote).toBe(true);
    expect(posts[0].text).toBe("Food and snacks");
    expect(posts[0].poll_title).toBe("Which one usually eats it most?");
    expect(posts[0].user_id).toBe(SENDER);
    expect(posts[0].thread_id).toBe(THREAD);
    expect(posts[0].space_id).toBe(THREAD);
    watcher.dispose();
  });

  it("forwards the vote even when the provider returned the poll without a title", async () => {
    // The exact shape that makes the SDK's own poll cache reject the poll.
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([
      pollState({ title: "", options: [{ optionIdentifier: "option-1", text: "Food and snacks" }] }),
    ]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit({
      ...votedEvent("option-1"),
      delta: { type: "created", title: "", options: [{ optionIdentifier: "option-1", text: "Food and snacks" }] },
    });
    polls.emit(votedEvent("option-1"));
    await tick();

    expect(posts).toHaveLength(1);
    expect(posts[0].text).toBe("Food and snacks");
    watcher.dispose();
  });

  it("ignores a vote from someone other than the person we asked", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-1", "+15559999999"));
    await tick();

    expect(posts).toHaveLength(0);
    watcher.dispose();
  });

  it("assumes the addressee when the provider names no voter", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-2", undefined));
    await tick();

    expect(posts).toHaveLength(1);
    expect(posts[0].text).toBe("Rides/transport");
    watcher.dispose();
  });

  it("ignores unvoted and our own votes", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit({ ...votedEvent("option-1"), delta: { type: "unvoted", optionIdentifier: "option-1" } });
    polls.emit({ ...votedEvent("option-1"), isFromMe: true });
    await tick();

    expect(posts).toHaveLength(0);
    watcher.dispose();
  });

  it("resolves the option text from the provider when identifiers were never primed", async () => {
    const posts: InboundPayload[] = [];
    // The prime read finds nothing; the vote read finds the option list.
    const polls = new FakePolls([new Error("not found"), pollState()]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-2"));
    await tick();

    expect(posts).toHaveLength(1);
    expect(posts[0].text).toBe("Rides/transport");
    watcher.dispose();
  });

  it("forwards a tap only once across the stream and a reconciliation", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([
      pollState(),
      pollState({ votes: [{ optionIdentifier: "option-1", participant: { address: SENDER } }] }),
    ]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-1"));
    await tick();
    await watcher.reconcileThread(THREAD);
    await tick();

    expect(posts).toHaveLength(1);
    watcher.dispose();
  });

  it("delivers a poll's votes in the order they were tapped", async () => {
    // Two taps on one poll: on a Confirm/Cancel confirmation the order decides
    // the outcome, so a slow first POST must not let the second overtake it.
    const completions: string[] = [];
    const polls = new FakePolls([]);
    const client: ResolvedPollClient = { phone: "+15550000000", client: { polls } };
    const watcher = new PollWatcher({
      log,
      clients: [client],
      postVote: async (payload) => {
        // The first tap is the slow one, as a real provider call would be.
        await new Promise((resolve) => setTimeout(resolve, completions.length === 0 ? 20 : 0));
        completions.push(payload.text);
      },
    });
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-1"));
    polls.emit(votedEvent("option-2"));
    await new Promise((resolve) => setTimeout(resolve, 60));

    expect(completions).toEqual(["Food and snacks", "Rides/transport"]);
    watcher.dispose();
  });

  it("recovers a tap that landed while the live stream was down", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([
      pollState(),
      pollState({ votes: [{ optionIdentifier: "option-2", participant: { address: SENDER } }] }),
    ]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher);
    await tick();

    // No stream event at all — the reconcile read is the only source.
    expect(watcher.hasTrackedPoll(THREAD)).toBe(true);
    await watcher.reconcileThread(THREAD);

    expect(posts).toHaveLength(1);
    expect(posts[0].text).toBe("Rides/transport");
    watcher.dispose();
  });

  it("recovers a tap when the stream returns and the person typed nothing", async () => {
    // The gap a per-thread reconcile cannot cover: the tap happened while the
    // stream was down and no new inbound message ever arrives to ride on, so the
    // stream coming back is the only cue that we may have missed it.
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([
      pollState(),
      pollState({ votes: [{ optionIdentifier: "option-1", participant: { address: SENDER } }] }),
    ]);
    const { watcher } = makeWatcher(polls, posts, 1);
    watcher.start();
    register(watcher);
    await tick();

    polls.fail(new Error("stream reset by peer"));
    await new Promise((resolve) => setTimeout(resolve, 30));

    expect(posts).toHaveLength(1);
    expect(posts[0].text).toBe("Food and snacks");
    expect(posts[0].is_poll_vote).toBe(true);
    expect(watcher.stats.reconciles).toBe(1);
    watcher.dispose();
  });

  it("does not re-read state on the first connect", async () => {
    // booting is not a reconnect: there is nothing to recover from yet, and the
    // send-time prime already read the poll.
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts, 1);
    watcher.start();
    register(watcher);
    await tick();

    expect(watcher.stats.reconciles).toBe(0);
    watcher.dispose();
  });

  it("matches an email-addressed participant regardless of case", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    register(watcher, "alice@example.com");
    await tick();

    polls.emit(votedEvent("option-1", "Alice@Example.COM"));
    await tick();
    expect(posts).toHaveLength(1);

    // A different address is still not the person we asked.
    polls.emit(votedEvent("option-2", "bob@example.com"));
    await tick();
    expect(posts).toHaveLength(1);
    watcher.dispose();
  });

  it("never posts a vote for a poll it did not send", async () => {
    const posts: InboundPayload[] = [];
    const polls = new FakePolls([]);
    const { watcher } = makeWatcher(polls, posts);
    watcher.start();
    await tick();

    polls.emit(votedEvent("option-1"));
    await tick();

    expect(posts).toHaveLength(0);
    expect(watcher.hasTrackedPoll(THREAD)).toBe(false);
    watcher.dispose();
  });

  it("keeps a failed forward retryable instead of eating the tap", async () => {
    const posts: InboundPayload[] = [];
    // Prime reads state with no vote; the reconcile read reports the tap.
    const polls = new FakePolls(
      [pollState()],
      pollState({ votes: [{ optionIdentifier: "option-1", participant: { address: SENDER } }] }),
    );
    const client: ResolvedPollClient = { phone: "+15550000000", client: { polls } };
    let failNext = true;
    const watcher = new PollWatcher({
      log,
      clients: [client],
      postVote: async (payload) => {
        if (failNext) {
          failNext = false;
          throw new Error("backend down");
        }
        posts.push(payload);
      },
    });
    watcher.start();
    register(watcher);
    await tick();

    polls.emit(votedEvent("option-1"));
    await tick();
    expect(posts).toHaveLength(0);

    await watcher.reconcileThread(THREAD);
    expect(posts).toHaveLength(1);
    watcher.dispose();
  });

  it("stays inert with no resolvable client", async () => {
    const posts: InboundPayload[] = [];
    const watcher = new PollWatcher({
      log,
      clients: [],
      postVote: async (payload) => {
        posts.push(payload);
      },
    });
    watcher.start();
    register(watcher);
    await tick();

    expect(watcher.enabled).toBe(false);
    expect(watcher.hasTrackedPoll(THREAD)).toBe(false);
    expect(posts).toHaveLength(0);
    watcher.dispose();
  });
});

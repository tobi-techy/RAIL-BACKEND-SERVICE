import { describe, expect, it } from "bun:test";
import type { Content, Message } from "spectrum-ts";
import {
  InboundDebouncer,
  isOutboundEcho,
  routeInboundContent,
  type InboundContext,
  type InboundPayload,
  type InboundRouterDeps,
} from "./inbound";
import { getLogger } from "./logger";

const log = getLogger();

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function payload(text: string, msgId: string, extras: Partial<InboundPayload> = {}): InboundPayload {
  return {
    platform: "imessage",
    user_id: "+15551234567",
    thread_id: "chat-1",
    space_id: "chat-1",
    text,
    msg_id: msgId,
    ...extras,
  };
}

function fakeMessage(id: string, content: Content, direction: "inbound" | "outbound" = "inbound"): Message {
  return { id, content, direction } as unknown as Message;
}

function route(deps: InboundRouterDeps, msg: Message): Promise<void> {
  return routeInboundContent(deps, ctx, msg, msg.content);
}

function makeRouter(posts: InboundPayload[], debouncer: InboundDebouncer): InboundRouterDeps {
  return {
    postToBackend: async (_path, body) => {
      posts.push(body);
    },
    debouncer,
    log,
  };
}

const ctx: InboundContext = {
  platform: "imessage",
  senderId: "+15551234567",
  threadID: "chat-1",
  spaceId: "chat-1",
};

describe("isOutboundEcho", () => {
  it("skips outbound-direction messages", () => {
    expect(isOutboundEcho(fakeMessage("m1", { type: "text", text: "hi" } as Content, "outbound"))).toBe(true);
    expect(isOutboundEcho(fakeMessage("m2", { type: "text", text: "hi" } as Content, "inbound"))).toBe(false);
  });
});

describe("InboundDebouncer", () => {
  it("coalesces a 3-message burst into one posted payload", async () => {
    const posts: InboundPayload[] = [];
    const d = new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 40, maxWaitMs: 5000, maxBuffer: 5 });

    d.add("chat-1", payload("Hey", "m1"));
    d.add("chat-1", payload("Oluwatobiloba", "m2"));
    d.add("chat-1", payload("Hi", "m3"));
    expect(posts.length).toBe(0);

    await sleep(120);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("Hey\nOluwatobiloba\nHi");
    // LAST message's id, first message's thread metadata
    expect(posts[0].msg_id).toBe("m3");
    expect(posts[0].thread_id).toBe("chat-1");
    expect(posts[0].user_id).toBe("+15551234567");
    d.dispose();
  });

  it("resets the quiet-window timer on each new message", async () => {
    const posts: InboundPayload[] = [];
    const d = new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 60, maxWaitMs: 5000, maxBuffer: 5 });

    d.add("chat-1", payload("one", "m1"));
    await sleep(40); // t=40, timer would have fired at t=60 without reset
    d.add("chat-1", payload("two", "m2"));
    await sleep(30); // t=70 — past the original deadline, before the reset one
    expect(posts.length).toBe(0);
    await sleep(80); // t=150 — past t=40+60
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("one\ntwo");
    d.dispose();
  });

  it("hard-flushes at max wait even if messages keep arriving", async () => {
    const posts: InboundPayload[] = [];
    const d = new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 500, maxWaitMs: 120, maxBuffer: 50 });

    d.add("chat-1", payload("a", "m1"));
    await sleep(60);
    d.add("chat-1", payload("b", "m2")); // resets debounce, but max-wait caps at t=120
    await sleep(50); // t=110 — debounce (t=560) not reached
    expect(posts.length).toBe(0);
    await sleep(60); // t=170 — past the 120ms hard cap
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("a\nb");
    expect(posts[0].msg_id).toBe("m2");
    d.dispose();
  });

  it("hard-flushes immediately when the buffer hits maxBuffer", async () => {
    const posts: InboundPayload[] = [];
    const d = new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 5000, maxWaitMs: 60000, maxBuffer: 2 });

    d.add("chat-1", payload("a", "m1"));
    d.add("chat-1", payload("b", "m2"));
    await sleep(10);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("a\nb");
    d.dispose();
  });

  it("keeps separate buffers per space", async () => {
    const posts: InboundPayload[] = [];
    const d = new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 40, maxWaitMs: 5000, maxBuffer: 5 });

    d.add("chat-1", payload("hello", "m1"));
    d.add("chat-2", payload("other space", "m9", { thread_id: "chat-2", space_id: "chat-2" }));
    await sleep(120);
    expect(posts.length).toBe(2);
    expect(posts.find((p) => p.thread_id === "chat-1")?.text).toBe("hello");
    expect(posts.find((p) => p.thread_id === "chat-2")?.text).toBe("other space");
    d.dispose();
  });
});

describe("routeInboundContent", () => {
  function makeDebouncer(posts: InboundPayload[]): InboundDebouncer {
    return new InboundDebouncer({ post: (_k, p) => posts.push(p), debounceMs: 40, maxWaitMs: 5000, maxBuffer: 5 });
  }

  it("flushes the pending buffer before posting a poll vote immediately", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    await route(deps, fakeMessage("m1", { type: "text", text: "confirming" } as Content));
    expect(posts.length).toBe(0); // still buffered

    const vote = {
      type: "poll_option",
      option: { title: "Confirm" },
      poll: { type: "poll", title: "Confirm?", options: [{ title: "Confirm" }] },
      selected: true,
      title: "Confirm",
    } as unknown as Content;
    await route(deps, fakeMessage("m2", vote));

    expect(posts.length).toBe(2);
    expect(posts[0].text).toBe("confirming"); // buffered text flushed FIRST
    expect(posts[0].msg_id).toBe("m1");
    expect(posts[1].is_poll_vote).toBe(true);
    expect(posts[1].text).toBe("Confirm");
    expect(posts[1].msg_id).toBe("m2");
    debouncer.dispose();
  });

  it("ignores unselected poll options", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const vote = {
      type: "poll_option",
      option: { title: "Cancel" },
      poll: { type: "poll", title: "Confirm?", options: [{ title: "Cancel" }] },
      selected: false,
      title: "Cancel",
    } as unknown as Content;
    await route(deps, fakeMessage("m1", vote));
    expect(posts.length).toBe(0);
    debouncer.dispose();
  });

  it("flushes pending text before a shared contact", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    await route(deps, fakeMessage("m1", { type: "text", text: "my number" } as Content));
    const contact = {
      type: "contact",
      name: { first: "Tobi", formatted: "Tobi O" },
      phones: [{ value: "+15550000000" }],
    } as unknown as Content;
    await route(deps, fakeMessage("m2", contact));

    expect(posts.length).toBe(2);
    expect(posts[0].text).toBe("my number");
    expect(posts[1].is_contact).toBe(true);
    expect(posts[1].contact?.first_name).toBe("Tobi");
    expect(posts[1].contact?.phones).toEqual(["+15550000000"]);
    debouncer.dispose();
  });

  it("forwards PDF statement attachments with bounded document fields", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);
    const pdf = {
      type: "attachment",
      name: "NectarFi_Statement.pdf",
      mimeType: "application/pdf",
      read: async () => new Uint8Array([0x25, 0x50, 0x44, 0x46, 0x2d]),
    } as unknown as Content;

    await route(deps, fakeMessage("pdf-1", pdf));

    expect(posts.length).toBe(1);
    expect(posts[0].is_document).toBe(true);
    expect(posts[0].document_name).toBe("NectarFi_Statement.pdf");
    expect(posts[0].document_mime).toBe("application/pdf");
    expect(posts[0].document_b64).toBe("JVBERi0=");
    debouncer.dispose();
  });

  it("drops oversized PDF statement attachments before posting", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);
    const oversized = {
      type: "attachment",
      name: "large.pdf",
      mimeType: "application/pdf",
      read: async () => new Uint8Array(4 * 1024 * 1024 + 1),
    } as unknown as Content;

    await route(deps, fakeMessage("pdf-2", oversized));

    expect(posts.length).toBe(0);
    debouncer.dispose();
  });

  it("posts reactions immediately with the reaction payload shape, flushing text first", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    await route(deps, fakeMessage("m1", { type: "text", text: "lol" } as Content));

    const target = fakeMessage("t1", { type: "text", text: "you spent 40k" } as Content);
    const reaction = { type: "reaction", emoji: "❤️", target } as unknown as Content;
    await route(deps, fakeMessage("m2", reaction));

    expect(posts.length).toBe(2);
    expect(posts[0].text).toBe("lol"); // flush first
    const r = posts[1];
    expect(r.is_reaction).toBe(true);
    expect(r.reaction_emoji).toBe("❤️");
    expect(r.reply_to).toBe("t1");
    expect(r.text).toBe("");
    expect(r.msg_id).toBe("m2");
    debouncer.dispose();
  });

  it("unwraps replies and attaches reply_to + reply_to_text", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const target = fakeMessage("t9", { type: "text", text: "Want me to move 20k to stash?" } as Content);
    const reply = {
      type: "reply",
      content: { type: "text", text: "yes do it" },
      target,
    } as unknown as Content;
    await route(deps, fakeMessage("m5", reply));

    await debouncer.flush(ctx.threadID);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("yes do it");
    expect(posts[0].reply_to).toBe("t9");
    expect(posts[0].reply_to_text).toBe("Want me to move 20k to stash?");
    debouncer.dispose();
  });

  it("truncates long quoted reply text to ~200 chars", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const target = fakeMessage("t9", { type: "text", text: "x".repeat(500) } as Content);
    const reply = {
      type: "reply",
      content: { type: "text", text: "ok" },
      target,
    } as unknown as Content;
    await route(deps, fakeMessage("m6", reply));
    await debouncer.flush(ctx.threadID);
    expect(posts[0].reply_to_text?.length).toBe(200);
    debouncer.dispose();
  });

  it("unwraps edits and attaches edit_of", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const target = fakeMessage("t3", { type: "text", text: "orignal" } as Content);
    const edit = {
      type: "edit",
      content: { type: "text", text: "original" },
      target,
    } as unknown as Content;
    await route(deps, fakeMessage("m7", edit));
    await debouncer.flush(ctx.threadID);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("original");
    expect(posts[0].edit_of).toBe("t3");
    debouncer.dispose();
  });

  it("unwraps effects and processes the inner content", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const fx = {
      type: "effect",
      content: { type: "text", text: "yay" },
      effect: "com.apple.messages.effect.CKConfettiEffect",
    } as unknown as Content;
    await route(deps, fakeMessage("m8", fx));
    await debouncer.flush(ctx.threadID);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("yay");
    debouncer.dispose();
  });

  it("processes each item of a group", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    const group = {
      type: "group",
      items: [
        fakeMessage("g1", { type: "text", text: "first" } as Content),
        fakeMessage("g2", { type: "text", text: "second" } as Content),
      ],
    } as unknown as Content;
    await route(deps, fakeMessage("m10", group));
    await debouncer.flush(ctx.threadID);
    expect(posts.length).toBe(1);
    expect(posts[0].text).toBe("first\nsecond");
    expect(posts[0].msg_id).toBe("g2");
    debouncer.dispose();
  });

  it("does not post typing/read/poll echo content", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    await route(deps, fakeMessage("m1", { type: "typing", state: "start" } as unknown as Content));
    await route(deps, fakeMessage("m2", { type: "read", target: fakeMessage("x", { type: "text", text: "y" } as Content) } as unknown as Content));
    await route(deps, fakeMessage("m3", { type: "poll", title: "Confirm?", options: [{ title: "Confirm" }] } as unknown as Content));
    expect(posts.length).toBe(0);
    expect(debouncer.hasPending(ctx.threadID)).toBe(false);
    debouncer.dispose();
  });

  it("warns (and drops nothing silently) on unrecognized content", async () => {
    const posts: InboundPayload[] = [];
    const debouncer = makeDebouncer(posts);
    const deps = makeRouter(posts, debouncer);

    await route(deps, fakeMessage("m1", { type: "hologram", beams: 2 } as unknown as Content));
    expect(posts.length).toBe(0);
    debouncer.dispose();
  });

  it("debouncer errors are reported via onError, not thrown", async () => {
    const errors: unknown[] = [];
    const d = new InboundDebouncer({
      post: () => {
        throw new Error("backend down");
      },
      debounceMs: 20,
      maxWaitMs: 5000,
      maxBuffer: 5,
      onError: (_k, err) => errors.push(err),
    });
    d.add("chat-1", payload("hi", "m1"));
    await sleep(80);
    expect(errors.length).toBe(1);
    d.dispose();
  });
});

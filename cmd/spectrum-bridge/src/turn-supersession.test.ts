import { describe, expect, it } from "bun:test";
import { TurnSupersession, carriesUserContent } from "./turn-supersession";

describe("TurnSupersession", () => {
  it("tags a content-bearing inbound batch and keeps its own turn current", () => {
    const turns = new TurnSupersession(true);
    const body = { thread_id: "chat-1", text: "Hey Miriam" };

    const turnId = turns.tagInbound(body);

    expect(turnId).toBeString();
    expect(body.turn_id).toBe(turnId as string);
    expect(turns.isStale("chat-1", turnId)).toBe(false);
  });

  it("supersedes an older turn once a newer one is minted", () => {
    const turns = new TurnSupersession(true);
    const first = turns.tagInbound({ thread_id: "chat-1", text: "how much did I spend" })!;
    const second = turns.tagInbound({ thread_id: "chat-1", text: "actually never mind" })!;

    expect(first).not.toBe(second);
    expect(turns.isStale("chat-1", first)).toBe(true);
    expect(turns.isStale("chat-1", second)).toBe(false);
  });

  it("leaves lifecycle signals untagged so they cannot supersede a reply", () => {
    const turns = new TurnSupersession(true);
    // A read receipt: no text, no media.
    const receipt = { thread_id: "chat-1", text: "" };
    expect(turns.tagInbound(receipt)).toBeUndefined();
    expect(receipt.turn_id).toBeUndefined();

    // An unsend behaves the same.
    expect(turns.tagInbound({ thread_id: "chat-1" })).toBeUndefined();

    // Nothing recorded ⇒ nothing is stale.
    expect(turns.isStale("chat-1", "some-turn")).toBe(false);
    expect(turns.trackedThreads).toBe(0);
  });

  it("reuses the turn id already on a payload (retries keep their turn)", () => {
    const turns = new TurnSupersession(true);
    const body = { thread_id: "chat-1", text: "hello" };
    const first = turns.tagInbound(body);
    const again = turns.tagInbound(body);

    expect(again).toBe(first as string);
  });

  it("scopes turns per thread", () => {
    const turns = new TurnSupersession(true);
    const mine = turns.tagInbound({ thread_id: "chat-1", text: "hi" })!;
    turns.tagInbound({ thread_id: "chat-2", text: "hi" });

    expect(turns.isStale("chat-1", mine)).toBe(false);
  });

  it("fails open for unknown threads and untagged replies", () => {
    const turns = new TurnSupersession(true);
    turns.tagInbound({ thread_id: "chat-1", text: "hi" });

    // Unknown thread, unknown turn, and no turn at all all deliver.
    expect(turns.isStale("chat-9", "whatever")).toBe(false);
    expect(turns.isStale("chat-1", undefined)).toBe(false);
    expect(turns.isStale(undefined, "whatever")).toBe(false);
  });

  it("is inert when disabled", () => {
    const turns = new TurnSupersession(false);
    const body = { thread_id: "chat-1", text: "hey" };

    expect(turns.isEnabled).toBe(false);
    expect(turns.tagInbound(body)).toBeUndefined();
    expect(body.turn_id).toBeUndefined();
    expect(turns.isStale("chat-1", "anything")).toBe(false);
  });

  it("tags media that carries no text", () => {
    const turns = new TurnSupersession(true);
    expect(turns.tagInbound({ thread_id: "chat-1", is_voice: true })).toBeString();
    expect(turns.tagInbound({ thread_id: "chat-1", is_image: true })).toBeString();
    expect(turns.tagInbound({ thread_id: "chat-1", is_poll_vote: true })).toBeString();
    // An unsupported/oversized notice carries empty text and no media flags.
    expect(turns.tagInbound({ thread_id: "chat-1", text: "" })).toBeUndefined();
  });

  it("cold-drops a thread's state on clear", () => {
    const turns = new TurnSupersession(true);
    const turnId = turns.tagInbound({ thread_id: "chat-1", text: "hi" })!;
    turns.clear("chat-1");
    expect(turns.isStale("chat-1", turnId)).toBe(false);
    expect(turns.trackedThreads).toBe(0);
  });
});

describe("carriesUserContent", () => {
  it("treats blank text as no content", () => {
    expect(carriesUserContent({ text: "   " })).toBe(false);
    expect(carriesUserContent({})).toBe(false);
    expect(carriesUserContent({ text: "hello" })).toBe(true);
  });
});

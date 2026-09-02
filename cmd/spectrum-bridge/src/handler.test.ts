import { describe, expect, it } from "bun:test";
import type { Space } from "spectrum-ts";
import { MessageHandler, renderInsightCard, extractCardRows, type InsightCard, type OutboundMessage } from "./handler";

describe("renderInsightCard", () => {
  it("renders a stat_grid card from StatItem rows", () => {
    const card: InsightCard = {
      type: "stat_grid",
      title: "Spending Summary",
      data: [
        { label: "Total Spent", value: "$123.45" },
        { label: "Transactions", value: "12" },
      ],
    };
    expect(renderInsightCard(card)).toBe(
      "**Spending Summary**\n• Total Spent: **$123.45**\n• Transactions: **12**",
    );
  });

  it("renders a breakdown card from BreakdownItem rows (numeric amounts)", () => {
    const card: InsightCard = {
      type: "breakdown",
      title: "By Category",
      data: [
        { label: "Groceries", amount: 42.1 },
        { label: "Transport", amount: 15.9 },
      ],
    };
    const out = renderInsightCard(card);
    expect(out).toContain("**By Category**");
    expect(out).toContain("• Groceries: **42.1**");
    expect(out).toContain("• Transport: **15.9**");
  });

  it("renders a tip card message", () => {
    const card: InsightCard = {
      type: "tip",
      title: "Pro tip",
      data: { message: "Move idle cash into Stash to earn yield." },
    };
    expect(renderInsightCard(card)).toBe(
      "**Pro tip**\nMove idle cash into Stash to earn yield.",
    );
  });

  it("renders an empty_state card", () => {
    const card: InsightCard = {
      type: "empty_state",
      title: "No activity yet",
      data: { message: "Once you start using Rail, I'll show you where your money goes." },
    };
    expect(renderInsightCard(card)).toContain("Once you start using Rail");
  });

  it("renders a chart card as y_label + recent points", () => {
    const card: InsightCard = {
      type: "chart",
      title: "Spending Trend",
      data: {
        chart_type: "line",
        y_label: "Amount",
        points: [
          { label: "Jun 1", value: 10 },
          { label: "Jun 2", value: 15 },
          { label: "Jun 3", value: 12 },
        ],
      },
    };
    const out = renderInsightCard(card);
    expect(out).toContain("**Spending Trend**");
    expect(out).toContain("• Amount: **12**");
    expect(out).toContain("• Jun 1: **10**");
  });

  it("renders structured data scalars into rows", () => {
    const card: InsightCard = {
      type: "runway",
      title: "Runway",
      data: { months: 4, days: 12, status: "healthy" },
    };
    const out = renderInsightCard(card);
    expect(out).toContain("• Months: **4**");
    expect(out).toContain("• Status: **healthy**");
  });

  it("returns empty for a card with no renderable content", () => {
    expect(renderInsightCard({ type: "chart", title: "", data: {} })).toBe("");
  });

  it("extracts items nested under a data.items key", () => {
    const rows = extractCardRows({ items: [{ label: "A", value: "1" }] });
    expect(rows).toEqual([{ label: "A", value: "1" }]);
  });
});

// ---------------------------------------------------------------------------
// RULE: no Miriam response goes out without a typing indicator first.
// Every content type the backend can emit must produce at least one typing
// signal BEFORE any user-visible content in the space's send order.
// ---------------------------------------------------------------------------

function recordingSpace(overrides: Record<string, unknown> = {}): { space: Space; events: string[] } {
  const events: string[] = [];
  const space = {
    id: "thread-1",
    async send(content: unknown) {
      let built: unknown = content;
      if (content && typeof (content as { build?: unknown }).build === "function") {
        built = await (content as { build: () => Promise<unknown> }).build();
      }
      const type =
        typeof built === "string"
          ? "text"
          : ((built as { type?: string } | null)?.type ?? "unknown");
      events.push(type);
      return { id: `out-${events.length}`, content: built };
    },
    async startTyping() {
      events.push("typing");
    },
    async stopTyping() {
      events.push("typing-stop");
    },
    async getMessage() {
      return undefined;
    },
    ...overrides,
  } as unknown as Space;
  return { space, events };
}

function baseMsg(extra: Partial<OutboundMessage> = {}): OutboundMessage {
  return { platform: "imessage", user_id: "u1", thread_id: "thread-1", text: "Hey there", ...extra };
}

/** The first user-visible event must be preceded by a typing signal. */
function expectTypingBeforeContent(events: string[]): void {
  const idx = events.findIndex((e) => e !== "typing" && e !== "typing-stop");
  expect(idx).toBeGreaterThan(-1);
  expect(events.slice(0, idx)).toContain("typing");
}

describe("typing-before-send rule", () => {
  it("plain text replies lead with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    const sent = await handler.handleOutbound(space, baseMsg());
    expect(sent).toBe(true);
    expectTypingBeforeContent(events);
  });

  it("markdown replies lead with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(space, baseMsg({ content_type: "markdown" }));
    expectTypingBeforeContent(events);
  });

  it("reply without parent leads with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(space, baseMsg({ content_type: "reply" }));
    expectTypingBeforeContent(events);
  });

  it("threaded reply leads with a typing indicator", async () => {
    const handler = new MessageHandler();
    const parent = { id: "parent-1", content: { type: "text", text: "earlier message" } };
    const { space, events } = recordingSpace({ getMessage: async () => parent });
    await handler.handleOutbound(
      space,
      baseMsg({ content_type: "reply", reply_to: "parent-1" }),
    );
    expectTypingBeforeContent(events);
  });

  it("effect sends lead with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ text: "You did it!", content_type: "effect", effect: "sparkles" }),
    );
    expectTypingBeforeContent(events);
  });

  it("iMessage polls lead with a typing indicator (was previously bare)", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({
        text: "",
        content_type: "poll",
        poll_title: "Send ₦5,000 to Chioma?",
        poll_options: ["Confirm", "Cancel"],
      }),
    );
    expectTypingBeforeContent(events);
  });

  it("poll fallback on platforms without poll support leads with typing", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ platform: "telegram", content_type: "poll", poll_title: "Confirm?" }),
    );
    expectTypingBeforeContent(events);
  });

  it("voice notes lead with a typing indicator (was previously bare)", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ text: "", content_type: "voice", audio_b64: Buffer.from("fake-audio").toString("base64") }),
    );
    expectTypingBeforeContent(events);
  });

  it("card-only appcard sends lead with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ text: "", content_type: "appcard", card_url: "rail://authorize" }),
    );
    expectTypingBeforeContent(events);
  });

  it("appcards preceded by narrative text keep typing before every bubble", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ content_type: "appcard", card_url: "rail://authorize" }),
    );
    expectTypingBeforeContent(events);
  });

  it("richlink sends lead with a typing indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({ content_type: "richlink", card_url: "https://userail.money" }),
    );
    expectTypingBeforeContent(events);
  });

  it("insight cards lead with a typing indicator even without narrative text", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    await handler.handleOutbound(
      space,
      baseMsg({
        text: "",
        content_type: "cards",
        cards: [{ type: "tip", title: "Tip", data: { message: "Automate your savings." } }],
      }),
    );
    expectTypingBeforeContent(events);
  });

  it("deduplicated repeats emit nothing and report nothing sent", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    const msg = baseMsg();
    expect(await handler.handleOutbound(space, msg)).toBe(true);
    const countAfterFirst = events.length;
    const sent = await handler.handleOutbound(space, msg);
    expect(sent).toBe(false);
    expect(events.length).toBe(countAfterFirst);
  });

  it("empty payload reports nothing sent so the caller can end the indicator", async () => {
    const handler = new MessageHandler();
    const { space, events } = recordingSpace();
    const sent = await handler.handleOutbound(space, baseMsg({ text: "" }));
    expect(sent).toBe(false);
    expect(events).toEqual([]);
  });
});

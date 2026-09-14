import { describe, expect, it } from "bun:test";
import {
  MessageHandler,
  renderInsightCard,
  extractCardRows,
  type InsightCard,
  type OutboundMessage,
} from "./handler";

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

describe("MessageHandler reaction content type", () => {
  function base(overrides: Partial<OutboundMessage>): OutboundMessage {
    return {
      platform: "imessage",
      user_id: "u1",
      thread_id: "t1",
      text: "",
      ...overrides,
    };
  }

  it("reacts natively on the stored inbound message", async () => {
    const reacts: Array<[string, string]> = [];
    const handler = new MessageHandler();
    handler.registerInboundMessage({
      id: "in-1",
      direction: "inbound",
      react: (emoji: string) => {
        reacts.push(["in-1", emoji]);
        return Promise.resolve(undefined);
      },
    } as never);

    await handler.handleOutbound(
      { send: async () => {} } as never,
      base({ content_type: "reaction", reply_to: "in-1", reaction_emoji: "❤️" }),
    );

    expect(reacts).toEqual([["in-1", "❤️"]]);
  });

  it("falls back to the last inbound message per thread when no reply_to", async () => {
    const reacts: Array<[string, string]> = [];
    const handler = new MessageHandler();
    handler.registerInboundMessage({
      id: "in-2",
      direction: "inbound",
      space: { id: "t1" },
      react: (emoji: string) => {
        reacts.push(["in-2", emoji]);
        return Promise.resolve(undefined);
      },
    } as never);

    await handler.handleOutbound(
      { send: async () => {} } as never,
      base({ content_type: "reaction", reaction_emoji: "👍" }),
    );

    expect(reacts).toEqual([["in-2", "👍"]]);
  });

  it("no-ops silently when the target message is unavailable", async () => {
    const handler = new MessageHandler();
    let sends = 0;
    const err = await handler.handleOutbound(
      { send: async () => { sends++; } } as never,
      base({ content_type: "reaction", reply_to: "missing", reaction_emoji: "😂" }),
    );
    expect(err).toBeUndefined();
    expect(sends).toBe(0);
  });
});

import { describe, expect, it } from "bun:test";
import { renderInsightCard, extractCardRows, type InsightCard } from "./handler";

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

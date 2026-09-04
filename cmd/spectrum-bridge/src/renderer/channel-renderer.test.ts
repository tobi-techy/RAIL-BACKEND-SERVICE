import { describe, expect, it } from "bun:test";
import { renderMessage, channelHintToRenderStrategy } from "./channel-renderer";

describe("channel renderer", () => {
  it("renders text strategy within platform bubble limits", () => {
    const bubbles = renderMessage({
      strategy: "text",
      text: "Hello.\n\nWorld.\n\nAgain.",
      platform: "whatsapp",
      channel: { maxBubblesPerReply: 3, maxCharsPerBubble: 100 },
    });

    expect(bubbles.length).toBeLessThanOrEqual(3);
    expect(bubbles[0]?.text).toContain("Hello.");
  });

  it("renders plan strategy with step formatting", () => {
    const bubbles = renderMessage({
      strategy: "plan",
      text: "Here is my plan.",
      platform: "imessage",
      planData: {
        plan_id: "plan_123",
        steps: [
          { id: 1, tool: "transfer_funds", status: "pending", check: "balance > $200" },
          { id: 2, tool: "pay_bill", status: "pending" },
        ],
        status: "draft",
      },
      actionChips: [
        { label: "Run plan", action: "execute_plan", confirm: true },
        { label: "Cancel", action: "cancel_plan" },
      ],
    });

    expect(bubbles.length).toBeGreaterThan(0);
    const last = bubbles[bubbles.length - 1];
    expect(last?.planData?.plan_id).toBe("plan_123");
    expect(last?.actionChips?.map((chip) => chip.label)).toEqual(["Run plan", "Cancel"]);
  });

  it("degrades polls on unsupported platforms to YES/NO text", () => {
    const bubbles = renderMessage({
      strategy: "poll",
      text: "Confirm?",
      platform: "whatsapp",
      channel: { supportsPolls: false },
      pollOptions: ["Confirm", "Cancel"],
    });

    expect(bubbles.length).toBe(1);
    expect(bubbles[0]?.contentType).toBe("text");
    expect(bubbles[0]?.text).toContain("Reply YES");
  });

  it("renders trace strategy with summary fields", () => {
    const bubbles = renderMessage({
      strategy: "trace",
      text: "Here's why.",
      platform: "telegram",
      traceData: {
        trace_id: "trace_1",
        content: {
          balance_check: "Spend = $450",
          budget_remaining: "$180",
          anomaly_check: "none",
          confidence: "92%",
          model_version: "v2",
        },
      },
    });

    expect(bubbles.length).toBeGreaterThan(0);
    const last = bubbles[bubbles.length - 1];
    expect(last?.traceData?.trace_id).toBe("trace_1");
    expect(last?.text).toContain("Balance check: Spend = $450");
    expect(last?.text).toContain("Confidence: 92%");
  });

  it("falls back to text for unknown strategy", () => {
    const bubbles = renderMessage({
      strategy: "unknown",
      text: "Fallback.",
      platform: "sms",
    });

    expect(bubbles.length).toBe(1);
    expect(bubbles[0]?.text).toBe("Fallback.");
  });
});

describe("channelHintToRenderStrategy", () => {
  it("passes through valid strategies", () => {
    expect(channelHintToRenderStrategy("plan")).toBe("plan");
    expect(channelHintToRenderStrategy("trace")).toBe("trace");
  });

  it("falls back to text for invalid strategy", () => {
    expect(channelHintToRenderStrategy("not_real")).toBe("text");
  });
});

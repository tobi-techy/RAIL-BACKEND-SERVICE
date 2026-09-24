import { describe, expect, it } from "bun:test";
import {
  buildConfirmationContent,
  editConfirmationCard,
  hasNativeExtension,
  isTerminalConfirmationState,
  previewImageName,
  validConfirmationTransition,
  type ConfirmationCardPayload,
} from "./confirmation-card";
import { ConfirmationCardStore } from "./confirmation-store";

const card: ConfirmationCardPayload = {
  action_id: "11111111-2222-3333-4444-555555555555",
  action: "invest.buy",
  state: "pending",
  confirm_url: "https://api.userail.money/confirm/11111111-2222-3333-4444-555555555555?t=123.sig",
  title: "Buy GOOGL",
  subtitle: "Fractional · market",
};

describe("confirmation state machine (mirrors Go)", () => {
  it("accepts the legal lifecycle edges", () => {
    expect(validConfirmationTransition("pending", "authenticating")).toBe(true);
    expect(validConfirmationTransition("pending", "rejected")).toBe(true);
    expect(validConfirmationTransition("authenticating", "approved")).toBe(true);
    expect(validConfirmationTransition("approved", "completed")).toBe(true);
    expect(validConfirmationTransition("approved", "failed")).toBe(true);
  });

  it("rejects resurrection from terminal states", () => {
    for (const terminal of ["completed", "rejected", "failed", "expired"] as const) {
      expect(isTerminalConfirmationState(terminal)).toBe(true);
      expect(validConfirmationTransition(terminal, "pending")).toBe(false);
    }
    expect(validConfirmationTransition("pending", "approved")).toBe(false);
    expect(validConfirmationTransition("pending", "completed")).toBe(false);
  });
});

describe("preview image per state family", () => {
  it("maps one branded JPEG per family", () => {
    expect(previewImageName("pending")).toBe("confirm-pending.jpg");
    expect(previewImageName("authenticating")).toBe("confirm-working.jpg");
    expect(previewImageName("approved")).toBe("confirm-working.jpg");
    expect(previewImageName("completed")).toBe("confirm-done.jpg");
    expect(previewImageName("rejected")).toBe("confirm-muted.jpg");
    expect(previewImageName("expired")).toBe("confirm-muted.jpg");
    expect(previewImageName("failed")).toBe("confirm-error.jpg");
  });
});

describe("native extension vs stopgap", () => {
  it("requires bundle id + team id for the native Face ID path", () => {
    expect(hasNativeExtension({ appName: "Miriam" })).toBe(false);
    expect(
      hasNativeExtension({ appName: "Miriam", extensionBundleId: "com.railmoney.rail.messages" }),
    ).toBe(false);
    expect(
      hasNativeExtension({
        appName: "Miriam",
        extensionBundleId: "com.railmoney.rail.messages",
        teamId: "TEAM123",
      }),
    ).toBe(true);
  });

  it("builds content on both paths without throwing", () => {
    expect(() =>
      buildConfirmationContent(card, { appName: "Miriam" }),
    ).not.toThrow();
    expect(() =>
      buildConfirmationContent(
        card,
        { appName: "Miriam", extensionBundleId: "com.railmoney.rail.messages", teamId: "TEAM123" },
        new Uint8Array([1, 2, 3]),
      ),
    ).not.toThrow();
  });
});

describe("edit refuses to duplicate the bubble", () => {
  it("throws when the original card handle is missing", async () => {
    const space = { send: async () => ({}) } as never;
    await expect(
      editConfirmationCard(space, undefined, card, { appName: "Miriam" }),
    ).rejects.toThrow("refusing duplicate bubble");
  });
});

describe("ConfirmationCardStore", () => {
  it("records and returns handles by action id", () => {
    const store = new ConfirmationCardStore("/tmp/rail-confirmation-cards-test.json");
    store.record("action-1", "thread-1", "msg-1", "pending");
    expect(store.get("action-1")?.message_id).toBe("msg-1");
    expect(store.get("missing")).toBeUndefined();
    expect(store.count()).toBe(1);
  });
});

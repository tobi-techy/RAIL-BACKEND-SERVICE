import { describe, expect, it } from "bun:test";
import { aliasProviderPlatformKeys } from "./platform-alias";

describe("aliasProviderPlatformKeys", () => {
  it("aliases iMessage so a lowercase webhook platform resolves", () => {
    const runtime = { provider: "iMessage" };
    const platforms = new Map<string, unknown>([["iMessage", runtime]]);
    expect(aliasProviderPlatformKeys(platforms)).toEqual(["imessage"]);
    expect(platforms.get("imessage")).toBe(runtime);
    expect(platforms.get("iMessage")).toBe(runtime);
  });

  it("does not overwrite a key that is already registered", () => {
    const cloud = { provider: "iMessage" };
    const other = { provider: "other" };
    const platforms = new Map<string, unknown>([
      ["iMessage", cloud],
      ["imessage", other],
    ]);
    expect(aliasProviderPlatformKeys(platforms)).toEqual([]);
    expect(platforms.get("imessage")).toBe(other);
  });
});

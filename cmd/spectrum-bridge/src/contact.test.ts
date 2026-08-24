import { describe, expect, it } from "bun:test";
import { contactFromSpectrum, isVCardMime } from "./contact";

describe("isVCardMime", () => {
  it("recognizes vcard mimes and filenames", () => {
    expect(isVCardMime("text/vcard")).toBe(true);
    expect(isVCardMime("text/x-vcard; charset=utf-8")).toBe(true);
    expect(isVCardMime("image/jpeg")).toBe(false);
    expect(isVCardMime("application/octet-stream", "Ada.vcf")).toBe(true);
  });
});

describe("contactFromSpectrum", () => {
  it("flattens spectrum contact content", () => {
    const got = contactFromSpectrum({
      name: { first: "Ada", last: "Okafor", formatted: "Ada Okafor" },
      phones: [{ value: "+2348012345678" }, "080111"],
      emails: [{ value: "ada@example.com" }],
      addresses: [{ country: "Nigeria" }],
    });
    expect(got.first_name).toBe("Ada");
    expect(got.phones).toEqual(["+2348012345678", "080111"]);
    expect(got.emails).toEqual(["ada@example.com"]);
    expect(got.country).toBe("Nigeria");
  });
});

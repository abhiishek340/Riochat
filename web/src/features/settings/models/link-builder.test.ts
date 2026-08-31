import { describe, expect, it } from "vitest";
import { buildImportLink, emptyEntry } from "./link-builder";
import { parseImportFragment } from "./link-parser";

const entry = (id: number, over: Partial<ReturnType<typeof emptyEntry>> = {}) => ({ ...emptyEntry(id), ...over });

describe("buildImportLink", () => {
  it("builds a version 1 link for one entry, trimming the origin's slash", () => {
    const link = buildImportLink("https://chat.test/", [entry(1, { url: "https://api.example.com/v1", key: "sk-1", protocol: "anthropic_messages", name: "My key" })]);
    expect(link).toBe("https://chat.test/import#v=1&url=https%3A%2F%2Fapi.example.com%2Fv1&key=sk-1&protocol=anthropic_messages&name=My%20key");
  });
  it("writes placeholders for missing required values", () => {
    expect(buildImportLink("https://chat.test", [entry(1)])).toBe("https://chat.test/import#v=1&url={address}&key={key}");
  });
  it("uses version 2 groups for several entries, with a placeholder for a missing key", () => {
    const link = buildImportLink("https://chat.test", [entry(1, { url: "https://a.test", key: "k1" }), entry(2, { url: "https://b.test" })]);
    expect(link).toBe("https://chat.test/import#v=2&url=https%3A%2F%2Fa.test&key=k1&url=https%3A%2F%2Fb.test&key={key}");
  });
  it("is empty until the origin is known", () => {
    expect(buildImportLink("", [entry(1)])).toBe("");
  });
  it("produces links the strict parser accepts", () => {
    const link = buildImportLink("https://chat.test", [entry(1, { url: "https://a.test/v1", key: "k+1&2", name: "x y" })]);
    const parsed = parseImportFragment(link.slice(link.indexOf("#")));
    expect(parsed.ok && parsed.entries[0]).toMatchObject({ key: "k+1&2", name: "x y" });
  });
});

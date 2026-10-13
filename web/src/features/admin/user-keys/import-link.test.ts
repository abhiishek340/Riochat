import { describe, expect, it } from "vitest";
import { buildImportLink } from "./import-link";

describe("buildImportLink", () => {
  it("writes placeholders for missing required values", () => {
    expect(buildImportLink("https://chat.example/", [{ url: "", key: "" }])).toBe("https://chat.example/import#v=1&url={address}&key={key}");
  });
  it("encodes values and omits blank optional ones", () => {
    const link = buildImportLink("https://chat.example", [{ url: "https://api.x.test/v1?a=b", key: "sk-1 2", protocol: "", name: "My Box" }]);
    expect(link).toBe("https://chat.example/import#v=1&url=https%3A%2F%2Fapi.x.test%2Fv1%3Fa%3Db&key=sk-1%202&name=My%20Box");
  });
  it("uses version 2 for several entries", () => {
    const link = buildImportLink("https://c.test", [
      { url: "https://a.test", key: "k1" },
      { url: "https://b.test", key: "k2", protocol: "anthropic_messages" },
    ]);
    expect(link).toBe("https://c.test/import#v=2&url=https%3A%2F%2Fa.test&key=k1&url=https%3A%2F%2Fb.test&key=k2&protocol=anthropic_messages");
  });
});

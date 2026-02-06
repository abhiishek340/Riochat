import { describe, expect, it } from "vitest";
import { normalizeServerUrl } from "./base-url";

describe("normalizeServerUrl", () => {
  it("canonicalises scheme, host, port and trailing slashes", () => {
    expect(normalizeServerUrl(" HTTPS://Example.COM:8443/api/ ")).toBe("https://example.com:8443/api");
    expect(normalizeServerUrl("http://localhost:8080//")).toBe("http://localhost:8080");
    expect(normalizeServerUrl("http://[::1]:3000")).toBe("http://[::1]:3000");
  });
  it("rejects everything that is not a plain http(s) origin", () => {
    for (const bad of ["ftp://x", "http://u:p@h", "http://h?x=1", "http://h#f", "http://h:0", "http://h:70000", "example.com", "", "http://-bad.com"]) {
      expect(normalizeServerUrl(bad)).toBe("");
    }
  });
});

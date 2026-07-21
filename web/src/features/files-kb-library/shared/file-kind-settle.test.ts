import { describe, expect, it } from "vitest";
import { settleKind } from "./file-kind";

describe("settleKind", () => {
  it("demotes textual kinds whose content is binary garbage", () => {
    const garbage = "\u0000\u0001\u0002�".repeat(50);
    expect(settleKind("markdown", garbage)).toBe("unsupported");
    expect(settleKind("code", garbage)).toBe("unsupported");
  });
  it("keeps readable text in its viewer", () => {
    expect(settleKind("markdown", "# hi")).toBe("markdown");
    expect(settleKind("spreadsheet", "a,b\n1,2")).toBe("spreadsheet");
  });
  it("shows an unknown type as text when the content is readable", () => {
    expect(settleKind("unsupported", "plain words in a .xyz file")).toBe("text");
    expect(settleKind("unsupported", "")).toBe("unsupported");
    expect(settleKind("unsupported", "\u0000\u0000\u0000\u0000")).toBe("unsupported");
  });
  it("leaves binary viewers alone", () => {
    expect(settleKind("image", "")).toBe("image");
    expect(settleKind("pdf", "")).toBe("pdf");
  });
});

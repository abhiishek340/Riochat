import { describe, expect, it } from "vitest";
import { detectTrigger, removeTrigger, replaceTrigger } from "./mention";

const at = (text: string, caret = text.length) => detectTrigger(text, caret);

describe("detectTrigger", () => {
  it("opens the resource menu after whitespace and at the start", () => {
    expect(at("see @mo")).toEqual({ trigger: "@", start: 4, query: "mo" });
    expect(at("@")).toEqual({ trigger: "@", start: 0, query: "" });
    expect(at("/Sum")).toEqual({ trigger: "/", start: 0, query: "sum" });
  });

  it("does not open after an identifier character", () => {
    expect(at("a@mo")).toBeNull();
    expect(at("user.name@host")).toBeNull();
  });

  it("does not open a context menu inside a url or path", () => {
    expect(at("http://x/ab")).toBeNull();
    expect(at("src/lib/a")).toBeNull();
    expect(at("a /b")?.query).toBe("b");
  });

  it("opens after CJK characters and punctuation", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(at("\u4f60\u597d@\u6a21")?.query).toBe("\u6a21");
    expect(at("\u8bf7\u4f7f\u7528/\u6458\u8981")?.trigger).toBe("/");
    expect(at("(@mo")?.start).toBe(1);
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(at("\uff0c/x")?.start).toBe(1);
  });

  it("needs a collapsed selection and no whitespace in the query", () => {
    expect(detectTrigger("see @mo", 5, 7)).toBeNull();
    expect(at("see @mo re")).toBeNull();
    expect(at("see @mo re", 7)).toEqual({ trigger: "@", start: 4, query: "mo" });
  });

  it("edits the text around the trigger", () => {
    const trig = at("hello @mo there", 9)!;
    expect(removeTrigger("hello @mo there", trig, 9)).toEqual({ text: "hello  there", caret: 6 });
    expect(replaceTrigger("hello /su", detectTrigger("hello /su", 9)!, 9, "SUMMARY")).toEqual({ text: "hello SUMMARY", caret: 13 });
  });
});

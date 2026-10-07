import { describe, expect, it } from "vitest";
import {
  chatModelNames,
  DEFAULT_ALLOWED,
  DEFAULT_DENIED,
  docText,
  evaluatePath,
  evaluatePaths,
  normalizeFollow,
  normalizePromptValue,
  normalizeTemplateName,
  parseDoc,
  parseSampleProps,
  validateComponent,
  validateSchemaText,
  validateTemplate,
} from "./rules";

describe("template names and limits", () => {
  it("strips slashes and whitespace around the name", () => {
    expect(normalizeTemplateName("  //hello ")).toBe("hello");
    expect(normalizeTemplateName("/ /x")).toBe("/x");
    expect(normalizeTemplateName("///")).toBe("");
  });
  it("needs a name and content, and honours the limits", () => {
    expect(validateTemplate({ name: "/", description: "", content: "x" })?.key).toBe("adminConversation.problems.nameRequired");
    expect(validateTemplate({ name: "a", description: "", content: "  " })?.key).toBe("adminConversation.problems.contentRequired");
    expect(validateTemplate({ name: "a".repeat(65), description: "", content: "x" })?.key).toBe("adminConversation.problems.nameTooLong");
    expect(validateTemplate({ name: "a", description: "d".repeat(257), content: "x" })?.key).toBe("adminConversation.problems.descriptionTooLong");
    expect(validateTemplate({ name: "a", description: "", content: "c".repeat(10_001) })?.key).toBe("adminConversation.problems.contentTooLong");
    expect(validateTemplate({ name: "a", description: "", content: "c".repeat(10_000) })).toBeNull();
  });
  it("counts characters, not UTF-16 units", () => {
    expect(validateTemplate({ name: "😀".repeat(64), description: "", content: "x" })).toBeNull();
  });
});

describe("ui components", () => {
  const ok = { name: "price-card", description: "A card", propsSummary: "title, price", propsSchema: "", rendererSource: "<div></div>" };
  it("accepts kebab-case names only", () => {
    expect(validateComponent(ok)).toBeNull();
    for (const name of ["Bad Name", "a--b", "-a", "a-", "1abc", ""]) expect(validateComponent({ ...ok, name })?.key, name).toBe("adminConversation.problems.componentName");
  });
  it("accepts an empty schema or an object, never an array or scalar", () => {
    expect(validateSchemaText("")).toBeNull();
    expect(validateSchemaText('{"type":"object"}')).toBeNull();
    expect(validateSchemaText("[1]")).not.toBeNull();
    expect(validateSchemaText("3")).not.toBeNull();
    expect(validateSchemaText("nope")).not.toBeNull();
  });
  it("requires the renderer source", () => {
    expect(validateComponent({ ...ok, rendererSource: " " })?.key).toBe("adminConversation.problems.sourceRequired");
  });
  it("disables the preview for bad sample props", () => {
    expect(parseSampleProps("")).toEqual({});
    expect(parseSampleProps('{"a":1}')).toEqual({ a: 1 });
    expect(parseSampleProps("[1]")).toBeNull();
    expect(parseSampleProps("{")).toBeNull();
  });
});

describe("prompt values", () => {
  it("empties blank prompts and keeps real text verbatim", () => {
    expect(normalizePromptValue(" \n ")).toBe("");
    expect(normalizePromptValue("  keep me ")).toBe("  keep me ");
    expect(normalizeFollow("")).toBe("follow");
    expect(normalizeFollow("gpt")).toBe("gpt");
  });
});

describe("model option policy", () => {
  const allowed = DEFAULT_ALLOWED;
  const denied = DEFAULT_DENIED;
  it("always drops the system paths", () => {
    expect(evaluatePath("messages", "default", "denylist", {}, {})).toMatchObject({ pass: false, verdict: "systemDenied" });
    expect(evaluatePath("headers.x", "default", "denylist", {}, {})).toMatchObject({ pass: false, verdict: "systemDenied" });
  });
  it("allowlist passes a path that equals or descends from an allowed rule", () => {
    expect(evaluatePath("temperature", "openai_responses", "allowlist", allowed, denied)).toMatchObject({ pass: true, verdict: "allowlist" });
    expect(evaluatePath("response_format.type", "default", "allowlist", allowed, denied).pass).toBe(true);
    expect(evaluatePath("reasoning.effort", "openai_responses", "allowlist", allowed, denied).pass).toBe(true);
    expect(evaluatePath("reasoning.effort", "openai_chat_completions", "allowlist", allowed, denied)).toMatchObject({ pass: false, verdict: "notAllowed" });
  });
  it("allowlist treats an allowed ancestor as covering its children", () => {
    expect(evaluatePath("thinking.budget_tokens", "anthropic_messages", "allowlist", { default: ["thinking"] }, denied).pass).toBe(true);
  });
  it("denylist keeps everything not denied", () => {
    expect(evaluatePath("custom_flag", "default", "denylist", allowed, denied)).toMatchObject({ pass: true, verdict: "notDenied" });
    expect(evaluatePath("stream", "default", "denylist", allowed, denied)).toMatchObject({ pass: false, verdict: "denylist" });
    expect(evaluatePath("foo", "default", "denylist", allowed, { default: ["foo"] })).toMatchObject({ pass: false, verdict: "denylist", rule: "foo" });
  });
  it("disabled drops everything", () => {
    expect(evaluatePath("temperature", "default", "disabled", allowed, denied).verdict).toBe("disabled");
  });
  it("evaluates a list typed one per line or comma separated", () => {
    expect(evaluatePaths("temperature, top_k\nmodel", "default", "allowlist", allowed, denied).map((r) => r.pass)).toEqual([true, false, false]);
  });
});

describe("policy documents", () => {
  it("parses protocol keys case-insensitively", () => {
    expect(parseDoc('{"Default":[" a "]}')).toEqual({ ok: true, doc: { default: ["a"] } });
  });
  it("reports syntax, shape and protocol problems", () => {
    expect(parseDoc("{")).toEqual({ ok: false, reason: "syntax" });
    expect(parseDoc("[]")).toEqual({ ok: false, reason: "shape" });
    expect(parseDoc('{"default":"a"}')).toEqual({ ok: false, reason: "shape" });
    expect(parseDoc('{"mystery":["a"]}')).toEqual({ ok: false, reason: "protocol" });
  });
  it("shows the defaults for a blank value", () => {
    expect(JSON.parse(docText("", DEFAULT_DENIED))).toEqual(DEFAULT_DENIED);
    expect(docText('{"default":[]}', DEFAULT_DENIED)).toBe('{"default":[]}');
  });
});

describe("chatModelNames", () => {
  it("lists distinct chat models, sorted", () => {
    const names = chatModelNames([
      { platformModelName: "b", kindsJSON: '["chat"]' },
      { platformModelName: "a" },
      { platformModelName: "img", kindsJSON: '["image_gen"]' },
      { platformModelName: "a", kindsJSON: '["chat","image_gen"]' },
    ]);
    expect(names).toEqual(["a", "b"]);
  });
});

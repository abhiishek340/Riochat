import { describe, expect, it } from "vitest";
import { attachmentProblem, stringArguments } from "./mcp-schema";

const schema = JSON.stringify({
  type: "object",
  required: ["image", "mode"],
  properties: {
    image: { type: "string" },
    prompt: { anyOf: [{ type: "null" }, { type: "string" }] },
    count: { type: "integer" },
    mode: { $ref: "#/$defs/Mode" },
    tags: { type: "array", items: { type: "string" } },
    note: { type: ["string", "null"] },
  },
  $defs: { Mode: { type: "string", enum: ["a", "b"] } },
});

describe("stringArguments", () => {
  it("lists string-accepting top-level properties, sorted, with required flags", () => {
    expect(stringArguments(schema)).toEqual([
      { name: "image", required: true },
      { name: "mode", required: true },
      { name: "note", required: false },
      { name: "prompt", required: false },
    ]);
  });
  it("copes with bad input and reference loops", () => {
    expect(stringArguments("nope")).toEqual([]);
    expect(stringArguments("[]")).toEqual([]);
    const loop = JSON.stringify({ properties: { a: { $ref: "#/properties/a" } } });
    expect(stringArguments(loop)).toEqual([]);
  });
});

describe("attachmentProblem", () => {
  it("accepts mode none without looking at the schema", () => {
    expect(attachmentProblem("garbage", { mode: "none", imageArgument: "", promptEnabled: false, promptArgument: "" })).toBeNull();
  });
  it("requires the image argument to exist", () => {
    expect(attachmentProblem(schema, { mode: "image", imageArgument: "count", promptEnabled: false, promptArgument: "" })).toBe("imageArgument");
  });
  it("requires every required argument to be mapped", () => {
    expect(attachmentProblem(schema, { mode: "image", imageArgument: "image", promptEnabled: false, promptArgument: "" })).toBe("requiredUncovered");
    expect(attachmentProblem(schema, { mode: "image", imageArgument: "image", promptEnabled: true, promptArgument: "mode" })).toBeNull();
  });
  it("keeps the prompt argument distinct from the image argument", () => {
    expect(attachmentProblem(schema, { mode: "image", imageArgument: "image", promptEnabled: true, promptArgument: "image" })).toBe("promptSame");
    expect(attachmentProblem(schema, { mode: "image", imageArgument: "image", promptEnabled: true, promptArgument: "missing" })).toBe("promptArgument");
  });
});

import { describe, expect, it } from "vitest";
import { formatNumber, parseTemplate, resolveTemplate, TemplateError } from "./template";

const resolve = (src: string, scope = {}) => resolveTemplate(parseTemplate(src), scope, { steps: 10_000 });
const state = { price: 1234.5678, name: "A" };

describe("templates", () => {
  it("keeps the type of a single hole", () => {
    expect(resolve("{price}", state).value).toBe(1234.5678);
    expect(resolve("{price > 1}", state).value).toBe(true);
  });

  it("formats numbers in mixed text", () => {
    expect(resolve("Total: {price}", state).value).toBe("Total: 1,234.57");
    expect(formatNumber(Infinity)).toBe("—");
    expect(resolve("{[1,2]} and {null}!", {}).value).toBe("[1,2] and !");
    expect(resolve("{true}", {}).value).toBe(true);
    expect(resolve("is {true}", {}).value).toBe("is true");
  });

  it("collapses doubled braces only when the string has a hole", () => {
    expect(resolve("{{x}} = {name}", state).value).toBe("{x} = A");
    expect(resolve("{{literal}}").value).toBe("{{literal}}");
    expect(parseTemplate("{{literal}}").isStatic).toBe(true);
  });

  it("rejects lone and unclosed braces, even without holes", () => {
    expect(() => parseTemplate("a } b")).toThrow(TemplateError);
    expect(() => parseTemplate("a { b")).toThrow(TemplateError);
    expect(() => parseTemplate("{a {b}}")).toThrow(TemplateError);
    expect(() => parseTemplate("{1 +}")).toThrow(TemplateError);
  });

  it("protects braces inside quoted strings of a hole", () => {
    expect(resolve("{'}' + 'x'}").value).toBe("}x");
  });

  it("prints a dash for a failing hole in mixed text and drops a failing single hole", () => {
    const r = resolve("{missing} units");
    expect(r.value).toBe("— units");
    expect(r.issues[0]).toMatch(/missing/);
    expect(resolve("{missing}").value).toBeUndefined();
  });

  it("reports the variables it reads", () => {
    expect(parseTemplate("{a + b} and {len(c)}").vars.sort()).toEqual(["a", "b", "c"]);
    expect(parseTemplate("plain").vars).toEqual([]);
  });
});

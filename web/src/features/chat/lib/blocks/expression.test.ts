import { describe, expect, it } from "vitest";
import { compile, compileNumeric, evalNumeric, evaluate, ExprError } from "./expression";

const ev = (s: string, scope = {}) => evaluate(s, scope);

describe("expressions", () => {
  it("follows operator precedence", () => {
    expect(ev("2 + 3 * 4")).toBe(14);
    expect(ev("-2^2")).toBe(-4);
    expect(ev("2^3^2")).toBe(512);
    expect(ev("2^-1")).toBe(0.5);
    expect(ev("10 % 4 + 1")).toBe(3);
    expect(ev("(1 + 2) * 3")).toBe(9);
  });

  it("handles logic and truthiness", () => {
    expect(ev("1 < 2 && 'a' == 'a'")).toBe(true);
    expect(ev("null || 5")).toBe(5);
    expect(ev("0 && 1")).toBe(0);
    expect(ev("!0")).toBe(true);
    expect(ev("1 === 1 && 2 !== 3")).toBe(true);
    expect(ev("abs(-3) > 2 ? 'big' : 'small'")).toBe("big");
  });

  it("concatenates when either side is text and compares strings", () => {
    expect(ev("'a' + 1")).toBe("a1");
    expect(ev("null + 'x'")).toBe("x");
    expect(ev("[1] + 'x'")).toBe("[1]x");
    expect(ev("'a' < 'b'")).toBe(true);
    expect(() => ev("'a' - 1")).toThrow(ExprError);
  });

  it("calls functions", () => {
    expect(ev("round(2.567, 1)")).toBe(2.6);
    expect(ev("fixed(3.14159, 2)")).toBe("3.14");
    expect(ev("fixed(1/0)")).toBe("—");
    expect(ev("sum(range(1, 4))")).toBe(10);
    expect(ev("range(1, 3, 0.5)")).toEqual([1, 1.5, 2, 2.5, 3]);
    expect(ev("range(3, 1)")).toEqual([3, 2, 1]);
    expect(ev("range(0, 0.3, 0.1)")).toEqual([0, 0.1, 0.2, 0.3]);
    expect(ev("len('abc')")).toBe(3);
    expect(ev("len([1,2])")).toBe(2);
    expect(ev("contains('hello','ell')")).toBe(true);
    expect(ev("contains([1,[2]],[2])")).toBe(true);
    expect(ev("min(3, 1, 2)")).toBe(1);
    expect(ev("max([4, 9])")).toBe(9);
    expect(ev("clamp(15, 0, 10)")).toBe(10);
    expect(ev("avg([2,4])")).toBe(3);
    expect(ev("pow(2, 10)")).toBe(1024);
    expect(ev("pi > 3 && e < 3")).toBe(true);
  });

  it("allows division by zero and reads lists and objects", () => {
    expect(ev("1/0")).toBe(Infinity);
    expect(ev("[1,2,3][1]")).toBe(2);
    expect(ev("rows.length", { rows: [1, 2] })).toBe(2);
    expect(ev("o.a + o['b']", { o: { a: 1, b: 2 } })).toBe(3);
    expect(ev("'abc'.length")).toBe(3);
  });

  it("rejects bad input with errors", () => {
    expect(() => compile("foo(1)")).toThrow(/unknown function foo/);
    expect(() => ev("range(1, 5000)")).toThrow(ExprError);
    expect(() => ev("range(1, 2, 0)")).toThrow(ExprError);
    expect(() => ev("range(3, 1, 1)")).toThrow(ExprError);
    expect(() => compile("1 + ".repeat(200) + "1")).toThrow(/too long/);
    expect(() => ev("o.__proto__", { o: {} })).toThrow(ExprError);
    expect(() => ev("o['constructor']", { o: {} })).toThrow(ExprError);
    expect(() => ev("missing + 1")).toThrow(/unknown variable missing/);
    expect(() => ev("[1][3]")).toThrow(ExprError);
    expect(() => ev("5.foo")).toThrow(ExprError);
    expect(() => ev("'unterminated")).toThrow(/unterminated/);
    expect(() => ev("1 +")).toThrow(ExprError);
    expect(() => ev("sum()")).toThrow(ExprError);
    expect(() => ev("min([])")).toThrow(ExprError);
    expect(() => ev("avg([])")).toThrow(ExprError);
  });

  it("spends a step budget", () => {
    expect(() => evaluate("sum(range(1, 1000)) + sum(range(1, 1000))", {}, 500)).toThrow(/too expensive/);
    expect(evaluate("1 + 1", {}, 3)).toBe(2);
    expect(() => evaluate("1 + 1 + 1", {}, 3)).toThrow(/too expensive/);
  });

  it("does not let scope names shadow the safety rules", () => {
    expect(() => ev("__proto__.x", { x: 1 })).toThrow(ExprError);
    expect(ev("pi", { pi: 3 })).toBe(3);
  });

  it("reports the variables a formula reads", () => {
    expect(compile("a * (b + pi) + len(c)").vars.sort()).toEqual(["a", "b", "c"]);
  });
});

describe("numeric formulas", () => {
  it("rejects formulas that can never be numbers", () => {
    expect(() => compileNumeric("x > 1")).toThrow(ExprError);
    expect(() => compileNumeric("'a'")).toThrow(ExprError);
    expect(() => compileNumeric("[1,2]")).toThrow(ExprError);
    expect(() => compileNumeric("!x")).toThrow(ExprError);
    expect(() => compileNumeric("x && y")).toThrow(ExprError);
  });
  it("accepts conditionals with a numeric branch and evaluates with named numbers", () => {
    const c = compileNumeric("x > 1 ? 2 : 3");
    expect(evalNumeric(c, { x: 2 })).toBe(2);
    expect(evalNumeric(c, { x: 0 })).toBe(3);
    expect(() => evalNumeric(compileNumeric("x ? 'a' : 1 + x"), { x: 1 })).toThrow(ExprError);
  });
});

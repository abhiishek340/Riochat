import { describe, expect, it } from "vitest";
import { parseLatex, texToMathML, toMathML } from "./math";

const ml = (tex: string, display = false) => toMathML(parseLatex(tex), display);

describe("latex to mathml", () => {
  it("handles identifiers, numbers and operators", () => {
    expect(ml("x+1")).toBe('<mrow><mi>x</mi><mo>+</mo><mn>1</mn></mrow>');
    expect(ml("a-b")).toContain("<mo>−</mo>");
    expect(ml("3.14")).toBe("<mn>3.14</mn>");
  });

  it("handles fractions, roots and scripts", () => {
    expect(ml("\\frac{a}{b}")).toBe("<mfrac><mi>a</mi><mi>b</mi></mfrac>");
    expect(ml("\\sqrt{x}")).toBe("<msqrt><mi>x</mi></msqrt>");
    expect(ml("\\sqrt[3]{x}")).toBe("<mroot><mi>x</mi><mn>3</mn></mroot>");
    expect(ml("x^2")).toBe("<msup><mi>x</mi><mn>2</mn></msup>");
    expect(ml("x_i^2")).toBe("<msubsup><mi>x</mi><mi>i</mi><mn>2</mn></msubsup>");
    expect(ml("e^{-x^2}")).toContain("<msup><mi>x</mi><mn>2</mn></msup>");
  });

  it("puts limits above and below big operators in display mode", () => {
    expect(ml("\\sum_{i=1}^{n} i", true)).toContain("<munderover>");
    expect(ml("\\sum_{i=1}^{n} i", false)).toContain("<msubsup>");
    expect(ml("\\lim_{x\\to 0} f", true)).toContain("<munder>");
  });

  it("maps greek letters and symbols", () => {
    expect(ml("\\alpha\\leq\\infty")).toContain("<mi>α</mi>");
    expect(ml("\\alpha\\leq\\infty")).toContain("<mo>≤</mo>");
    expect(ml("a\\times b")).toContain("×");
  });

  it("builds delimiters and matrices", () => {
    expect(ml("\\left( x \\right)")).toContain('<mo fence="true" stretchy="true">(</mo>');
    const m = ml("\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}");
    expect(m).toContain("<mtable");
    expect((m.match(/<mtr>/g) ?? []).length).toBe(2);
    expect((m.match(/<mtd/g) ?? []).length).toBe(4);
    expect(m).toContain(">(</mo>");
  });

  it("handles text, fonts and accents", () => {
    expect(ml("\\text{if } x")).toContain("<mtext>if&#160;</mtext>");
    expect(ml("\\mathbb{R}")).toContain("ℝ");
    expect(ml("\\mathbf{v}")).toContain('mathvariant="bold"');
    expect(ml("\\vec{v}")).toContain('<mover accent="true">');
    expect(ml("\\overline{AB}")).toContain("<mover");
  });

  it("marks unknown commands instead of failing", () => {
    expect(texToMathML("\\frobnicate x", false).error).toBe(true);
    expect(texToMathML("x + y", false).error).toBe(false);
  });

  it("escapes markup in the source", () => {
    const out = texToMathML("<script>alert(1)</script>", false).html;
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;");
  });

  it("wraps the result with display mode and the tex annotation", () => {
    const out = texToMathML("a^2", true).html;
    expect(out.startsWith('<math display="block"')).toBe(true);
    expect(out).toContain('<annotation encoding="application/x-tex">a^2</annotation>');
  });

  it("survives unbalanced input", () => {
    expect(() => texToMathML("\\frac{a}{", false)).not.toThrow();
    expect(() => texToMathML("}}{{", false)).not.toThrow();
    expect(() => texToMathML("\\begin{pmatrix} a &", false)).not.toThrow();
  });
});

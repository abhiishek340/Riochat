import { describe, expect, it } from "vitest";
import { clampHeight, widgetDocument, WIDGET_HEIGHT_MESSAGE } from "./widget-doc";

const theme = { variables: { "--primary": "red" }, colorScheme: "light" as const };

describe("widgetDocument", () => {
  it("keeps the CSP head first and adds the height reporter once", () => {
    const doc = widgetDocument("<h1>Hi</h1>", theme);
    expect(doc.indexOf("Content-Security-Policy")).toBeLessThan(doc.indexOf("<h1>Hi</h1>"));
    expect(doc.split(WIDGET_HEIGHT_MESSAGE).length - 1).toBe(1);
    expect(doc.indexOf(WIDGET_HEIGHT_MESSAGE)).toBeLessThan(doc.lastIndexOf("</body>"));
  });
  it("works for full documents too", () => {
    const doc = widgetDocument("<html><head></head><body>x</body></html>", theme);
    expect(doc).toContain(WIDGET_HEIGHT_MESSAGE);
    expect(doc.indexOf(WIDGET_HEIGHT_MESSAGE)).toBeLessThan(doc.indexOf("</body>"));
  });
});

describe("clampHeight", () => {
  it("clamps and rejects junk", () => {
    expect(clampHeight(10)).toBe(60);
    expect(clampHeight(5000)).toBe(900);
    expect(clampHeight(200.2)).toBe(201);
    expect(clampHeight("x")).toBeNull();
    expect(clampHeight(NaN)).toBeNull();
  });
});

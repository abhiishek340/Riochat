import { describe, expect, it } from "vitest";
import { capturable, fitScale, screenshotFileName, TooLargeError } from "./screenshot";

describe("fitScale", () => {
  it("keeps the wanted scale when it fits", () => {
    expect(fitScale(800, 2000)).toBe(2);
  });
  it("reduces the scale to fit the area, down to 1", () => {
    const s = fitScale(1000, 8000, 2);
    expect(s).toBeLessThan(2);
    expect(s).toBeGreaterThanOrEqual(1);
    expect(1000 * s * 8000 * s).toBeLessThanOrEqual(24 * 1024 * 1024 + 1);
  });
  it("refuses content that would need less than 1x", () => {
    expect(() => fitScale(1000, 40000)).toThrow(TooLargeError);
    expect(() => fitScale(0, 10)).toThrow(TooLargeError);
  });
});

describe("screenshotFileName", () => {
  it("sanitises the title and stamps the time", () => {
    const d = new Date(2026, 0, 5, 9, 8, 7);
    expect(screenshotFileName('a/b: c*d?  "e"', d)).toBe("conversation-a-b-c-d-e-20260105-090807.png");
    expect(screenshotFileName("", d)).toBe("conversation-conversation-20260105-090807.png");
  });
});

describe("capturable", () => {
  it("takes the newest settled persisted messages", () => {
    const path = [
      { id: "a", persisted: true, status: "success" },
      { id: "b", persisted: false, status: "success" },
      { id: "c", persisted: true, status: "pending" },
      { id: "d", persisted: true, status: "success" },
    ];
    expect(capturable(path)).toEqual(["a", "d"]);
    const many = Array.from({ length: 130 }, (_, i) => ({ id: `m${i}`, persisted: true, status: "success" }));
    expect(capturable(many)).toHaveLength(100);
    expect(capturable(many)[0]).toBe("m30");
  });
});

import { describe, expect, it } from "vitest";
import { dayPeriod, formatBytes, formatLatency, hourInZone, safeFileName } from "./format";
import { forkTitle, isPlaceholderTitle, optimisticTitle, plainPreview } from "./text";
import { isLocalId, isRunId, localAssistantId, newRunId } from "./ids";

describe("optimisticTitle", () => {
  it("collapses whitespace, strips quotes and cuts to 16 code points", () => {
    expect(optimisticTitle('  "Hello   world, this is a very long message"  ')).toBe("Hello world, thi");
  });
  it("returns an empty string for blank input", () => {
    expect(optimisticTitle("   \n ")).toBe("");
    expect(optimisticTitle('""')).toBe("");
  });
  it("counts code points, not UTF-16 units", () => {
    expect(optimisticTitle("😀".repeat(20))).toBe("😀".repeat(16));
  });
});

describe("placeholder titles", () => {
  it("recognises the placeholder names and empties", () => {
    expect(isPlaceholderTitle("New Chat")).toBe(true);
    expect(isPlaceholderTitle("Untitled chat")).toBe(true);
    expect(isPlaceholderTitle("  untitled CHAT ")).toBe(true);
    expect(isPlaceholderTitle("")).toBe(true);
    expect(isPlaceholderTitle("Trip plan")).toBe(false);
  });
});

describe("forkTitle", () => {
  it("keeps the whole title within 255 characters and ends with the suffix", () => {
    const out = forkTitle("a".repeat(300), " (Fork)");
    expect(Array.from(out)).toHaveLength(255);
    expect(out.endsWith(" (Fork)")).toBe(true);
  });
  it("leaves short titles intact", () => {
    expect(forkTitle("Plan", " (Fork)")).toBe("Plan (Fork)");
  });
});

describe("formatLatency", () => {
  it("uses one decimal below ten seconds with a 0.1 floor", () => {
    expect(formatLatency(950)).toBe("0.9s");
    expect(formatLatency(40)).toBe("0.1s");
    expect(formatLatency(12400)).toBe("12s");
    expect(formatLatency(0)).toBe("");
  });
});

describe("greeting period", () => {
  it("reads the hour in the viewer's zone", () => {
    const instant = new Date("2026-01-01T22:30:00Z");
    expect(hourInZone(instant, "Asia/Tokyo")).toBe(7);
    expect(dayPeriod(instant, "Asia/Tokyo")).toBe("morning");
  });
  it("falls back to UTC for an invalid zone", () => {
    const instant = new Date("2026-01-01T03:00:00Z");
    expect(hourInZone(instant, "Not/AZone")).toBe(3);
    expect(dayPeriod(instant, "Not/AZone")).toBe("night");
  });
  it("splits the day at 6, 12 and 18", () => {
    expect(dayPeriod(new Date("2026-01-01T12:00:00Z"), "UTC")).toBe("afternoon");
    expect(dayPeriod(new Date("2026-01-01T18:00:00Z"), "UTC")).toBe("evening");
    expect(dayPeriod(new Date("2026-01-01T23:59:00Z"), "UTC")).toBe("evening");
  });
});

describe("misc formatting", () => {
  it("formats bytes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(5 * 1024 * 1024)).toBe("5 MB");
  });
  it("sanitises file names", () => {
    expect(safeFileName("a/b: c*d", "x")).toBe("a-b-c-d");
    expect(safeFileName("***", "conversation")).toBe("conversation");
  });
  it("makes one-line previews", () => {
    expect(plainPreview("# Title\n\n```js\nx\n```\nSee [docs](http://a.b) now", 100)).toBe("Title See docs now");
  });
});

describe("ids", () => {
  it("mints run ids of the documented shape", () => {
    const id = newRunId();
    expect(isRunId(id)).toBe(true);
    expect(id.length).toBeLessThanOrEqual(64);
  });
  it("marks local ids so they cannot be mistaken for server ids", () => {
    expect(isLocalId(localAssistantId(newRunId()))).toBe(true);
    expect(isLocalId("0123456789abcdef0123456789abcdef")).toBe(false);
  });
});

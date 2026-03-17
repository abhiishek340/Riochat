import { describe, expect, it } from "vitest";
import { dayPeriod } from "./format";
import { reasonFromBody, upstreamSummary } from "./error-text";
import { detectInlineImage } from "./inline-image";
import { GIVE_UP_MS, metadataChanged, nextDelay, schedule, shouldPoll } from "./metadata-poll";
import { TITLE_USES } from "./greeting";
import { formatLatency, formatMessageTime, safeFileName } from "./format";
import { forkTitle } from "./text";

function bytesToB64(bytes: number[]): string {
  return Buffer.from(Uint8Array.from(bytes)).toString("base64");
}

describe("upstream summaries", () => {
  it("prefers the structured message of the debug response", () => {
    const debug = { method: "POST", path: "/x", status: 429, requestHeaders: {}, requestBody: "", responseHeaders: {}, responseBody: '{"error":{"message":"Rate limit"}}' };
    expect(upstreamSummary({ message: "boom", debug, fallback: "fallback" })).toBe("HTTP 429: Rate limit");
  });

  it("reads the reason from the data lines of a streamed body, skipping [DONE]", () => {
    expect(reasonFromBody('data: {"error":{"message":"bad"}}\n\ndata: [DONE]')).toBe("bad");
    expect(reasonFromBody("data: [DONE]")).toBe("");
  });

  it("never uses streamed text of a successful response as the reason", () => {
    const debug = { method: "POST", path: "/x", status: 200, requestHeaders: {}, requestBody: "", responseHeaders: {}, responseBody: 'data: {"hello":"world"}' };
    expect(upstreamSummary({ message: "Error: expected a JSON body", debug, fallback: "f" })).toBe("HTTP 200: expected a JSON body");
  });

  it("falls back through Details, Error lines and the first line", () => {
    expect(upstreamSummary({ message: 'HTTP 502 upstream\nDetails: {"message":"gateway"}', fallback: "f" })).toBe("HTTP 502: gateway");
    expect(upstreamSummary({ message: "something odd happened", fallback: "f" })).toBe("something odd happened");
    expect(upstreamSummary({ message: "", fallback: "try later" })).toBe("try later");
  });
});

describe("inline images", () => {
  const png = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, ...Array(80).fill(1), 0, 0, 0, 0, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82];

  it("shows a complete png data url", () => {
    const img = detectInlineImage(`data:image/png;base64,${bytesToB64(png)}\n\nCaption`);
    expect(img).toMatchObject({ format: "png", complete: true, rest: "Caption" });
  });

  it("keeps the placeholder while the data is truncated", () => {
    const img = detectInlineImage(`data:image/png;base64,${bytesToB64(png.slice(0, 60))}`);
    expect(img).toMatchObject({ format: "png", complete: false });
  });

  it("detects raw base64 and markdown images, and ignores ordinary text", () => {
    expect(detectInlineImage(bytesToB64(png))?.complete).toBe(true);
    expect(detectInlineImage(`![x](data:image/png;base64,${bytesToB64(png)}) tail`)).toMatchObject({ complete: true, rest: "tail" });
    expect(detectInlineImage("Hello there, this is plain text")).toBeNull();
    expect(detectInlineImage("AAAA")).toBeNull();
  });

  it("verifies jpeg and gif trailers and the webp size", () => {
    const jpeg = [0xff, 0xd8, 0xff, 0xe0, ...Array(80).fill(2), 0xff, 0xd9];
    expect(detectInlineImage(`data:image/jpeg;base64,${bytesToB64(jpeg)}`)?.complete).toBe(true);
    const gif = [0x47, 0x49, 0x46, 0x38, 0x39, 0x61, ...Array(80).fill(3), 0x3b];
    expect(detectInlineImage(`data:image/gif;base64,${bytesToB64(gif)}`)?.complete).toBe(true);
    const body = Array(80).fill(4);
    const size = body.length + 4;
    const webp = [0x52, 0x49, 0x46, 0x46, size & 255, (size >> 8) & 255, 0, 0, 0x57, 0x45, 0x42, 0x50, ...body];
    expect(detectInlineImage(`data:image/webp;base64,${bytesToB64(webp)}`)?.complete).toBe(true);
    expect(detectInlineImage(`data:image/webp;base64,${bytesToB64(webp.slice(0, -5))}`)?.complete).toBe(false);
  });
});

describe("metadata polling", () => {
  it("grows 0.8 s -> 1.2 s -> 1.8 s up to 5 s and stops within 45 s", () => {
    expect(nextDelay(null)).toBe(800);
    expect(nextDelay(800)).toBe(1200);
    expect(nextDelay(1200)).toBe(1800);
    expect(nextDelay(4000)).toBe(5000);
    const all = schedule();
    expect(all.slice(0, 3)).toEqual([800, 1200, 1800]);
    expect(all.reduce((a, b) => a + b, 0)).toBeLessThanOrEqual(GIVE_UP_MS);
  });

  it("polls only while something is pending", () => {
    const base = { title: "New chat", optimisticTitle: "", labels: [], autoGenerateLabels: true, hint: undefined };
    expect(shouldPoll(base)).toBe(true);
    expect(shouldPoll({ ...base, hint: "not_needed" })).toBe(false);
    expect(shouldPoll({ ...base, hint: "skipped_no_titleable_content" })).toBe(false);
    expect(shouldPoll({ ...base, title: "Real title", labels: ["a"] })).toBe(false);
    expect(shouldPoll({ ...base, title: "Hello", optimisticTitle: "Hello", labels: ["a"] })).toBe(true);
    expect(shouldPoll({ ...base, title: "Real title", labels: [], autoGenerateLabels: false })).toBe(false);
  });

  it("detects new metadata", () => {
    const prev = { title: "Hello", labels: [] as string[] };
    expect(metadataChanged(prev, { title: "Hello", labels: [] }, "Hello")).toBe(false);
    expect(metadataChanged(prev, { title: "A real title", labels: [] }, "Hello")).toBe(true);
    expect(metadataChanged(prev, { title: "Hello", labels: ["x"] }, "Hello")).toBe(true);
  });
});

describe("format and text helpers", () => {
  it("formats latency", () => {
    expect(formatLatency(950)).toBe("0.9s");
    expect(formatLatency(40)).toBe("0.1s");
    expect(formatLatency(12_400)).toBe("12s");
    expect(formatLatency(0)).toBe("");
  });

  it("picks the greeting period in the viewer's zone", () => {
    expect(dayPeriod(new Date("2026-01-01T22:30:00Z"), "Asia/Tokyo")).toBe("morning");
    expect(dayPeriod(new Date("2026-01-01T22:30:00Z"), "Not/AZone")).toBe("evening");
    expect(dayPeriod(new Date("2026-01-01T03:10:00Z"), "Etc/UTC")).toBe("night");
    expect(TITLE_USES.filter((u) => u === "both")).toHaveLength(1);
    expect(TITLE_USES.filter((u) => u === "greeting")).toHaveLength(5);
    expect(TITLE_USES.filter((u) => u === "name")).toHaveLength(4);
  });

  it("shortens fork titles to 255 characters", () => {
    const long = "x".repeat(300);
    const t = forkTitle(long, " (Fork)");
    expect(Array.from(t)).toHaveLength(255);
    expect(t.endsWith(" (Fork)")).toBe(true);
  });

  it("builds download names", () => {
    expect(safeFileName("a/b: c*d", "x")).toBe("a-b-c-d");
    expect(safeFileName("///", "conversation")).toBe("conversation");
  });

  it("formats times by distance", () => {
    const now = new Date("2026-06-15T12:00:00Z");
    expect(formatMessageTime("2026-06-15T09:05:00Z", now, "en-US")).toMatch(/\d{1,2}:\d{2}/);
    expect(formatMessageTime("2025-01-02T09:05:00Z", now, "en-US")).toContain("2025");
    expect(formatMessageTime("garbage", now, "en-US")).toBe("");
  });
});

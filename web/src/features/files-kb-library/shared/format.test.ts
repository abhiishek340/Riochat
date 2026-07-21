import { describe, expect, it } from "vitest";
import { describeQuota, formatBytes, formatDateTime, nameInitials } from "./format";

describe("formatBytes", () => {
  it("uses 1024 steps and one decimal below 100", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(-5)).toBe("0 B");
    expect(formatBytes(Number.NaN)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1048576)).toBe("1.0 MB");
    expect(formatBytes(120 * 1024 * 1024)).toBe("120 MB");
  });
  it("never prints a value that rounds up to 1024 of the smaller unit", () => {
    expect(formatBytes(1024 * 1024 - 20)).toBe("1.0 MB");
    expect(formatBytes(1024 ** 3 - 1000)).toBe("1.0 GB");
  });
});

describe("describeQuota (scenario 15)", () => {
  it("treats a zero quota as unlimited", () => {
    expect(describeQuota({ quotaBytes: 0, usedBytes: 1048576 })).toMatchObject({ unlimited: true, used: 1048576 });
  });
  it("computes percent and remaining", () => {
    const view = describeQuota({ quotaBytes: 10 * 1048576, usedBytes: 2.5 * 1048576 });
    expect(view.unlimited).toBe(false);
    expect(view.percent).toBe(25);
    expect(formatBytes(view.remaining)).toBe("7.5 MB");
  });
  it("clamps overuse", () => {
    expect(describeQuota({ quotaBytes: 100, usedBytes: 250 })).toMatchObject({ percent: 100, remaining: 0 });
  });
});

describe("formatDateTime / nameInitials", () => {
  it("returns a dash for missing or invalid dates", () => {
    expect(formatDateTime(null, "en-US")).toBe("-");
    expect(formatDateTime("nope", "en-US")).toBe("-");
    expect(formatDateTime("2025-03-04T05:06:00Z", "en-US")).toMatch(/2025/);
  });
  it("makes two uppercase letters from the name", () => {
    expect(nameInitials("report final.pdf")).toBe("RE");
    expect(nameInitials("  ")).toBe("?");
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(nameInitials("\u6587\u6863.docx")).toBe("\u6587\u6863");
  });
});

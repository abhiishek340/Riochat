import { describe, expect, it } from "vitest";
import { encodeQr, penaltyScore, pickVersion, qrToSvg, reedSolomonRemainder } from "./qr";

describe("Reed-Solomon", () => {
  it("matches the published 1-M example", () => {
    const data = [32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17];
    expect(reedSolomonRemainder(data, 10)).toEqual([196, 35, 39, 119, 235, 215, 231, 226, 93, 23]);
  });
});

describe("encodeQr", () => {
  it("chooses the smallest version that fits", () => {
    expect(pickVersion(10)).toBe(1);
    expect(pickVersion(14)).toBe(1);
    expect(pickVersion(15)).toBe(2);
    expect(pickVersion(400)).toBeNull();
  });
  it("draws finder patterns and a timing line", () => {
    const m = encodeQr("HELLO")!;
    expect(m).toHaveLength(21);
    expect(m.every((row) => row.length === 21)).toBe(true);
    // top-left finder: dark ring, light ring, dark core
    expect(m[0]!.slice(0, 7).every(Boolean)).toBe(true);
    expect(m[1]![1]).toBe(false);
    expect(m[3]![3]).toBe(true);
    // top-right and bottom-left finders
    expect(m[0]![20]).toBe(true);
    expect(m[20]![0]).toBe(true);
    // timing pattern alternates along row 6 between the finders
    expect([8, 9, 10, 11, 12].map((x) => m[6]![x])).toEqual([true, false, true, false, true]);
    // the always-dark module
    expect(m[13]![8]).toBe(true);
  });
  it("grows for longer text and fails when it cannot fit", () => {
    const url = "otpauth://totp/Riochat:alice?secret=JBSWY3DPEHPK3PXP&issuer=Riochat&algorithm=SHA1&digits=6&period=30";
    const m = encodeQr(url)!;
    expect(m.length).toBeGreaterThan(21);
    expect((m.length - 17) % 4).toBe(0);
    expect(encodeQr("x".repeat(400))).toBeNull();
  });
  it("is deterministic", () => {
    expect(encodeQr("abc")).toEqual(encodeQr("abc"));
  });
  it("renders SVG path data with a quiet zone", () => {
    const svg = qrToSvg(encodeQr("abc")!);
    expect(svg.size).toBe(21 + 8);
    expect(svg.path.startsWith("M4 4h7v1h-7z")).toBe(true);
  });
  it("scores a uniform grid badly", () => {
    const flat = Array.from({ length: 21 }, () => new Array<boolean>(21).fill(false));
    expect(penaltyScore(flat)).toBeGreaterThan(penaltyScore(encodeQr("abc")!));
  });
});

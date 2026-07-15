import { describe, expect, it } from "vitest";
import { dataCodewords, encodeQr, formatBits, interleave, maskApplies, readCodewords, reedSolomon, versionBits, versionFor } from "./qr";

describe("Reed-Solomon", () => {
  it("matches the published 1-M example for HELLO WORLD", () => {
    const data = [32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17];
    expect(reedSolomon(data, 10)).toEqual([196, 35, 39, 119, 235, 215, 231, 226, 93, 23]);
  });
});

describe("format information", () => {
  it("produces the standard bit strings for level M", () => {
    expect(formatBits(0).toString(2).padStart(15, "0")).toBe("101010000010010");
    expect(formatBits(1).toString(2).padStart(15, "0")).toBe("101000100100101");
  });
});

describe("version information", () => {
  it("matches the standard bit string for version 7", () => {
    expect(versionBits(7).toString(2).padStart(18, "0")).toBe("000111110010010100");
  });
});

describe("version selection", () => {
  it("picks the smallest version holding the payload", () => {
    expect(versionFor(14)).toBe(1);
    expect(versionFor(15)).toBe(2);
    expect(versionFor(213)).toBe(10);
    expect(versionFor(214)).toBe(0);
  });
});

describe("data codewords", () => {
  it("encodes byte mode with terminator and pad bytes", () => {
    const cw = dataCodewords([0x41], 1);
    expect(cw).toHaveLength(16);
    expect(cw.slice(0, 3)).toEqual([0x40, 0x14, 0x10]); // 0100 00000001 01000001 0000
    expect(cw.slice(3, 6)).toEqual([0xec, 0x11, 0xec]);
  });
  it("interleaves blocks and appends error correction", () => {
    const v = 5; // two blocks of 43 data codewords, 24 ec each
    const data = dataCodewords(Array.from({ length: 60 }, (_, i) => i), v);
    const all = interleave(data, v);
    expect(all).toHaveLength(134);
    expect(all[0]).toBe(data[0]);
    expect(all[1]).toBe(data[43]); // second block's first byte comes next
  });
});

describe("encodeQr", () => {
  const text = "otpauth://totp/Riochat:ada@example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Riochat";
  const matrix = encodeQr(text);

  it("is square with a version-sized side", () => {
    expect((matrix.length - 17) % 4).toBe(0);
    expect(matrix.every((row) => row.length === matrix.length)).toBe(true);
  });
  it("draws the three finder patterns and the timing lines", () => {
    const n = matrix.length;
    const ring = (x: number, y: number) => {
      for (let d = 0; d < 7; d++) {
        for (const [dx, dy] of [[d, 0], [d, 6], [0, d], [6, d]]) expect(matrix[y + (dy as number)]?.[x + (dx as number)]).toBe(true);
      }
      expect(matrix[y + 3]?.[x + 3]).toBe(true);
      expect(matrix[y + 1]?.[x + 1]).toBe(false);
    };
    ring(0, 0);
    ring(n - 7, 0);
    ring(0, n - 7);
    for (let i = 8; i < n - 8; i++) expect(matrix[6]?.[i]).toBe(i % 2 === 0);
  });
  it("carries the codewords it was given, whichever mask it chose", () => {
    const version = (matrix.length - 17) / 4;
    const bytes = Array.from(new TextEncoder().encode(text));
    const expected = interleave(dataCodewords(bytes, version), version);
    const found = [0, 1, 2, 3, 4, 5, 6, 7].some((mask) => {
      const read = readCodewords(matrix, version, mask);
      return expected.every((b, i) => read[i] === b);
    });
    expect(found).toBe(true);
  });
  it("refuses text that does not fit", () => {
    expect(() => encodeQr("x".repeat(300))).toThrow();
  });
  it("defines all eight mask patterns", () => {
    expect([0, 1, 2, 3, 4, 5, 6, 7].map((m) => maskApplies(m, 3, 4))).toEqual([false, true, true, false, false, true, true, false]);
  });
});

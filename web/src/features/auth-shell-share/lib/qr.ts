/**
 * Minimal QR Code encoder: byte mode, error correction level M, versions 1-10
 * (up to 213 bytes), which is enough for an `otpauth://` URL. Returns the
 * module matrix; `null` when the text does not fit, so callers can fall back to
 * showing the manual key.
 */

interface VersionSpec {
  /** EC codewords per block. */
  ecPerBlock: number;
  /** [block count, data codewords per block] groups. */
  groups: ReadonlyArray<readonly [number, number]>;
  /** Alignment pattern centre coordinates. */
  align: readonly number[];
}

const SPECS: Record<number, VersionSpec> = {
  1: { ecPerBlock: 10, groups: [[1, 16]], align: [] },
  2: { ecPerBlock: 16, groups: [[1, 28]], align: [6, 18] },
  3: { ecPerBlock: 26, groups: [[1, 44]], align: [6, 22] },
  4: { ecPerBlock: 18, groups: [[2, 32]], align: [6, 26] },
  5: { ecPerBlock: 24, groups: [[2, 43]], align: [6, 30] },
  6: { ecPerBlock: 16, groups: [[4, 27]], align: [6, 34] },
  7: { ecPerBlock: 18, groups: [[4, 31]], align: [6, 22, 38] },
  8: { ecPerBlock: 22, groups: [[2, 38], [2, 39]], align: [6, 24, 42] },
  9: { ecPerBlock: 22, groups: [[3, 36], [2, 37]], align: [6, 26, 46] },
  10: { ecPerBlock: 26, groups: [[4, 43], [1, 44]], align: [6, 28, 50] },
};
const MAX_VERSION = 10;

function dataCapacity(version: number): number {
  const spec = SPECS[version];
  return spec ? spec.groups.reduce((sum, [count, size]) => sum + count * size, 0) : 0;
}

// --- Reed-Solomon over GF(256), primitive polynomial 0x11D -------------------

const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255] ?? 0;
})();

function gfMul(a: number, b: number): number {
  return a === 0 || b === 0 ? 0 : (EXP[(LOG[a] ?? 0) + (LOG[b] ?? 0)] ?? 0);
}

function generatorPoly(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] = (next[j] ?? 0) ^ (poly[j] ?? 0);
      next[j + 1] = (next[j + 1] ?? 0) ^ gfMul(poly[j] ?? 0, EXP[i] ?? 0);
    }
    poly = next;
  }
  return poly;
}

/** Remainder of data(x) * x^degree divided by the generator polynomial. */
export function reedSolomonRemainder(data: readonly number[], degree: number): number[] {
  const gen = generatorPoly(degree);
  const rem = new Array<number>(degree).fill(0);
  for (const byte of data) {
    const factor = byte ^ (rem.shift() ?? 0);
    rem.push(0);
    for (let i = 0; i < degree; i++) rem[i] = (rem[i] ?? 0) ^ gfMul(gen[i + 1] ?? 0, factor);
  }
  return rem;
}

// --- Bit stream ---------------------------------------------------------------

function buildCodewords(bytes: Uint8Array, version: number): number[] {
  const bits: number[] = [];
  const push = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  push(0b0100, 4);
  push(bytes.length, version >= 10 ? 16 : 8);
  for (const b of bytes) push(b, 8);

  const capacityBits = dataCapacity(version) * 8;
  push(0, Math.min(4, capacityBits - bits.length));
  while (bits.length % 8 !== 0) bits.push(0);

  const out: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = (byte << 1) | (bits[i + j] ?? 0);
    out.push(byte);
  }
  for (let pad = 0xec; out.length < dataCapacity(version); pad ^= 0xec ^ 0x11) out.push(pad);
  return out;
}

function interleave(data: number[], version: number): number[] {
  const spec = SPECS[version];
  if (!spec) return data;
  const blocks: number[][] = [];
  let at = 0;
  for (const [count, size] of spec.groups) {
    for (let i = 0; i < count; i++) {
      blocks.push(data.slice(at, at + size));
      at += size;
    }
  }
  const ecBlocks = blocks.map((block) => reedSolomonRemainder(block, spec.ecPerBlock));
  const out: number[] = [];
  const longest = Math.max(...blocks.map((b) => b.length));
  for (let i = 0; i < longest; i++) for (const block of blocks) if (i < block.length) out.push(block[i] ?? 0);
  for (let i = 0; i < spec.ecPerBlock; i++) for (const ec of ecBlocks) out.push(ec[i] ?? 0);
  return out;
}

// --- Matrix -------------------------------------------------------------------

const bit = (value: number, index: number): boolean => ((value >>> index) & 1) !== 0;

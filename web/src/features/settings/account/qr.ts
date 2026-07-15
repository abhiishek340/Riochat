/**
 * Small QR Code generator: byte mode, error correction level M, versions 1 to
 * 10 (up to 213 bytes). Enough for an authenticator setup link. Throws when
 * the text does not fit; callers fall back to showing the secret.
 */

// Level M parameters per version: [ecCodewordsPerBlock, [blockCount, dataCodewordsPerBlock][]].
const M_BLOCKS: readonly (readonly [number, readonly (readonly [number, number])[]])[] = [
  [10, [[1, 16]]],
  [16, [[1, 28]]],
  [26, [[1, 44]]],
  [18, [[2, 32]]],
  [24, [[2, 43]]],
  [16, [[4, 27]]],
  [18, [[4, 31]]],
  [22, [[2, 38], [2, 39]]],
  [22, [[3, 36], [2, 37]]],
  [26, [[4, 43], [1, 44]]],
];

const ALIGNMENT: readonly (readonly number[])[] = [
  [],
  [6, 18],
  [6, 22],
  [6, 26],
  [6, 30],
  [6, 34],
  [6, 22, 38],
  [6, 24, 42],
  [6, 26, 46],
  [6, 28, 50],
];

export const MAX_VERSION = M_BLOCKS.length;

function dataCapacity(version: number): number {
  const [, groups] = M_BLOCKS[version - 1] as (typeof M_BLOCKS)[number];
  return groups.reduce((sum, [count, size]) => sum + count * size, 0);
}

/** Smallest version whose capacity holds `byteLength` bytes, or 0 when none does. */
export function versionFor(byteLength: number): number {
  for (let v = 1; v <= MAX_VERSION; v++) {
    const countBits = v <= 9 ? 8 : 16;
    const bits = 4 + countBits + byteLength * 8;
    if (Math.ceil(bits / 8) <= dataCapacity(v)) return v;
  }
  return 0;
}

// ---- Reed-Solomon over GF(256), polynomial 0x11D ----------------------------------

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
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255] as number;
})();

function gfMul(a: number, b: number): number {
  return a === 0 || b === 0 ? 0 : (EXP[(LOG[a] as number) + (LOG[b] as number)] as number);
}

function generator(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] = (next[j] as number) ^ gfMul(poly[j] as number, 1);
      next[j + 1] = (next[j + 1] as number) ^ gfMul(poly[j] as number, EXP[i] as number);
    }
    poly = next;
  }
  return poly;
}

/** The `degree` error-correction codewords of `data`. */
export function reedSolomon(data: readonly number[], degree: number): number[] {
  const gen = generator(degree);
  const rem = new Array<number>(degree).fill(0);
  for (const byte of data) {
    const factor = byte ^ (rem.shift() as number);
    rem.push(0);
    for (let i = 0; i < degree; i++) rem[i] = (rem[i] as number) ^ gfMul(gen[i + 1] as number, factor);
  }
  return rem;
}

// ---- codewords -------------------------------------------------------------------

function utf8(text: string): number[] {
  return Array.from(new TextEncoder().encode(text));
}

/** Data codewords of a byte-mode segment, padded to the version's capacity. */
export function dataCodewords(bytes: readonly number[], version: number): number[] {
  const bits: number[] = [];
  const push = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  push(0b0100, 4);
  push(bytes.length, version <= 9 ? 8 : 16);
  for (const b of bytes) push(b, 8);
  const capacityBits = dataCapacity(version) * 8;
  push(0, Math.min(4, capacityBits - bits.length));
  while (bits.length % 8 !== 0) bits.push(0);
  const out: number[] = [];
  for (let i = 0; i < bits.length; i += 8) out.push(parseInt(bits.slice(i, i + 8).join(""), 2));
  for (let pad = 0xec; out.length < dataCapacity(version); pad ^= 0xec ^ 0x11) out.push(pad);
  return out;
}

/** Splits into blocks, appends error correction and interleaves. */
export function interleave(data: readonly number[], version: number): number[] {
  const [ecLen, groups] = M_BLOCKS[version - 1] as (typeof M_BLOCKS)[number];
  const blocks: number[][] = [];
  let at = 0;
  for (const [count, size] of groups) {
    for (let i = 0; i < count; i++) {
      blocks.push(data.slice(at, at + size));
      at += size;
    }
  }
  const ec = blocks.map((b) => reedSolomon(b, ecLen));
  const out: number[] = [];
  const longest = Math.max(...blocks.map((b) => b.length));
  for (let i = 0; i < longest; i++) for (const b of blocks) if (i < b.length) out.push(b[i] as number);
  for (let i = 0; i < ecLen; i++) for (const e of ec) out.push(e[i] as number);
  return out;
}

// ---- matrix ----------------------------------------------------------------------

export type Matrix = boolean[][];

const bit = (value: number, i: number): boolean => ((value >>> i) & 1) !== 0;

/** 15 format bits for level M and the mask, already XOR-masked. */
export function formatBits(mask: number): number {
  const data = (0 << 3) | mask; // level M is 0b00
  let rem = data;
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
  return ((data << 10) | rem) ^ 0x5412;
}

export function versionBits(version: number): number {
  let rem = version;
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
  return (version << 12) | rem;
}

interface Grid {
  size: number;
  modules: Matrix;
  fixed: boolean[][];
}

function newGrid(version: number): Grid {
  const size = 17 + 4 * version;
  const make = () => Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  return { size, modules: make(), fixed: make() };
}

function setFixed(g: Grid, x: number, y: number, dark: boolean): void {
  if (x < 0 || y < 0 || x >= g.size || y >= g.size) return;
  (g.modules[y] as boolean[])[x] = dark;
  (g.fixed[y] as boolean[])[x] = true;
}

function drawFormat(g: Grid, mask: number): void {
  const bits = formatBits(mask);
  for (let i = 0; i <= 5; i++) setFixed(g, 8, i, bit(bits, i));
  setFixed(g, 8, 7, bit(bits, 6));
  setFixed(g, 8, 8, bit(bits, 7));
  setFixed(g, 7, 8, bit(bits, 8));
  for (let i = 9; i < 15; i++) setFixed(g, 14 - i, 8, bit(bits, i));
  for (let i = 0; i < 8; i++) setFixed(g, g.size - 1 - i, 8, bit(bits, i));
  for (let i = 8; i < 15; i++) setFixed(g, 8, g.size - 15 + i, bit(bits, i));
  setFixed(g, 8, g.size - 8, true);
}

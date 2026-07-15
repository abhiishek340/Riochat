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

function drawFunctionPatterns(g: Grid, version: number): void {
  for (let i = 0; i < g.size; i++) {
    setFixed(g, 6, i, i % 2 === 0);
    setFixed(g, i, 6, i % 2 === 0);
  }
  for (const [cx, cy] of [
    [3, 3],
    [g.size - 4, 3],
    [3, g.size - 4],
  ] as const) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        setFixed(g, cx + dx, cy + dy, dist !== 2 && dist !== 4);
      }
    }
  }
  const pos = ALIGNMENT[version - 1] as readonly number[];
  const last = pos.length - 1;
  for (let i = 0; i < pos.length; i++) {
    for (let j = 0; j < pos.length; j++) {
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) continue;
      for (let dy = -2; dy <= 2; dy++) {
        for (let dx = -2; dx <= 2; dx++) setFixed(g, (pos[i] as number) + dx, (pos[j] as number) + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
      }
    }
  }
  drawFormat(g, 0);
  if (version >= 7) {
    const bits = versionBits(version);
    for (let i = 0; i < 18; i++) {
      const a = g.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      setFixed(g, a, b, bit(bits, i));
      setFixed(g, b, a, bit(bits, i));
    }
  }
}

function placeCodewords(g: Grid, codewords: readonly number[]): void {
  let i = 0;
  const total = codewords.length * 8;
  for (let right = g.size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < g.size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? g.size - 1 - vert : vert;
        if (!(g.fixed[y] as boolean[])[x] && i < total) {
          (g.modules[y] as boolean[])[x] = bit(codewords[i >>> 3] as number, 7 - (i & 7));
          i++;
        }
      }
    }
  }
}

export function maskApplies(mask: number, x: number, y: number): boolean {
  switch (mask) {
    case 0:
      return (x + y) % 2 === 0;
    case 1:
      return y % 2 === 0;
    case 2:
      return x % 3 === 0;
    case 3:
      return (x + y) % 3 === 0;
    case 4:
      return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
    case 5:
      return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6:
      return ((((x * y) % 2) + ((x * y) % 3)) % 2) === 0;
    default:
      return ((((x + y) % 2) + ((x * y) % 3)) % 2) === 0;
  }
}

function applyMask(g: Grid, mask: number): void {
  for (let y = 0; y < g.size; y++) {
    for (let x = 0; x < g.size; x++) {
      if (!(g.fixed[y] as boolean[])[x] && maskApplies(mask, x, y)) (g.modules[y] as boolean[])[x] = !(g.modules[y] as boolean[])[x];
    }
  }
}

/** Penalty score of a finished matrix (lower is easier to scan). */
export function penalty(m: Matrix): number {
  const size = m.length;
  const at = (x: number, y: number) => (m[y] as boolean[])[x] as boolean;
  let score = 0;
  for (let pass = 0; pass < 2; pass++) {
    for (let a = 0; a < size; a++) {
      let run = 1;
      for (let b = 1; b < size; b++) {
        const same = pass === 0 ? at(b, a) === at(b - 1, a) : at(a, b) === at(a, b - 1);
        if (same) {
          run++;
          if (run === 5) score += 3;
          else if (run > 5) score += 1;
        } else run = 1;
      }
    }
  }
  for (let y = 0; y < size - 1; y++) {
    for (let x = 0; x < size - 1; x++) {
      const c = at(x, y);
      if (c === at(x + 1, y) && c === at(x, y + 1) && c === at(x + 1, y + 1)) score += 3;
    }
  }
  const pattern = [true, false, true, true, true, false, true, false, false, false, false];
  const rev = [...pattern].reverse();
  for (let pass = 0; pass < 2; pass++) {
    for (let a = 0; a < size; a++) {
      for (let b = 0; b + 11 <= size; b++) {
        const cell = (k: number) => (pass === 0 ? at(b + k, a) : at(a, b + k));
        if (pattern.every((v, k) => cell(k) === v) || rev.every((v, k) => cell(k) === v)) score += 40;
      }
    }
  }
  let dark = 0;
  for (const row of m) for (const c of row) if (c) dark++;
  const percent = (dark * 100) / (size * size);
  score += Math.floor(Math.abs(percent - 50) / 5) * 10;
  return score;
}

/** Encodes `text` and returns the module matrix (true = dark). */
export function encodeQr(text: string): Matrix {
  const bytes = utf8(text);
  const version = versionFor(bytes.length);
  if (version === 0) throw new Error("qr: text too long");
  const codewords = interleave(dataCodewords(bytes, version), version);
  let best: Matrix | null = null;
  let bestScore = Number.POSITIVE_INFINITY;
  for (let mask = 0; mask < 8; mask++) {
    const g = newGrid(version);
    drawFunctionPatterns(g, version);
    placeCodewords(g, codewords);
    applyMask(g, mask);
    drawFormat(g, mask);
    const score = penalty(g.modules);
    if (score < bestScore) {
      bestScore = score;
      best = g.modules;
    }
  }
  return best as Matrix;
}

/**
 * Reads the codewords back out of a finished symbol of the given version and
 * mask (the inverse of placement). Used to check the encoder.
 */
export function readCodewords(m: Matrix, version: number, mask: number): number[] {
  const g = newGrid(version);
  drawFunctionPatterns(g, version);
  const out: number[] = [];
  let acc = 0;
  let n = 0;
  for (let right = g.size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < g.size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? g.size - 1 - vert : vert;
        if ((g.fixed[y] as boolean[])[x]) continue;
        const raw = (m[y] as boolean[])[x] as boolean;
        const value = maskApplies(mask, x, y) ? !raw : raw;
        acc = (acc << 1) | (value ? 1 : 0);
        if (++n === 8) {
          out.push(acc);
          acc = 0;
          n = 0;
        }
      }
    }
  }
  return out;
}

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

class Grid {
  readonly size: number;
  readonly modules: boolean[][];
  readonly reserved: boolean[][];

  constructor(version: number) {
    this.size = version * 4 + 17;
    this.modules = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
    this.reserved = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
  }

  set(x: number, y: number, dark: boolean): void {
    const row = this.modules[y];
    const flag = this.reserved[y];
    if (row && flag) {
      row[x] = dark;
      flag[x] = true;
    }
  }

  finder(cx: number, cy: number): void {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        const x = cx + dx;
        const y = cy + dy;
        if (x >= 0 && x < this.size && y >= 0 && y < this.size) this.set(x, y, dist !== 2 && dist !== 4);
      }
    }
  }

  alignment(cx: number, cy: number): void {
    for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) this.set(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  }

  formatBits(mask: number): void {
    // Level M indicator is 00.
    const data = (0 << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const n = this.size;
    for (let i = 0; i <= 5; i++) this.set(8, i, bit(bits, i));
    this.set(8, 7, bit(bits, 6));
    this.set(8, 8, bit(bits, 7));
    this.set(7, 8, bit(bits, 8));
    for (let i = 9; i < 15; i++) this.set(14 - i, 8, bit(bits, i));
    for (let i = 0; i < 8; i++) this.set(n - 1 - i, 8, bit(bits, i));
    for (let i = 8; i < 15; i++) this.set(8, n - 15 + i, bit(bits, i));
    this.set(8, n - 8, true);
  }

  versionBits(version: number): void {
    if (version < 7) return;
    let rem = version;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const bits = (version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const a = this.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      this.set(a, b, bit(bits, i));
      this.set(b, a, bit(bits, i));
    }
  }

  functionPatterns(version: number): void {
    const n = this.size;
    for (let i = 0; i < n; i++) {
      this.set(6, i, i % 2 === 0);
      this.set(i, 6, i % 2 === 0);
    }
    this.finder(3, 3);
    this.finder(n - 4, 3);
    this.finder(3, n - 4);
    const align = SPECS[version]?.align ?? [];
    const last = align.length - 1;
    align.forEach((cy, i) => {
      align.forEach((cx, j) => {
        const onFinder = (i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0);
        if (!onFinder) this.alignment(cx, cy);
      });
    });
    this.formatBits(0);
    this.versionBits(version);
  }

  placeCodewords(codewords: number[]): void {
    const n = this.size;
    let i = 0;
    for (let right = n - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (let vert = 0; vert < n; vert++) {
        for (let j = 0; j < 2; j++) {
          const x = right - j;
          const upward = ((right + 1) & 2) === 0;
          const y = upward ? n - 1 - vert : vert;
          if (!this.reserved[y]?.[x] && i < codewords.length * 8) {
            const row = this.modules[y];
            if (row) row[x] = bit(codewords[i >>> 3] ?? 0, 7 - (i & 7));
            i++;
          }
        }
      }
    }
  }

  applyMask(mask: number): void {
    for (let y = 0; y < this.size; y++) {
      for (let x = 0; x < this.size; x++) {
        let invert: boolean;
        switch (mask) {
          case 0: invert = (x + y) % 2 === 0; break;
          case 1: invert = y % 2 === 0; break;
          case 2: invert = x % 3 === 0; break;
          case 3: invert = (x + y) % 3 === 0; break;
          case 4: invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break;
          case 5: invert = ((x * y) % 2) + ((x * y) % 3) === 0; break;
          case 6: invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0; break;
          default: invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0; break;
        }
        const row = this.modules[y];
        if (row && !this.reserved[y]?.[x] && invert) row[x] = !row[x];
      }
    }
  }

  clone(): Grid {
    const copy = Object.create(Grid.prototype) as { size: number; modules: boolean[][]; reserved: boolean[][] };
    copy.size = this.size;
    copy.modules = this.modules.map((r) => r.slice());
    copy.reserved = this.reserved;
    return copy as Grid;
  }
}

/** The four mask-selection penalty rules of the specification. */
export function penaltyScore(m: readonly (readonly boolean[])[]): number {
  const n = m.length;
  const at = (x: number, y: number) => m[y]?.[x] === true;
  let score = 0;

  for (const horizontal of [true, false]) {
    for (let a = 0; a < n; a++) {
      let run = 1;
      for (let b = 1; b <= n; b++) {
        const same = b < n && (horizontal ? at(b, a) === at(b - 1, a) : at(a, b) === at(a, b - 1));
        if (same) run++;
        else {
          if (run >= 5) score += 3 + (run - 5);
          run = 1;
        }
      }
      // Finder-like 1:1:3:1:1 pattern with four light modules on either side.
      const line = Array.from({ length: n }, (_, b) => (horizontal ? at(b, a) : at(a, b)));
      for (let b = 0; b + 11 <= n; b++) {
        const seg = line.slice(b, b + 11).map((v) => (v ? 1 : 0)).join("");
        if (seg === "10111010000" || seg === "00001011101") score += 40;
      }
    }
  }
  for (let y = 0; y < n - 1; y++) {
    for (let x = 0; x < n - 1; x++) {
      const c = at(x, y);
      if (c === at(x + 1, y) && c === at(x, y + 1) && c === at(x + 1, y + 1)) score += 3;
    }
  }
  let dark = 0;
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) if (at(x, y)) dark++;
  const total = n * n;
  score += (Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1) * 10;
  return score;
}

/** Smallest supported version that holds `byteLength` bytes, or null. */
export function pickVersion(byteLength: number): number | null {
  for (let v = 1; v <= MAX_VERSION; v++) {
    const header = 4 + (v >= 10 ? 16 : 8);
    if (header + byteLength * 8 <= dataCapacity(v) * 8) return v;
  }
  return null;
}

/** Module matrix (true = dark) without the quiet zone, or null if too long. */
export function encodeQr(text: string): boolean[][] | null {
  const bytes = new TextEncoder().encode(text);
  const version = pickVersion(bytes.length);
  if (version === null) return null;
  const codewords = interleave(buildCodewords(bytes, version), version);

  const base = new Grid(version);
  base.functionPatterns(version);
  base.placeCodewords(codewords);

  let best: Grid | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const candidate = base.clone();
    candidate.applyMask(mask);
    candidate.formatBits(mask);
    const score = penaltyScore(candidate.modules);
    if (score < bestScore) {
      bestScore = score;
      best = candidate;
    }
  }
  return best ? best.modules : null;
}

/** SVG path data for the dark modules with a 4-module quiet zone; viewBox is `0 0 size size`. */
export function qrToSvg(matrix: readonly (readonly boolean[])[]): { path: string; size: number } {
  const quiet = 4;
  let path = "";
  matrix.forEach((row, y) => {
    let x = 0;
    while (x < row.length) {
      if (!row[x]) {
        x++;
        continue;
      }
      let end = x;
      while (end < row.length && row[end]) end++;
      path += `M${x + quiet} ${y + quiet}h${end - x}v1h-${end - x}z`;
      x = end;
    }
  });
  return { path, size: matrix.length + quiet * 2 };
}

#!/usr/bin/env node
// Renders the application icon set from a procedural mark: a ring with a
// speech tail on a rounded square. No external image tooling is needed.
//
//   node scripts/gen-icons.mjs [--out src-tauri/icons]
//
// Outputs: source.png (1024), icon.png (512), 32x32.png, 128x128.png,
// 128x128@2x.png, icon.ico, icon.icns and tray.png (44px template glyph).

import { deflateSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const BACKGROUND = [0xf8, 0xf8, 0xf6];
export const INK = [0x3d, 0x39, 0x29];

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

export function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const out = Buffer.alloc(12 + data.length);
  out.writeUInt32BE(data.length, 0);
  out.write(type, 4, "ascii");
  data.copy(out, 8);
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length);
  return out;
}

/** Encodes straight RGBA bytes as a PNG. */
export function encodePng(width, height, rgba) {
  const stride = width * 4;
  const raw = Buffer.alloc((stride + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (stride + 1)] = 0;
    rgba.copy(raw, y * (stride + 1) + 1, y * stride, (y + 1) * stride);
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

// Signed distance helpers in a unit square (0..1).
const sdRoundRect = (x, y, cx, cy, hw, hh, r) => {
  const qx = Math.abs(x - cx) - hw + r;
  const qy = Math.abs(y - cy) - hh + r;
  return Math.hypot(Math.max(qx, 0), Math.max(qy, 0)) + Math.min(Math.max(qx, qy), 0) - r;
};

// The mark: a ring whose lower-left carries a short tail, drawn as the union
// of a ring and a small triangle, in unit coordinates.
function markCoverage(x, y) {
  const cx = 0.5;
  const cy = 0.48;
  const d = Math.hypot(x - cx, y - cy);
  const ring = d <= 0.27 && d >= 0.16;
  // tail: triangle (0.33,0.64) (0.30,0.80) (0.46,0.70)
  const tri = inTriangle(x, y, [0.32, 0.62], [0.27, 0.8], [0.47, 0.71]);
  const dot = Math.hypot(x - cx, y - cy) <= 0.07;
  return ring || tri || dot;
}

function inTriangle(px, py, a, b, c) {
  const s = (p, q, r) => (p[0] - r[0]) * (q[1] - r[1]) - (q[0] - r[0]) * (p[1] - r[1]);
  const p = [px, py];
  const d1 = s(p, a, b);
  const d2 = s(p, b, c);
  const d3 = s(p, c, a);
  const neg = d1 < 0 || d2 < 0 || d3 < 0;
  const pos = d1 > 0 || d2 > 0 || d3 > 0;
  return !(neg && pos);
}

const SAMPLES = 3;

/** Full-colour app icon on the macOS grid (824/1024 rounded square). */
export function renderApp(size) {
  const px = Buffer.alloc(size * size * 4);
  const half = 824 / 1024 / 2;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let bg = 0;
      let ink = 0;
      for (let sy = 0; sy < SAMPLES; sy++) {
        for (let sx = 0; sx < SAMPLES; sx++) {
          const u = (x + (sx + 0.5) / SAMPLES) / size;
          const v = (y + (sy + 0.5) / SAMPLES) / size;
          if (sdRoundRect(u, v, 0.5, 0.5, half, half, 0.2 * (824 / 1024)) <= 0) {
            bg++;
            // mark occupies 82% of the plate
            const mu = 0.5 + (u - 0.5) / (0.82 * (824 / 1024));
            const mv = 0.5 + (v - 0.5) / (0.82 * (824 / 1024));
            if (markCoverage(mu, mv)) ink++;
          }
        }
      }
      const total = SAMPLES * SAMPLES;
      const o = (y * size + x) * 4;
      if (bg === 0) continue;
      const mix = ink / bg;
      px[o] = Math.round(BACKGROUND[0] * (1 - mix) + INK[0] * mix);
      px[o + 1] = Math.round(BACKGROUND[1] * (1 - mix) + INK[1] * mix);
      px[o + 2] = Math.round(BACKGROUND[2] * (1 - mix) + INK[2] * mix);
      px[o + 3] = Math.round((255 * bg) / total);
    }
  }
  return encodePng(size, size, px);
}

/** Monochrome (black on transparent) template glyph for the macOS tray. */
export function renderGlyph(size) {
  const px = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let ink = 0;
      for (let sy = 0; sy < SAMPLES; sy++) {
        for (let sx = 0; sx < SAMPLES; sx++) {
          const u = 0.5 + ((x + (sx + 0.5) / SAMPLES) / size - 0.5) / 0.92;
          const v = 0.5 + ((y + (sy + 0.5) / SAMPLES) / size - 0.5) / 0.92;
          if (markCoverage(u, v)) ink++;
        }
      }
      px[(y * size + x) * 4 + 3] = Math.round((255 * ink) / (SAMPLES * SAMPLES));
    }
  }
  return encodePng(size, size, px);
}

/** ICO container holding PNG images (supported since Windows Vista). */
export function buildIco(images) {
  const head = Buffer.alloc(6 + images.length * 16);
  head.writeUInt16LE(0, 0);
  head.writeUInt16LE(1, 2);
  head.writeUInt16LE(images.length, 4);
  let offset = head.length;
  images.forEach(({ size, png }, i) => {
    const e = 6 + i * 16;
    head[e] = size >= 256 ? 0 : size;
    head[e + 1] = size >= 256 ? 0 : size;
    head.writeUInt16LE(1, e + 4);
    head.writeUInt16LE(32, e + 6);
    head.writeUInt32LE(png.length, e + 8);
    head.writeUInt32LE(offset, e + 12);
    offset += png.length;
  });
  return Buffer.concat([head, ...images.map((i) => i.png)]);
}

/** ICNS container with PNG payloads. */
export function buildIcns(entries) {
  const parts = entries.map(({ type, png }) => {
    const h = Buffer.alloc(8);
    h.write(type, 0, "ascii");
    h.writeUInt32BE(png.length + 8, 4);
    return Buffer.concat([h, png]);
  });
  const body = Buffer.concat(parts);
  const h = Buffer.alloc(8);
  h.write("icns", 0, "ascii");
  h.writeUInt32BE(body.length + 8, 4);
  return Buffer.concat([h, body]);
}

export function generate(outDir) {
  mkdirSync(outDir, { recursive: true });
  const cache = new Map();
  const app = (s) => {
    if (!cache.has(s)) cache.set(s, renderApp(s));
    return cache.get(s);
  };
  const files = {
    "source.png": app(1024),
    "icon.png": app(512),
    "32x32.png": app(32),
    "128x128.png": app(128),
    "128x128@2x.png": app(256),
    "icon.ico": buildIco([16, 32, 48, 256].map((size) => ({ size, png: app(size) }))),
    "icon.icns": buildIcns([
      { type: "ic07", png: app(128) },
      { type: "ic08", png: app(256) },
      { type: "ic09", png: app(512) },
      { type: "ic10", png: app(1024) },
    ]),
    "tray.png": renderGlyph(44),
  };
  for (const [name, data] of Object.entries(files)) writeFileSync(path.join(outDir, name), data);
  return Object.keys(files);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const i = process.argv.indexOf("--out");
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const out = i > 0 ? path.resolve(process.argv[i + 1]) : path.join(root, "src-tauri", "icons");
  for (const name of generate(out)) console.log(path.join(out, name));
}

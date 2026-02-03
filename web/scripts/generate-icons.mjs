// Generates the brand mark as SVG plus PNG/ICO renditions, with no image
// dependencies: shapes are rasterised here with 3x3 supersampling and encoded
// with node's zlib. Run with `pnpm icons`; outputs are committed under public/.
import { deflateSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "public");
mkdirSync(join(root, "pwa"), { recursive: true });

// --- geometry in a 64 x 64 design space ---------------------------------------
const C1 = [0x1a, 0xa6, 0xb7]; // gradient start (tide teal)
const C2 = [0x1c, 0x45, 0x86]; // gradient end (deep river blue)
const BUBBLE = { x: 13, y: 14, w: 38, h: 28, r: 10 };
const TAIL = [[21, 41], [19, 52], [32, 41]];
const WAVES = [
  { y: 25.5, x0: 20, x1: 44 },
  { y: 34.5, x0: 20, x1: 38 },
];
const STROKE = 1.7; // half width

function wavePoints({ y, x0, x1 }) {
  const pts = [];
  for (let x = x0; x <= x1 + 1e-9; x += 0.5) pts.push([x, y + 2.1 * Math.sin(((x - x0) * Math.PI) / 6)]);
  return pts;
}
const wavePolys = WAVES.map(wavePoints);

function distRoundRect(px, py, { x, y, w, h, r }) {
  const cx = x + w / 2, cy = y + h / 2;
  const qx = Math.abs(px - cx) - (w / 2 - r), qy = Math.abs(py - cy) - (h / 2 - r);
  return Math.hypot(Math.max(qx, 0), Math.max(qy, 0)) + Math.min(Math.max(qx, qy), 0) - r;
}
function inTriangle(px, py, [a, b, c]) {
  const s = (p, q, r) => (p[0] - r[0]) * (q[1] - r[1]) - (q[0] - r[0]) * (p[1] - r[1]);
  const p = [px, py];
  const d1 = s(p, a, b), d2 = s(p, b, c), d3 = s(p, c, a);
  return !((d1 < 0 || d2 < 0 || d3 < 0) && (d1 > 0 || d2 > 0 || d3 > 0));
}
function distSegment(px, py, [ax, ay], [bx, by]) {
  const dx = bx - ax, dy = by - ay;
  const t = Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy || 1)));
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}
function nearWave(px, py) {
  for (const poly of wavePolys) {
    for (let i = 0; i + 1 < poly.length; i++) if (distSegment(px, py, poly[i], poly[i + 1]) <= STROKE) return true;
  }
  return false;
}
const mix = (t) => C1.map((v, i) => Math.round(v + (C2[i] - v) * t));

// Returns [r,g,b,a] for a design-space point.
function shade(px, py, { rounded, scale }) {
  // glyph is scaled about the centre; background is not
  const gx = (px - 32) / scale + 32, gy = (py - 32) / scale + 32;
  const bg = rounded ? distRoundRect(px, py, { x: 0, y: 0, w: 64, h: 64, r: 15 }) <= 0 : true;
  if (!bg) return [0, 0, 0, 0];
  const base = mix(Math.min(1, Math.max(0, (px + py) / 128)));
  const inBubble = distRoundRect(gx, gy, BUBBLE) <= 0 || inTriangle(gx, gy, TAIL);
  if (!inBubble) return [...base, 255];
  if (nearWave(gx, gy)) return [...mix(0.15), 255];
  return [250, 252, 252, 255];
}

function render(size, opts) {
  const SS = 3;
  const buf = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let r = 0, g = 0, b = 0, a = 0;
      for (let sy = 0; sy < SS; sy++) {
        for (let sx = 0; sx < SS; sx++) {
          const px = ((x + (sx + 0.5) / SS) / size) * 64, py = ((y + (sy + 0.5) / SS) / size) * 64;
          const c = shade(px, py, opts);
          r += c[0] * c[3]; g += c[1] * c[3]; b += c[2] * c[3]; a += c[3];
        }
      }
      const o = (y * size + x) * 4;
      if (a > 0) { buf[o] = Math.round(r / a); buf[o + 1] = Math.round(g / a); buf[o + 2] = Math.round(b / a); }
      buf[o + 3] = Math.round(a / (SS * SS));
    }
  }
  return buf;
}

// --- PNG / ICO encoding ---------------------------------------------------------
const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
const crc32 = (buf) => {
  let c = 0xffffffff;
  for (const byte of buf) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
};
function chunk(type, data) {
  const out = Buffer.alloc(12 + data.length);
  out.writeUInt32BE(data.length, 0);
  out.write(type, 4, "ascii");
  data.copy(out, 8);
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length);
  return out;
}
function png(size, rgba) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0); ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; ihdr[9] = 6; // 8-bit RGBA
  const raw = Buffer.alloc((size * 4 + 1) * size);
  for (let y = 0; y < size; y++) {
    raw[y * (size * 4 + 1)] = 0;
    rgba.copy(raw, y * (size * 4 + 1) + 1, y * size * 4, (y + 1) * size * 4);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw, { level: 9 })), chunk("IEND", Buffer.alloc(0)),
  ]);
}
function ico(pngBytes, size) {
  const head = Buffer.alloc(22);
  head.writeUInt16LE(0, 0); head.writeUInt16LE(1, 2); head.writeUInt16LE(1, 4);
  head[6] = size; head[7] = size; head.writeUInt16LE(1, 10); head.writeUInt16LE(32, 12);
  head.writeUInt32LE(pngBytes.length, 14); head.writeUInt32LE(22, 18);
  return Buffer.concat([head, pngBytes]);
}

// --- outputs -----------------------------------------------------------------------
const out = (rel, data) => writeFileSync(join(root, rel), data);
const rounded = { rounded: true, scale: 1 };
out("pwa/icon-192.png", png(192, render(192, rounded)));
out("pwa/icon-512.png", png(512, render(512, rounded)));
out("pwa/icon-maskable-512.png", png(512, render(512, { rounded: false, scale: 0.78 })));
out("pwa/apple-touch-icon.png", png(180, render(180, { rounded: false, scale: 0.86 })));
out("favicon.ico", ico(png(32, render(32, rounded)), 32));

const path = (poly) => "M" + poly.map(([x, y]) => `${x.toFixed(2)} ${y.toFixed(2)}`).join("L");
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="Riochat">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#${C1.map((v) => v.toString(16).padStart(2, "0")).join("")}"/>
      <stop offset="1" stop-color="#${C2.map((v) => v.toString(16).padStart(2, "0")).join("")}"/>
    </linearGradient>
  </defs>
  <rect width="64" height="64" rx="15" fill="url(#g)"/>
  <rect x="${BUBBLE.x}" y="${BUBBLE.y}" width="${BUBBLE.w}" height="${BUBBLE.h}" rx="${BUBBLE.r}" fill="#fafcfc"/>
  <path d="M${TAIL.map((p) => p.join(" ")).join("L")}Z" fill="#fafcfc"/>
  ${wavePolys.map((p) => `<path d="${path(p)}" fill="none" stroke="#1d6f9b" stroke-width="${STROKE * 2}" stroke-linecap="round" stroke-linejoin="round"/>`).join("\n  ")}
</svg>
`;
out("icon.svg", svg);
out("riochat-icon.svg", svg); // the name older branding defaults point at
console.log("icons written to", root);

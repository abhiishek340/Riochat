// A strict minimal ZIP reader used to check archives written by the tooling.

import { inflateRawSync } from "node:zlib";
import { crc32 } from "../../scripts/lib/zip.mjs";

/** Parses the central directory and returns entries with decoded data. */
export function readZip(buf) {
  const eocd = buf.length - 22;
  if (buf.readUInt32LE(eocd) !== 0x06054b50) throw new Error("no end of central directory");
  const count = buf.readUInt16LE(eocd + 10);
  const size = buf.readUInt32LE(eocd + 12);
  const offset = buf.readUInt32LE(eocd + 16);
  if (offset + size !== eocd) throw new Error("central directory does not end at the EOCD");
  const entries = [];
  let p = offset;
  for (let i = 0; i < count; i++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error("bad central header");
    const flags = buf.readUInt16LE(p + 8);
    const method = buf.readUInt16LE(p + 10);
    const crc = buf.readUInt32LE(p + 16);
    const csize = buf.readUInt32LE(p + 20);
    const usize = buf.readUInt32LE(p + 24);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const attrs = buf.readUInt32LE(p + 38);
    const local = buf.readUInt32LE(p + 42);
    const name = buf.subarray(p + 46, p + 46 + nameLen).toString("utf8");
    p += 46 + nameLen + extraLen + commentLen;
    if (buf.readUInt32LE(local) !== 0x04034b50) throw new Error("bad local header");
    const lname = buf.readUInt16LE(local + 26);
    const lextra = buf.readUInt16LE(local + 28);
    const start = local + 30 + lname + lextra;
    const raw = buf.subarray(start, start + csize);
    const data = method === 8 ? inflateRawSync(raw) : raw;
    if (method !== 0 && method !== 8) throw new Error(`unsupported method ${method}`);
    if (data.length !== usize) throw new Error(`${name}: size mismatch`);
    if (crc32(data) !== crc) throw new Error(`${name}: crc mismatch`);
    entries.push({
      name,
      data,
      method,
      flags,
      dir: name.endsWith("/"),
      dosDir: (attrs & 0x10) !== 0,
      unixMode: attrs >>> 16,
      time: buf.readUInt16LE(local + 10),
      date: buf.readUInt16LE(local + 12),
    });
  }
  return entries;
}

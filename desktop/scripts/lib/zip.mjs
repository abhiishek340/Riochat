// A small, deterministic ZIP writer (no ZIP64).
//
// - directory entries end with "/" and carry the directory attribute
// - names use forward slashes; the UTF-8 flag is set only for non-ASCII names
// - data is deflated at level 9 only when that is smaller, else stored
// - the archive is written to "<path>.partial" and renamed into place

import { deflateRawSync } from "node:zlib";
import { renameSync, rmSync, writeFileSync } from "node:fs";

const MAX_ENTRIES = 65534;
const MAX_SIZE = 0xffffffff;

const TABLE = (() => {
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
  for (let i = 0; i < buf.length; i++) c = TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

/** MS-DOS date and time for a Date, using UTC and clamped to 1980 or later. */
export function dosDateTime(date) {
  let d = date;
  if (d.getUTCFullYear() < 1980) d = new Date(Date.UTC(1980, 0, 1, 0, 0, 0));
  const time = (d.getUTCHours() << 11) | (d.getUTCMinutes() << 5) | (d.getUTCSeconds() >> 1);
  const day = ((d.getUTCFullYear() - 1980) << 9) | ((d.getUTCMonth() + 1) << 5) | d.getUTCDate();
  return { time, date: day };
}

/** The entry timestamp: SOURCE_DATE_EPOCH when set, else now. */
export function entryDate(env = process.env, now = new Date()) {
  const raw = env.SOURCE_DATE_EPOCH;
  if (raw !== undefined && raw !== "" && /^\d+$/.test(raw)) {
    const d = new Date(Number(raw) * 1000);
    if (d.getUTCFullYear() < 1980) return new Date(Date.UTC(1980, 0, 1));
    return d;
  }
  return now;
}

const isAscii = (s) => /^[\x00-\x7f]*$/.test(s);

/**
 * Builds the archive bytes. `entries` is a list of
 * `{ name, data?: Buffer, dir?: boolean, mode?: number }`; names are relative
 * with forward slashes. Directory names get a trailing slash.
 */
export function buildZip(entries, date = new Date()) {
  if (entries.length > MAX_ENTRIES) throw new Error(`too many entries for a ZIP without ZIP64 (${entries.length})`);
  const { time, date: dosDate } = dosDateTime(date);
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const entry of entries) {
    const dir = Boolean(entry.dir);
    const name = dir && !entry.name.endsWith("/") ? `${entry.name}/` : entry.name;
    if (name.includes("\\")) throw new Error(`zip entry names must use forward slashes: ${name}`);
    const nameBuf = Buffer.from(name, "utf8");
    const raw = dir ? Buffer.alloc(0) : entry.data;
    const deflated = dir || raw.length === 0 ? null : deflateRawSync(raw, { level: 9 });
    const useDeflate = deflated !== null && deflated.length < raw.length;
    const payload = useDeflate ? deflated : raw;
    if (raw.length > MAX_SIZE || payload.length > MAX_SIZE || offset > MAX_SIZE) {
      throw new Error(`entry ${name} is too large for a ZIP without ZIP64`);
    }
    const flags = isAscii(name) ? 0 : 0x0800;
    const crc = crc32(raw);
    const method = useDeflate ? 8 : 0;
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(flags, 6);
    local.writeUInt16LE(method, 8);
    local.writeUInt16LE(time, 10);
    local.writeUInt16LE(dosDate, 12);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(payload.length, 18);
    local.writeUInt32LE(raw.length, 22);
    local.writeUInt16LE(nameBuf.length, 26);
    local.writeUInt16LE(0, 28);

    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE((3 << 8) | 20, 4); // made by Unix, version 2.0
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(flags, 8);
    central.writeUInt16LE(method, 10);
    central.writeUInt16LE(time, 12);
    central.writeUInt16LE(dosDate, 14);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(payload.length, 20);
    central.writeUInt32LE(raw.length, 24);
    central.writeUInt16LE(nameBuf.length, 28);
    const unixMode = entry.mode ?? (dir ? 0o755 : 0o644);
    const typeBits = dir ? 0o040000 : 0o100000;
    const attrs = (((typeBits | unixMode) << 16) | (dir ? 0x10 : 0)) >>> 0;
    central.writeUInt32LE(attrs, 38);
    central.writeUInt32LE(offset, 42);

    locals.push(local, nameBuf, payload);
    centrals.push(central, nameBuf);
    offset += local.length + nameBuf.length + payload.length;
  }
  const centralBytes = Buffer.concat(centrals);
  if (offset + centralBytes.length > MAX_SIZE) throw new Error("archive is too large for a ZIP without ZIP64");
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(centralBytes.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, centralBytes, end]);
}

/** Writes the archive via a ".partial" file and renames it into place. */
export function writeZip(file, entries, date) {
  const bytes = buildZip(entries, date);
  const partial = `${file}.partial`;
  try {
    writeFileSync(partial, bytes);
    renameSync(partial, file);
  } catch (e) {
    rmSync(partial, { force: true });
    throw e;
  }
}

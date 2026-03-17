/**
 * Replies that start with an inline image (a data URL, a markdown data image or
 * bare base64). The image is shown once its encoded data is complete, which is
 * verified from the format's end marker.
 */

export type ImageFormat = "png" | "jpeg" | "gif" | "webp";

export interface InlineImage {
  format: ImageFormat;
  mime: string;
  base64: string;
  complete: boolean;
  /** Text after the image data. */
  rest: string;
  src: string;
}

const MIME: Record<ImageFormat, string> = { png: "image/png", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp" };
const MIN_RAW_BASE64 = 64;

function decodeBytes(b64: string): Uint8Array | null {
  try {
    const clean = b64.replace(/\s+/g, "");
    const padded = clean + "=".repeat((4 - (clean.length % 4)) % 4);
    if (typeof atob === "function") {
      const bin = atob(padded);
      const out = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
      return out;
    }
    return Uint8Array.from(Buffer.from(padded, "base64"));
  } catch {
    return null;
  }
}

function sniff(b: Uint8Array): ImageFormat | null {
  if (b.length >= 8 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47) return "png";
  if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return "jpeg";
  if (b.length >= 6 && b[0] === 0x47 && b[1] === 0x49 && b[2] === 0x46 && b[3] === 0x38) return "gif";
  if (b.length >= 12 && b[0] === 0x52 && b[1] === 0x49 && b[2] === 0x46 && b[3] === 0x46 && b[8] === 0x57 && b[9] === 0x45 && b[10] === 0x42 && b[11] === 0x50) return "webp";
  return null;
}

/** Is the byte stream a finished image of its format? */
export function isCompleteImage(b: Uint8Array, format: ImageFormat): boolean {
  if (format === "png") {
    const n = b.length;
    // IEND chunk: length 0, "IEND", CRC.
    return n >= 12 && b[n - 8] === 0x49 && b[n - 7] === 0x45 && b[n - 6] === 0x4e && b[n - 5] === 0x44;
  }
  if (format === "jpeg") return b.length >= 4 && b[b.length - 2] === 0xff && b[b.length - 1] === 0xd9;
  if (format === "gif") return b.length >= 1 && b[b.length - 1] === 0x3b;
  const size = b.length >= 8 ? (b[4]! | (b[5]! << 8) | (b[6]! << 16) | (b[7]! << 24)) >>> 0 : 0;
  return b.length >= 12 && size + 8 === b.length;
}

const BASE64_RUN = /^[A-Za-z0-9+/]+={0,2}/;

/**
 * Takes the leading base64 run. Whitespace ends it, unless the next token is
 * itself a long base64 line (wrapped payloads).
 */
function takeBase64(text: string): { data: string; rest: string } {
  let data = "";
  let pos = 0;
  for (;;) {
    const run = BASE64_RUN.exec(text.slice(pos));
    if (!run) break;
    data += run[0];
    pos += run[0].length;
    const ws = /^[ \t]*\r?\n/.exec(text.slice(pos));
    if (!ws) break;
    const next = BASE64_RUN.exec(text.slice(pos + ws[0].length));
    if (!next || next[0].length < 40) break;
    pos += ws[0].length;
  }
  return { data, rest: text.slice(pos) };
}

/**
 * Detects an image at the very start of a reply. Returns null when the reply is
 * ordinary text.
 */
export function detectInlineImage(content: string): InlineImage | null {
  const text = content.replace(/^\s+/, "");
  let format: ImageFormat | null = null;
  let payload = "";
  let rest = "";

  let m = /^data:image\/(png|jpe?g|gif|webp);base64,/i.exec(text);
  if (m) {
    const taken = takeBase64(text.slice(m[0].length));
    payload = taken.data;
    rest = taken.rest;
  } else if ((m = /^!\[[^\]]*\]\(data:image\/(png|jpe?g|gif|webp);base64,/i.exec(text))) {
    const after = text.slice(m[0].length);
    const close = after.indexOf(")");
    payload = close < 0 ? after : after.slice(0, close);
    rest = close < 0 ? "" : after.slice(close + 1);
  } else {
    const taken = takeBase64(text);
    const compact = taken.data.replace(/\s+/g, "");
    if (compact.length >= MIN_RAW_BASE64) {
      payload = taken.data;
      rest = taken.rest;
    } else {
      return null;
    }
  }
  const compact = payload.replace(/\s+/g, "");
  const probe = decodeBytes(compact.slice(0, 64));
  if (!probe) return null;
  format = sniff(probe);
  if (!format) return null;
  const bytes = decodeBytes(compact);
  const complete = !!bytes && isCompleteImage(bytes, format);
  return {
    format,
    mime: MIME[format],
    base64: compact,
    complete,
    rest: rest.replace(/^\s+/, ""),
    src: `data:${MIME[format]};base64,${compact}`,
  };
}

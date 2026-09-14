/**
 * Draws a DOM subtree to a PNG: the subtree is cloned with its computed styles
 * inlined, wrapped in an SVG foreignObject and rasterised on a canvas.
 * Browser only.
 */
import { fitScale, PADDING } from "./screenshot";

export interface RenderOptions {
  scale?: number;
  padding?: number;
  background: string;
  /** Rows (by `data-message-id`) to keep; others are left out. */
  keepRows?: ReadonlySet<string>;
  brand?: string;
  signal?: AbortSignal;
}

const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "LINK", "META"]);
const HOLLOW_TAGS = new Set(["IFRAME", "VIDEO", "AUDIO", "OBJECT", "EMBED"]);

async function toDataUrl(src: string, signal?: AbortSignal): Promise<string | null> {
  if (src.startsWith("data:")) return src;
  try {
    const res = await fetch(src, { signal, credentials: "include" });
    if (!res.ok) return null;
    const blob = await res.blob();
    return await new Promise<string | null>((resolve) => {
      const r = new FileReader();
      r.onload = () => resolve(typeof r.result === "string" ? r.result : null);
      r.onerror = () => resolve(null);
      r.readAsDataURL(blob);
    });
  } catch {
    return null;
  }
}

function inlineStyles(src: Element, dst: HTMLElement | SVGElement): void {
  const cs = getComputedStyle(src);
  let css = "";
  for (let i = 0; i < cs.length; i++) {
    const name = cs.item(i);
    const value = cs.getPropertyValue(name);
    if (value) css += `${name}:${value};`;
  }
  dst.setAttribute("style", css);
}

async function cloneNode(src: Node, opts: RenderOptions): Promise<Node | null> {
  if (src.nodeType === Node.TEXT_NODE) return src.cloneNode(false);
  if (src.nodeType !== Node.ELEMENT_NODE) return null;
  const el = src as Element;
  if (SKIP_TAGS.has(el.tagName)) return null;
  if (el.hasAttribute("data-capture-hide")) return null;
  const rowId = el.getAttribute("data-message-id");
  if (rowId !== null && opts.keepRows && !opts.keepRows.has(rowId)) return null;
  const cs = getComputedStyle(el);
  if (cs.display === "none" && !el.hasAttribute("data-capture-only")) return null;

  if (el instanceof HTMLCanvasElement) {
    const img = document.createElement("img");
    try {
      img.setAttribute("src", el.toDataURL("image/png"));
    } catch {
      return null;
    }
    inlineStyles(el, img);
    return img;
  }
  const out = el.cloneNode(false) as HTMLElement;
  for (const attr of [...out.getAttributeNames()]) if (attr.startsWith("on")) out.removeAttribute(attr);
  inlineStyles(el, out);
  if (HOLLOW_TAGS.has(el.tagName)) {
    const box = document.createElement("div");
    inlineStyles(el, box);
    return box;
  }
  if (el instanceof HTMLImageElement) {
    const data = await toDataUrl(el.currentSrc || el.src, opts.signal);
    if (data) out.setAttribute("src", data);
    out.removeAttribute("srcset");
    out.removeAttribute("loading");
    return out;
  }
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    if (el instanceof HTMLTextAreaElement) out.textContent = el.value;
    else out.setAttribute("value", el.value);
  }
  for (const child of Array.from(el.childNodes)) {
    const c = await cloneNode(child, opts);
    if (c) out.appendChild(c);
  }
  return out;
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("render failed"));
    img.src = url;
  });
}

export async function renderToPng(root: HTMLElement, opts: RenderOptions): Promise<Blob> {
  const padding = opts.padding ?? PADDING;
  const body = await cloneNode(root, opts);
  if (!body) throw new Error("nothing to capture");
  if (opts.signal?.aborted) throw new DOMException("aborted", "AbortError");
  const width = Math.ceil(root.getBoundingClientRect().width);
  // Computed margins and widths are used values here: the clone sits at the left edge at its measured width.
  const rootStyle = (body as HTMLElement).style;
  rootStyle.margin = "0";
  rootStyle.maxWidth = "none";
  rootStyle.width = `${width}px`;
  const bodyStyle = getComputedStyle(document.body);
  const wrapper = document.createElement("div");
  wrapper.setAttribute("xmlns", "http://www.w3.org/1999/xhtml");
  wrapper.style.cssText = `box-sizing:border-box;width:${width + padding * 2}px;padding:${padding}px;background:${opts.background};color:${bodyStyle.color};font-family:${bodyStyle.fontFamily.replace(/"/g, "'")}`;
  const mark = (position: "top" | "bottom") => {
    if (!opts.brand) return null;
    const d = document.createElement("div");
    d.style.cssText = `font:600 13px/1 system-ui,sans-serif;opacity:.55;${position === "top" ? "margin-bottom" : "margin-top"}:${padding / 2}px;text-align:center`;
    d.textContent = opts.brand;
    return d;
  };
  const top = mark("top");
  const bottom = mark("bottom");
  if (top) wrapper.appendChild(top);
  wrapper.appendChild(body);
  if (bottom) wrapper.appendChild(bottom);

  // Measure the finished clone off-screen so the canvas matches it exactly.
  const probe = document.createElement("div");
  probe.style.cssText = "position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none";
  probe.appendChild(wrapper.cloneNode(true));
  document.body.appendChild(probe);
  const height = Math.ceil((probe.firstElementChild as HTMLElement).getBoundingClientRect().height);
  probe.remove();
  const totalWidth = width + padding * 2;

  const scale = fitScale(totalWidth, height, opts.scale);
  const xml = new XMLSerializer().serializeToString(wrapper);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${totalWidth}" height="${height}"><foreignObject x="0" y="0" width="100%" height="100%">${xml}</foreignObject></svg>`;
  const img = await loadImage(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`);
  if (opts.signal?.aborted) throw new DOMException("aborted", "AbortError");
  const canvas = document.createElement("canvas");
  canvas.width = Math.floor(totalWidth * scale);
  canvas.height = Math.floor(height * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas unavailable");
  ctx.fillStyle = opts.background;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0, totalWidth, height);
  return await new Promise<Blob>((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("encode failed"))), "image/png"));
}

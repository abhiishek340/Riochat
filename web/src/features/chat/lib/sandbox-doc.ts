/**
 * Documents for sandboxed previews. Everything model-written runs inside an
 * iframe with `sandbox="allow-scripts"` only; the document itself also forbids
 * network access, frames, workers and form posts through a CSP.
 */

export const SANDBOX_ATTR = "allow-scripts";
export const PERMISSIONS_POLICY =
  "accelerometer 'none'; autoplay 'none'; camera 'none'; clipboard-read 'none'; clipboard-write 'none'; encrypted-media 'none'; fullscreen 'none'; geolocation 'none'; gyroscope 'none'; microphone 'none'; midi 'none'; payment 'none'; serial 'none'; usb 'none'; bluetooth 'none'";

export const CSP =
  "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data: blob:; media-src data: blob:; font-src data:; base-uri 'none'; form-action 'none'; object-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; connect-src 'none'; manifest-src 'none'";

export interface PreviewTheme {
  /** Allow-listed CSS variables and their current values. */
  variables: Record<string, string>;
  colorScheme: "light" | "dark";
}

/** Theme variables a preview may see (colors and radii only). */
export const THEME_VARIABLES = [
  "--background", "--foreground", "--card", "--card-foreground", "--popover", "--popover-foreground", "--primary", "--primary-foreground", "--secondary",
  "--secondary-foreground", "--muted", "--muted-foreground", "--accent", "--accent-foreground", "--destructive", "--success", "--warning", "--info", "--border",
  "--input", "--ring", "--chart-1", "--chart-2", "--chart-3", "--chart-4", "--chart-5", "--radius",
] as const;

const VAR_NAME = /^--[a-z0-9-]+$/;
const SAFE_VALUE = /^[^<>{};@\\]{0,200}$/;

export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

/** Neutralises sequences that would end the surrounding element early. */
export function escapeStyle(css: string): string {
  return css.replace(/<\/(style)/gi, "<\\/$1").replace(/<!--/g, "<\\!--");
}

export function escapeScript(js: string): string {
  return js.replace(/<\/(script)/gi, "<\\/$1").replace(/<!--/g, "<\\!--");
}

export function themeStyle(theme: PreviewTheme): string {
  const vars = Object.entries(theme.variables)
    .filter(([k, v]) => VAR_NAME.test(k) && SAFE_VALUE.test(v))
    .map(([k, v]) => `${k}:${v.replace(/\s+/g, " ").trim()}`)
    .join(";");
  return escapeStyle(`:root{color-scheme:${theme.colorScheme};${vars}}`);
}

const RESET = `*,*::before,*::after{box-sizing:border-box}html,body{margin:0}body{font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;line-height:1.5;color:var(--foreground,CanvasText);background:var(--background,Canvas)}`;

const ERROR_REPORTER = `(function(){function show(m){try{var d=document.createElement('pre');d.setAttribute('data-preview-error','');d.style.cssText='color:#b42318;background:rgba(180,35,24,.08);padding:8px 10px;margin:8px;border-radius:6px;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace';d.textContent=String(m);(document.body||document.documentElement).appendChild(d)}catch(e){}}window.addEventListener('error',function(e){show(e.message||e.error)});window.addEventListener('unhandledrejection',function(e){show('Unhandled rejection: '+(e.reason&&e.reason.message||e.reason))})})();`;

export function headBlock(theme: PreviewTheme, title: string): string {
  return (
    `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${CSP}">` +
    `<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer">` +
    `<title>${escapeHtml(title)}</title><style>${themeStyle(theme)}${escapeStyle(RESET)}</style><script>${escapeScript(ERROR_REPORTER)}</script>`
  );
}

const TITLE = "Preview";

/** HTML: user markup is kept; our head goes first so the CSP applies to everything after it. */
export function htmlDocument(code: string, theme: PreviewTheme): string {
  const hasHead = /<head[\s>]/i.test(code);
  const hasHtml = /<html[\s>]/i.test(code);
  if (hasHead) {
    return code.replace(/<head([^>]*)>/i, (_m, attrs: string) => `<head${attrs}>${headBlock(theme, TITLE)}`);
  }
  if (hasHtml) {
    return code.replace(/<html([^>]*)>/i, (_m, attrs: string) => `<html${attrs}><head>${headBlock(theme, TITLE)}</head>`);
  }
  const doctype = /^\s*<!doctype[^>]*>/i.exec(code);
  const rest = doctype ? code.slice(doctype[0].length) : code;
  return `<!doctype html><html><head>${headBlock(theme, TITLE)}</head><body>${rest}</body></html>`;
}

const DEMO = `<main class="demo"><h1>Heading</h1><h2>Subheading</h2><p>Paragraph text with <a href="#">a link</a>, <strong>bold</strong> and <em>italic</em> words.</p><ul><li>First item</li><li>Second item</li></ul><p><button class="btn">Button</button> <input class="field" placeholder="Input" /> <select><option>Option</option></select></p><div class="card box">A card or box</div></main>`;

export function cssDocument(css: string, theme: PreviewTheme): string {
  return `<!doctype html><html><head>${headBlock(theme, "CSS preview")}<style>.demo{padding:16px;max-width:640px}</style><style>${escapeStyle(css)}</style></head><body>${DEMO}</body></html>`;
}

const CONSOLE_HOOK = `(function(){var out;function el(){if(!out){out=document.getElementById('console')}return out}function fmt(a){try{if(typeof a==='string')return a;if(a instanceof Error)return a.stack||a.message;return JSON.stringify(a,null,2)}catch(e){return String(a)}}['log','info','warn','error','debug'].forEach(function(k){var o=console[k];console[k]=function(){var line=document.createElement('div');line.className='line '+k;line.textContent=Array.prototype.map.call(arguments,fmt).join(' ');var c=el();if(c)c.appendChild(line);try{o.apply(console,arguments)}catch(e){}}})})();`;

export function javascriptDocument(js: string, theme: PreviewTheme): string {
  const style = `<style>#console{margin:0;padding:10px 12px;font:12px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap}#console .line{padding:2px 0;border-bottom:1px solid var(--border,#ddd)}#console .warn{color:#b54708}#console .error{color:#b42318}</style>`;
  return `<!doctype html><html><head>${headBlock(theme, "JavaScript preview")}${style}</head><body><div id="console" aria-live="polite"></div><script>${escapeScript(CONSOLE_HOOK)}</script><script>${escapeScript(js)}</script></body></html>`;
}

/** SVG: parsed first; invalid markup is reported instead of injected. */
export function validateSvg(svg: string): { ok: true } | { ok: false; reason: string } {
  if (typeof DOMParser === "undefined") return { ok: true };
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const err = doc.getElementsByTagName("parsererror")[0];
  if (err) return { ok: false, reason: (err.textContent ?? "").trim().split("\n")[0] ?? "invalid" };
  const root = doc.documentElement;
  if (!root || root.localName.toLowerCase() !== "svg") return { ok: false, reason: "not an svg document" };
  return { ok: true };
}

/** A data URL for an <img>: images never run scripts. */
export function svgDataUrl(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

export type PreviewKind = "html" | "css" | "javascript" | "svg";

export function previewDocument(kind: Exclude<PreviewKind, "svg">, code: string, theme: PreviewTheme): string {
  switch (kind) {
    case "html":
      return htmlDocument(code, theme);
    case "css":
      return cssDocument(code, theme);
    case "javascript":
      return javascriptDocument(code, theme);
  }
}

/** Reads the allow-listed theme variables from the live document. */
export function captureTheme(): PreviewTheme {
  if (typeof document === "undefined") return { variables: {}, colorScheme: "light" };
  const root = document.documentElement;
  const cs = getComputedStyle(root);
  const variables: Record<string, string> = {};
  for (const name of THEME_VARIABLES) {
    const v = cs.getPropertyValue(name).trim();
    if (v) variables[name] = v;
  }
  return { variables, colorScheme: root.classList.contains("dark") ? "dark" : "light" };
}

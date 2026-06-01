/** Clipboard and file-saving helpers. */

/** Marker type put on copies from the app's own code blocks, so a paste can tell. */
export const CODE_CLIPBOARD_TYPE = "application/x-riochat-code";

export async function copyText(text: string, options: { codeBlock?: boolean } = {}): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.write && options.codeBlock && typeof ClipboardItem !== "undefined") {
      try {
        const item = new ClipboardItem({
          "text/plain": new Blob([text], { type: "text/plain" }),
          [`web ${CODE_CLIPBOARD_TYPE}`]: new Blob([text], { type: CODE_CLIPBOARD_TYPE }),
        } as Record<string, Blob>);
        await navigator.clipboard.write([item]);
        return true;
      } catch {
        /* custom types are not supported everywhere: fall through */
      }
    }
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall back below */
  }
  return legacyCopy(text);
}

function legacyCopy(text: string): boolean {
  if (typeof document === "undefined") return false;
  const active = document.activeElement as HTMLElement | null;
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.cssText = "position:fixed;top:0;left:0;opacity:0;pointer-events:none";
  document.body.appendChild(area);
  const selection = document.getSelection();
  const saved = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null;
  area.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  area.remove();
  if (saved && selection) {
    selection.removeAllRanges();
    selection.addRange(saved);
  }
  active?.focus?.();
  return ok;
}

export function downloadBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function downloadText(text: string, fileName: string, type = "text/plain;charset=utf-8"): void {
  downloadBlob(new Blob([text], { type }), fileName);
}

export function timestampForFile(date = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}${p(date.getMonth() + 1)}${p(date.getDate())}-${p(date.getHours())}${p(date.getMinutes())}${p(date.getSeconds())}`;
}

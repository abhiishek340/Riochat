/** Browser-side hand-offs for a loaded preview (blob URL). */
export function openInNewTab(url: string): void {
  const win = window.open(url, "_blank", "noopener,noreferrer");
  if (win) win.opener = null;
}

export function downloadBlobUrl(url: string, fileName: string): void {
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName || "download";
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

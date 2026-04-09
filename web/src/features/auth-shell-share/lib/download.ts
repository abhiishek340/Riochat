/** Triggers a browser download for in-memory data. */
export function downloadBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  link.rel = "noopener";
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  // Revoke after the click has been handled.
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** A safe file name stem from a conversation title. */
export function fileStem(title: string, fallback = "conversation"): string {
  const stem = title
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 60);
  return stem || fallback;
}

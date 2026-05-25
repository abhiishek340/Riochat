/** The desktop download link is admin-supplied: only plain http(s) URLs are accepted. */
export function validDownloadUrl(raw: string | null | undefined): string {
  const value = (raw ?? "").trim();
  if (!value || value.length > 200) return "";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "";
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return "";
  if (!url.hostname || url.username || url.password) return "";
  return value;
}

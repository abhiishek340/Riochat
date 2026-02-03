// Serves the static export (out/) for a quick local check: `pnpm build && pnpm start`.
// Production hosting is the Go backend (frontend_dist_dir); this is only a convenience.
import { createServer } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(fileURLToPath(new URL("..", import.meta.url)), "out");
const port = Number(process.env.PORT ?? 3000);
const types = {
  ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".json": "application/json",
  ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".txt": "text/plain", ".webmanifest": "application/manifest+json",
};

async function find(urlPath) {
  const clean = normalize(decodeURIComponent(urlPath)).replace(/^(\.\.[/\\])+/, "");
  for (const candidate of [clean, `${clean}.html`, join(clean, "index.html")]) {
    const file = join(root, candidate);
    if (!file.startsWith(root)) continue;
    try { if ((await stat(file)).isFile()) return file; } catch { /* next candidate */ }
  }
  return null;
}

createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", "http://x");
  const file = (await find(url.pathname)) ?? join(root, "404.html");
  try {
    res.writeHead(file.endsWith("404.html") ? 404 : 200, { "content-type": types[extname(file)] ?? "application/octet-stream" });
    res.end(await readFile(file));
  } catch {
    res.writeHead(500).end("export not built: run `pnpm build` first");
  }
}).listen(port, () => console.log(`serving out/ on http://localhost:${port}`));

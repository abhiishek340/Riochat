import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

/**
 * The production artifact is a static export (`out/`): the Go backend hosts it
 * and there is no Node runtime. Only `next dev` needs a server, where it
 * forwards API traffic to the backend so the browser sees a single origin
 * (the refresh cookie and CORS then need no special handling).
 */
const backend = (process.env.RIOCHAT_BACKEND_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");

export default function config(phase: string): NextConfig {
  const dev = phase === PHASE_DEVELOPMENT_SERVER;
  return {
    reactStrictMode: true,
    poweredByHeader: false,
    // The backend compresses its own responses; recompressing a proxied
    // stream in dev would buffer it.
    compress: false,
    images: { unoptimized: true },
    // Linting is a separate gate (`pnpm lint`, CI); a style finding must not
    // stop the container image or the desktop export from building.
    eslint: { ignoreDuringBuilds: true },
    ...(dev
      ? {
          async rewrites() {
            return [
              { source: "/api/:path*", destination: `${backend}/api/:path*` },
              { source: "/manifest.webmanifest", destination: `${backend}/manifest.webmanifest` },
            ];
          },
        }
      : { output: "export" as const }),
  };
}

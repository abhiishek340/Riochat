import type { Metadata, Viewport } from "next";
import type { ReactNode } from "react";
import { RootProviders } from "@/components/providers/root-providers";
import { THEME_BOOTSTRAP_SCRIPT } from "@/lib/theme/config";
import "./globals.css";

/*
 * Static defaults. At runtime the branding provider rewrites the title,
 * description, icons and manifest link from the backend's branding endpoint.
 */
export const metadata: Metadata = {
  title: "Riochat",
  description: "Riochat is a multi-model AI chat studio.",
  applicationName: "Riochat",
  manifest: "/api/v1/branding/manifest.webmanifest",
  icons: { icon: [{ url: "/icon.svg", type: "image/svg+xml" }], apple: [{ url: "/pwa/apple-touch-icon.png", sizes: "180x180" }] },
  appleWebApp: { capable: true, title: "Riochat", statusBarStyle: "default" },
  formatDetection: { telephone: false },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f8f7f2" },
    { media: "(prefers-color-scheme: dark)", color: "#10151c" },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    // data-branding="pending" hides the body until branding and the catalog
    // are ready; the theme script and provider mutate <html>, hence the
    // suppressed hydration warning.
    <html lang="en" data-branding="pending" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOTSTRAP_SCRIPT }} />
        <noscript>
          <style>{`html[data-branding="pending"] body{opacity:1}`}</style>
        </noscript>
      </head>
      <body suppressHydrationWarning>
        <RootProviders>{children}</RootProviders>
      </body>
    </html>
  );
}

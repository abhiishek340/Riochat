import type { Metadata } from "next";
import { Suspense } from "react";
import { ProviderImportPage } from "@/features/settings";

// The import link carries an API key in the URL fragment: keep it out of
// referrers and search indexes, and keep this route outside the signed-in
// workspace so the fragment can be removed before any sign-in redirect.
export const metadata: Metadata = {
  referrer: "no-referrer",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <Suspense>
      <ProviderImportPage />
    </Suspense>
  );
}

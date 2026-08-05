import { Suspense } from "react";
import { SharePage } from "@/features/auth-shell-share";

// The share id travels in the query string (static export has no dynamic segments).
export default function Page() {
  return (
    <Suspense>
      <SharePage />
    </Suspense>
  );
}

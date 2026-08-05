import { Suspense } from "react";
import { AuthCallbackPage } from "@/features/auth-shell-share";

export default function Page() {
  return (
    <Suspense>
      <AuthCallbackPage />
    </Suspense>
  );
}

import { Suspense } from "react";
import { LoginPage } from "@/features/auth-shell-share";

export default function Page() {
  return (
    <Suspense>
      <LoginPage />
    </Suspense>
  );
}

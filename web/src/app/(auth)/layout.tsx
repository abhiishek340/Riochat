import type { ReactNode } from "react";
import { AuthLayoutFrame } from "@/components/shell/auth-layout-frame";

/** Signed-out routes: a centred card on a quiet brand backdrop. */
export default function AuthLayout({ children }: { children: ReactNode }) {
  return <AuthLayoutFrame>{children}</AuthLayoutFrame>;
}

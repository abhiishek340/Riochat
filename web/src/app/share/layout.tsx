import type { ReactNode } from "react";
import { ShareLayout } from "@/features/auth-shell-share";

/** Shared conversations: the app shell for signed-in viewers, a bare public frame for everyone else. */
export default function ShareRouteLayout({ children }: { children: ReactNode }) {
  return <ShareLayout>{children}</ShareLayout>;
}

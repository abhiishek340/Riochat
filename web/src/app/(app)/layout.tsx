import type { ReactNode } from "react";
import { AppWorkspace } from "@/components/shell/app-workspace";

/** Signed-in workspace: auth guard, feature policy, sidebar shell. */
export default function AppLayout({ children }: { children: ReactNode }) {
  return <AppWorkspace>{children}</AppWorkspace>;
}

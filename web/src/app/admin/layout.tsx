import type { ReactNode } from "react";
import { AdminWorkspace } from "@/components/shell/admin-workspace";
import { AdminBridgeNotice } from "@/features/admin";

/** Administration console: auth guard, admin gate, admin shell. */
export default function AdminLayout({ children }: { children: ReactNode }) {
  return (
    <AdminWorkspace>
      {children}
      <AdminBridgeNotice />
    </AdminWorkspace>
  );
}

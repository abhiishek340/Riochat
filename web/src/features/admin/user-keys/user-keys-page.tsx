"use client";

import { Ban, KeyRound, Link2, MoreHorizontal, Play, Trash2, UserRound, X } from "lucide-react";
import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
import { useI18n, useT } from "@/lib/i18n/provider";
import { BulkBar } from "../components/bulk-bar";
import { CopyButton } from "../components/copy-button";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { AdminPage, Mono, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { SettingsGroup } from "../components/settings-group";
import { StatusBadge } from "../components/status";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { formatDateTime, formatNumber } from "../lib/format";
import { protocolLabel } from "../lib/protocols";
import type { FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useSelection } from "../lib/use-selection";
import { useSettingsForm } from "../lib/use-settings-form";
import { deleteProviders, listProviders, suspendHost, suspendProviders, type PersonalProvider } from "./api";
import { buildImportLink, IMPORT_PROTOCOLS } from "./import-link";

const NS = "personal_provider";
const FIELDS: FieldDef[] = [
  { ns: NS, key: "enabled", type: "bool", label: "adminUserKeys.policy.enabled", hint: "adminUserKeys.policy.enabledHint", def: "false" },
  { ns: NS, key: "max_per_user", type: "int", label: "adminUserKeys.policy.maxPerUser", hint: "adminUserKeys.policy.maxPerUserHint", def: "10", visible: (d) => d[`${NS}.enabled`] === "true" },
  { ns: NS, key: "blocked_hosts", type: "textarea", label: "adminUserKeys.policy.blockedHosts", hint: "adminUserKeys.policy.blockedHostsHint", def: "", visible: (d) => d[`${NS}.enabled`] === "true" },
];

/** Window event raised after the access policy changed; the user-side access cache listens. */
export const PERSONAL_PROVIDER_ACCESS_EVENT = "riochat:personal-provider-access-changed";

type Confirm = { kind: "suspend" | "resume" | "delete"; ids: string[] } | { kind: "host"; host: string } | null;

export function AdminUserKeysPage() {
  return (
    <AdminSection feature="multiUser">
      <UserKeysContent />
    </AdminSection>
  );
}

function UserKeysContent() {
  const t = useT();
  const [linkOpen, setLinkOpen] = useState(false);
  const form = useSettingsForm({ fields: FIELDS });
  return (
    <AdminPage
      title={t("adminUserKeys.title")}
      description={t("adminUserKeys.description")}
      actions={
        <Button variant="outline" onClick={() => setLinkOpen(true)}>
          <Link2 />
          {t("adminUserKeys.importLink")}
        </Button>
      }
    >
      <p className="flex items-start gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-[13px] text-muted-foreground">
        <KeyRound aria-hidden className="mt-0.5 size-4 shrink-0" />
        {t("adminUserKeys.securityNote")}
      </p>
      <SettingsGroup
        form={form}
        fields={FIELDS}
        title={t("adminUserKeys.policy.title")}
        description={t("adminUserKeys.policy.description")}
        onSaved={() => window.dispatchEvent(new CustomEvent(PERSONAL_PROVIDER_ACCESS_EVENT))}
      />
      <ProviderList />
      <ImportLinkDialog open={linkOpen} onOpenChange={setLinkOpen} />
    </AdminPage>
  );
}

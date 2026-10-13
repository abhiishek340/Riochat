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

function ProviderList() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const selection = useSelection<string>();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [owner, setOwner] = useState<{ id: number; label: string } | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const filters = useMemo(() => ({ q, status, ownerUserId: owner?.id }), [q, status, owner]);

  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listProviders({ page, pageSize, ...f }, signal),
    onError: (error) => notify.error("adminUserKeys.toast.loadFailed", error),
  });

  const run = async (action: Confirm) => {
    if (!action) return;
    try {
      if (action.kind === "host") {
        const count = await suspendHost(action.host);
        notify.success("adminUserKeys.toast.hostSuspended", { count, host: action.host });
      } else if (action.kind === "delete") {
        notify.success("adminUserKeys.toast.deleted", { count: await deleteProviders(action.ids) });
      } else {
        const count = await suspendProviders(action.ids, action.kind === "suspend");
        notify.success(action.kind === "suspend" ? "adminUserKeys.toast.suspended" : "adminUserKeys.toast.resumed", { count });
      }
    } finally {
      selection.clear();
      list.reload();
    }
  };

  const ownerName = (p: PersonalProvider) => p.ownerLabel || p.ownerDisplayName || p.ownerUsername || String(p.ownerUserID);
  const columns: Column<PersonalProvider>[] = [
    {
      id: "owner",
      sticky: "left",
      header: t("adminUserKeys.list.columns.owner"),
      cell: (p) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{ownerName(p)}</div>
          {p.ownerEmail ? <div className="truncate text-xs text-muted-foreground">{p.ownerEmail}</div> : null}
        </div>
      ),
    },
    {
      id: "name",
      header: t("adminUserKeys.list.columns.name"),
      cell: (p) => (
        <div className="min-w-0">
          <div className="truncate">{p.name}</div>
          <div className="truncate text-xs text-muted-foreground">
            {protocolLabel(p.protocol)}
            {p.keyHint ? ` · ${t("adminUserKeys.actions.keyHint", { hint: p.keyHint })}` : ""}
          </div>
        </div>
      ),
    },
    { id: "url", header: t("adminUserKeys.list.columns.address"), cell: (p) => <Mono className="break-all">{p.baseURL}</Mono> },
    { id: "models", header: t("adminUserKeys.list.columns.models"), className: "tabular-nums", cell: (p) => formatNumber(p.modelCount, locale) },
    {
      id: "status",
      header: t("adminUserKeys.list.columns.status"),
      cell: (p) => (
        <div className="flex items-center gap-1.5">
          <StatusBadge value={p.status} prefix="adminUserKeys.status" />
          {p.lastError ? (
            <Tooltip label={`${t("adminUserKeys.actions.lastError")}: ${p.lastError}`}>
              <Badge tone="danger">!</Badge>
            </Tooltip>
          ) : null}
        </div>
      ),
    },
    { id: "created", header: t("adminUserKeys.list.columns.created"), className: "whitespace-nowrap text-muted-foreground", cell: (p) => formatDateTime(p.createdAt, locale) },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (p) => (
        <CellActions>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("adminCommon.table.moreActions")}: ${p.name}`}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => setOwner({ id: p.ownerUserID, label: ownerName(p) })}>
                <UserRound />
                {t("adminUserKeys.actions.filterByOwner")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {p.status === "suspended" ? (
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "resume", ids: [p.id] })}>
                  <Play />
                  {t("adminUserKeys.actions.resume")}
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "suspend", ids: [p.id] })}>
                  <Ban />
                  {t("adminUserKeys.actions.suspend")}
                </DropdownMenuItem>
              )}
              {p.host ? (
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "host", host: p.host })}>
                  <Ban />
                  {t("adminUserKeys.actions.suspendHost")}
                </DropdownMenuItem>
              ) : null}
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onSelect={() => setConfirm({ kind: "delete", ids: [p.id] })}>
                <Trash2 />
                {t("adminUserKeys.actions.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </CellActions>
      ),
    },
  ];

  const count = confirm && confirm.kind !== "host" ? confirm.ids.length : 0;
  const dialog = confirm
    ? confirm.kind === "host"
      ? { title: t("adminUserKeys.confirm.hostTitle", { host: confirm.host }), body: t("adminUserKeys.confirm.hostBody"), label: t("adminUserKeys.actions.suspendHost"), destructive: true }
      : confirm.kind === "delete"
        ? { title: t("adminUserKeys.confirm.deleteTitle", { count }), body: t("adminUserKeys.confirm.deleteBody"), label: t("adminUserKeys.actions.delete"), destructive: true }
        : confirm.kind === "suspend"
          ? { title: t("adminUserKeys.confirm.suspendTitle", { count }), body: t("adminUserKeys.confirm.suspendBody"), label: t("adminUserKeys.actions.suspend"), destructive: true }
          : { title: t("adminUserKeys.confirm.resumeTitle", { count }), body: t("adminUserKeys.confirm.resumeBody"), label: t("adminUserKeys.actions.resume"), destructive: false }
    : null;
  const ids = [...selection.selected];

  return (
    <Section title={t("adminUserKeys.list.title")} flush>
      <div className="space-y-3 p-4">
        <Toolbar>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminUserKeys.list.search")} className="sm:w-72" />
          <OptionSelect
            value={status}
            onChange={setStatus}
            label={t("adminCommon.table.status")}
            options={[
              { value: "", label: t("adminUserKeys.list.allStatuses") },
              { value: "active", label: t("adminUserKeys.status.active") },
              { value: "disabled", label: t("adminUserKeys.status.disabled") },
              { value: "suspended", label: t("adminUserKeys.status.suspended") },
            ]}
          />
          {owner ? (
            <Button variant="secondary" size="sm" onClick={() => setOwner(null)} aria-label={t("adminUserKeys.list.clearOwner")}>
              {t("adminUserKeys.list.ownerFilter", { owner: owner.label })}
              <X />
            </Button>
          ) : null}
        </Toolbar>
        <BulkBar count={selection.count} onClear={selection.clear}>
          <Button size="sm" variant="outline" onClick={() => setConfirm({ kind: "suspend", ids })}>
            {t("adminUserKeys.actions.suspend")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setConfirm({ kind: "resume", ids })}>
            {t("adminUserKeys.actions.resume")}
          </Button>
          <Button size="sm" variant="destructive" onClick={() => setConfirm({ kind: "delete", ids })}>
            {t("adminUserKeys.actions.delete")}
          </Button>
        </BulkBar>
        <DataTable
          columns={columns}
          rows={list.rows}
          rowKey={(p) => p.id}
          loading={list.loading} error={list.error} onRetry={list.reload}
          fetching={list.fetching}
          selection={selection}
          empty={t("adminUserKeys.list.empty")}
          rowLabel={(p) => p.name} label={t("adminUserKeys.list.title")}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
      </div>
      {dialog ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setConfirm(null)}
          title={dialog.title}
          description={dialog.body}
          confirmLabel={dialog.label}
          destructive={dialog.destructive}
          onConfirm={() => run(confirm)}
        />
      ) : null}
    </Section>
  );
}

function ImportLinkDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");
  const [protocol, setProtocol] = useState("");
  const [name, setName] = useState("");
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  const link = buildImportLink(origin, [{ url, key, protocol, name }]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("adminUserKeys.link.title")}</DialogTitle>
          <DialogDescription>{t("adminUserKeys.link.description")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <Field required>
            <FieldLabel>{t("adminUserKeys.link.url")}</FieldLabel>
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://api.example.com/v1" autoComplete="off" spellCheck={false} />
          </Field>
          <Field required>
            <FieldLabel>{t("adminUserKeys.link.key")}</FieldLabel>
            <Input value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminUserKeys.link.protocol")}</FieldLabel>
              <OptionSelect
                value={protocol}
                onChange={setProtocol}
                options={[{ value: "", label: t("adminUserKeys.link.auto") }, ...IMPORT_PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))]}
                className="w-full"
              />
            </Field>
            <Field>
              <FieldLabel>{t("adminUserKeys.link.name")}</FieldLabel>
              <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
            </Field>
          </div>
          <div className="space-y-1.5">
            <div className="text-sm font-medium">{t("adminUserKeys.link.result")}</div>
            <div className="flex items-start gap-1 rounded-lg border bg-muted/40 p-2">
              <Mono className="min-w-0 flex-1 break-all">{link}</Mono>
              <CopyButton text={link} />
            </div>
            <p className="text-[13px] text-muted-foreground">{t("adminUserKeys.link.warning")}</p>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

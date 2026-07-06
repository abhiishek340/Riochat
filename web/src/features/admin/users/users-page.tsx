"use client";

import { FileDown, ImagePlus, KeyRound, LogOut, MoreHorizontal, Pencil, ShieldOff, Trash2, UserPlus } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useSession } from "@/lib/session/provider";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { SelectMenu } from "../components/menu-select";
import { AdminPage, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { StatusBadge } from "../components/status";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { runBulk } from "../lib/bulk";
import { formatDateTime, formatDisplayMoney } from "../lib/format";
import { listProviders } from "../login/api";
import type { IdentityProvider } from "../login/rules";
import { loadBillingConfig, loadPlans } from "../lib/reference";
import { useBulk } from "../lib/use-bulk";
import { useLabel } from "../lib/use-label";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import { DEFAULT_BILLING_CONFIG, type BillingConfig, type UserRecord } from "../lib/wire";
import { deleteUser, listUsers, patchUser, resetTwoFactor, revokeSessions, setBalance, type ImportReport } from "./api";
import { avatarSrc } from "./avatar";
import { ImportDialog, ResetPasswordDialog } from "./user-dialogs";
import { AvatarDialog, UserFormSheet } from "./user-form";
import { bulkReason, canManage, filterUsers, parseBalance, roleChoices, SETTABLE_STATUSES, sortUsers, STATUSES, SUBSCRIPTION_STATUSES, tierChoices, USER_SORTS, type UserSort } from "./rules";
import { timezoneList } from "./timezones";

type Confirm = { kind: "twoFactor" | "sessions" | "delete"; user: UserRecord } | { kind: "bulkDelete"; users: UserRecord[] } | null;

export function AdminUsersPage() {
  return (
    <AdminSection feature="multiUser">
      <UsersContent />
    </AdminSection>
  );
}

function UsersContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const message = useErrorMessage();
  const label = useLabel();
  const { user: viewer } = useSession();
  const viewerRole = viewer?.role;

  const [config, setConfig] = useState<BillingConfig>(DEFAULT_BILLING_CONFIG);
  const [planCodes, setPlanCodes] = useState<string[]>([]);
  const [planNames, setPlanNames] = useState<Record<string, string>>({});
  const [providers, setProviders] = useState<IdentityProvider[]>([]);
  const [q, setQ] = useState("");
  const [subStatus, setSubStatus] = useState("");
  const [provider, setProvider] = useState("");
  const [role, setRole] = useState("");
  const [status, setStatus] = useState("");
  const [tier, setTier] = useState("");
  const [sort, setSort] = useState<UserSort>("id_desc");
  const [form, setForm] = useState<UserRecord | "new" | null>(null);
  const [avatarFor, setAvatarFor] = useState<UserRecord | null>(null);
  const [resetFor, setResetFor] = useState<UserRecord | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [importing, setImporting] = useState(false);
  const selection = useSelection<number>();
  const mode = config.mode;

  useEffect(() => {
    void loadBillingConfig().then(setConfig, (error: unknown) => notify.error("adminCommon.toast.loadFailed", error));
    void loadPlans().then((plans) => {
      setPlanCodes(plans.map((p) => p.code));
      setPlanNames(Object.fromEntries(plans.map((p) => [p.code, p.name])));
    }, () => undefined);
    void listProviders().then(setProviders, () => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const filters = useMemo(() => ({ q, subscriptionStatus: subStatus, identityProvider: provider }), [q, subStatus, provider]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listUsers({ page, pageSize, ...f }, signal),
    onError: (error) => notify.error("adminCommon.toast.loadFailed", error),
  });

  const visible = useMemo(() => sortUsers(filterUsers(list.rows, { role, status, tier }), sort), [list.rows, role, status, tier, sort]);
  const manageable = useMemo(() => visible.filter((u) => canManage(viewerRole, u)), [visible, viewerRole]);
  const getKey = useCallback((u: UserRecord) => u.id, []);
  const rows = useRowUpdates<UserRecord, number>({ getKey, setRows: list.setRows });
  // Rows with an edit in flight leave the selection until the edit settles.
  useEffect(() => {
    selection.prune(manageable.filter((u) => !rows.pending.has(u.id)).map((u) => u.id));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [manageable, rows.pending]);
  const bulk = useBulk<UserRecord, number>({ getKey, setRows: list.setRows, selection });
  const selected = useMemo(() => list.rows.filter((u) => selection.has(u.id) && canManage(viewerRole, u)), [list.rows, selection, viewerRole]);

  const money = (usd: number) => formatDisplayMoney(usd, { currency: config.displayCurrency, rate: config.usdToCNYRate }, locale, 2);
  const nameOf = (u: UserRecord) => u.displayName || u.username;

  const inline = (u: UserRecord, field: "role" | "status", value: string) => {
    if (!canManage(viewerRole, u) || u[field] === value) return;
    void rows.update(u, (r) => ({ ...r, [field]: value }), () => patchUser(u.id, { [field]: value, reason: "inline_admin_table" }), { failKey: "adminUsers.toast.updateFailed" });
  };

  // --- bulk
  const [bulkRole, setBulkRole] = useState("user");
  const [bulkStatus, setBulkStatus] = useState("active");
  const [bulkZone, setBulkZone] = useState("Etc/UTC");
  const [bulkBalance, setBulkBalance] = useState("");
  const zones = useMemo(() => timezoneList().map((z) => ({ value: z, label: z })), []);

  const applyBulkBalance = () => {
    const parsed = parseBalance(bulkBalance);
    if (!parsed.ok) {
      toast.error(t("adminUsers.problems.balanceInvalid"));
      return;
    }
    void bulk.run({
      targets: selected,
      alreadyDone: (u) => Math.round(u.billingBalanceUSD * 1e6) / 1e6 === parsed.value,
      optimistic: (u) => ({ ...u, billingBalanceUSD: parsed.value }),
      request: async (u) => {
        const account = await setBalance(u.id, parsed.value, t("adminUsers.balanceNote"));
        return { ...u, billingBalanceUSD: account.balanceUSD, billingBalanceNanousd: account.balanceNanousd, billingAccountStatus: account.status };
      },
      successKey: "adminCommon.toast.bulkDone",
    });
  };

  const runBulkDelete = async (users: UserRecord[]) => {
    const outcome = await runBulk(users, (u) => deleteUser(u.id), { chunkSize: 10, sequential: true });
    const gone = new Set(outcome.succeeded.map((u) => u.id));
    list.setRows((r) => r.filter((u) => !gone.has(u.id)));
    list.setTotal((n) => n - gone.size);
    if (outcome.failed.length > 0) {
      selection.set(outcome.failed.map((f) => f.item.id));
      toast.error(t("adminCommon.toast.bulkPartial"), { description: `${t("adminCommon.toast.bulkPartialBody", { succeeded: gone.size, failed: outcome.failed.length })} ${message(outcome.failed[0]?.error)}` });
    } else {
      selection.clear();
      notify.success("adminUsers.toast.deletedMany", { count: gone.size });
    }
    if (gone.size > 0 && list.rows.length - gone.size <= 0 && list.page > 1) list.setPage(list.page - 1);
    else if (gone.size > 0) list.reload();
  };

  const columns: Column<UserRecord>[] = [
    {
      id: "user",
      sticky: "left",
      header: t("adminUsers.columns.user"),
      cell: (u) => (
        <div className="flex min-w-48 items-center gap-3">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={avatarSrc(u.avatarURL, u)} alt="" width={36} height={36} referrerPolicy="no-referrer" className="size-9 shrink-0 rounded-full border bg-muted object-cover" />
          <div className="min-w-0">
            <div className="truncate font-medium">{nameOf(u)}</div>
            <div className="truncate text-xs text-muted-foreground">{u.email || `@${u.username}`}</div>
          </div>
        </div>
      ),
    },
    {
      id: "role",
      header: t("adminUsers.columns.role"),
      cell: (u) =>
        canManage(viewerRole, u) ? (
          <CellActions>
            <OptionSelect
              value={u.role}
              disabled={rows.isPending(u.id)}
              label={t("adminUsers.columns.role")}
              onChange={(v) => inline(u, "role", v)}
              options={roleChoices(viewerRole, u.role).map((r) => ({ value: r, label: label("adminUsers.roles", r) }))}
              className="h-8 w-32 sm:min-w-0"
            />
          </CellActions>
        ) : (
          <Badge tone="outline">{label("adminUsers.roles", u.role)}</Badge>
        ),
    },
    {
      id: "status",
      header: t("adminUsers.columns.status"),
      cell: (u) =>
        canManage(viewerRole, u) && u.role !== "superadmin" ? (
          <CellActions>
            <OptionSelect
              value={u.status}
              disabled={rows.isPending(u.id)}
              label={t("adminUsers.columns.status")}
              onChange={(v) => inline(u, "status", v)}
              options={[...((SETTABLE_STATUSES as readonly string[]).includes(u.status) ? [] : [u.status]), ...SETTABLE_STATUSES].map((s) => ({ value: s, label: label("adminUsers.statuses", s) }))}
              className="h-8 w-36 sm:min-w-0"
            />
          </CellActions>
        ) : (
          <StatusBadge value={u.status} prefix="adminUsers.statuses" />
        ),
    },
    ...(mode === "period"
      ? [
          {
            id: "subscription",
            header: t("adminUsers.columns.subscription"),
            cell: (u: UserRecord) => (
              <div className="space-y-0.5 text-[13px]">
                <div className="font-medium">{u.subscriptionPlanName || planNames[u.subscriptionTier] || u.subscriptionTier || "free"}</div>
                <div className="text-muted-foreground">
                  {u.subscriptionStatus ? label("adminUsers.subscriptionStatuses", u.subscriptionStatus) : ""}
                  {u.subscriptionExpiresAt ? ` · ${formatDateTime(u.subscriptionExpiresAt, locale)}` : ""}
                </div>
              </div>
            ),
          } satisfies Column<UserRecord>,
        ]
      : []),
    ...(mode !== "self"
      ? [
          {
            id: "balance",
            header: t("adminUsers.columns.balance"),
            className: "whitespace-nowrap tabular-nums",
            cell: (u: UserRecord) => (
              <div>
                <div>{money(u.billingBalanceUSD)}</div>
                {u.billingAccountStatus && u.billingAccountStatus !== "active" ? <div className="text-xs text-warning-foreground dark:text-warning">{label("adminUsers.accountStatuses", u.billingAccountStatus)}</div> : null}
              </div>
            ),
          } satisfies Column<UserRecord>,
        ]
      : []),
    { id: "timezone", header: t("adminUsers.columns.timezone"), className: "whitespace-nowrap text-muted-foreground", cell: (u) => u.timezone || "-" },
    { id: "active", header: t("adminUsers.columns.lastActive"), className: "whitespace-nowrap text-muted-foreground", cell: (u) => formatDateTime(u.lastActiveAt || u.lastLoginAt, locale) },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (u) => {
        const ok = canManage(viewerRole, u);
        return (
          <CellActions>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" disabled={!ok} aria-label={`${t("adminCommon.table.moreActions")}: ${nameOf(u)}`}>
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem onSelect={() => setForm(u)}>
                  <Pencil />
                  {t("common.actions.edit")}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setAvatarFor(u)}>
                  <ImagePlus />
                  {t("adminUsers.actions.avatar")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setResetFor(u)}>
                  <KeyRound />
                  {t("adminUsers.actions.resetPassword")}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "twoFactor", user: u })}>
                  <ShieldOff />
                  {t("adminUsers.actions.resetTwoFactor")}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "sessions", user: u })}>
                  <LogOut />
                  {t("adminUsers.actions.revokeSessions")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem destructive onSelect={() => setConfirm({ kind: "delete", user: u })}>
                  <Trash2 />
                  {t("common.actions.delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </CellActions>
        );
      },
    },
  ];

  const confirmCopy = (() => {
    if (!confirm) return null;
    if (confirm.kind === "bulkDelete") return { title: t("adminUsers.confirm.deleteManyTitle", { count: confirm.users.length }), body: t("adminUsers.confirm.deleteBody"), action: t("common.actions.delete"), destructive: true };
    const name = nameOf(confirm.user);
    if (confirm.kind === "delete") return { title: t("adminUsers.confirm.deleteTitle", { name }), body: t("adminUsers.confirm.deleteBody"), action: t("common.actions.delete"), destructive: true };
    if (confirm.kind === "twoFactor") return { title: t("adminUsers.confirm.twoFactorTitle", { name }), body: t("adminUsers.confirm.twoFactorBody"), action: t("adminUsers.actions.resetTwoFactor"), destructive: true };
    return { title: t("adminUsers.confirm.sessionsTitle", { name }), body: t("adminUsers.confirm.sessionsBody"), action: t("adminUsers.actions.revokeSessions"), destructive: true };
  })();

  const runConfirm = async () => {
    if (!confirm) return;
    try {
      if (confirm.kind === "bulkDelete") {
        await runBulkDelete(confirm.users);
        return;
      }
      const u = confirm.user;
      if (confirm.kind === "delete") {
        const snapshot = list.rows;
        list.setRows((r) => r.filter((x) => x.id !== u.id));
        list.setTotal((n) => n - 1);
        try {
          await deleteUser(u.id);
        } catch (error) {
          list.setRows(() => snapshot);
          list.setTotal((n) => n + 1);
          throw error;
        }
        notify.success("adminUsers.toast.deleted");
        if (snapshot.length <= 1 && list.page > 1) list.setPage(list.page - 1);
      } else if (confirm.kind === "twoFactor") {
        await resetTwoFactor(u.id);
        list.setRows((r) => r.map((x) => (x.id === u.id ? { ...x, twoFactorEnabled: false } : x)));
        notify.success("adminUsers.toast.twoFactorReset");
      } else {
        await revokeSessions(u.id);
        notify.success("adminUsers.toast.sessionsRevoked");
      }
    } catch (error) {
      notify.error("adminUsers.toast.actionFailed", error);
      throw error;
    }
  };

  const onImported = (report: ImportReport) => {
    notify.success("adminUsers.toast.imported", { imported: report.imported, skipped: report.skippedExistingEmail + report.skippedDuplicateSourceEmail + report.skippedInvalidEmail + report.skippedInvalidRow });
    if (list.page !== 1) list.setPage(1);
    else list.reload();
  };

  return (
    <AdminPage
      title={t("adminUsers.title")}
      description={t("adminUsers.description")}
      actions={
        <>
          <Button variant="outline" onClick={() => setImporting(true)}>
            <FileDown />
            {t("adminUsers.import.open")}
          </Button>
          <Button onClick={() => setForm("new")}>
            <UserPlus />
            {t("adminUsers.create")}
          </Button>
        </>
      }
    >
      <Section flush>
        <div className="space-y-3 p-4">
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminUsers.filters.search")} className="sm:w-72" />
            <OptionSelect value={subStatus} onChange={setSubStatus} label={t("adminUsers.filters.subscriptionStatus")} options={[{ value: "", label: t("adminUsers.filters.anySubscription") }, ...SUBSCRIPTION_STATUSES.map((s) => ({ value: s, label: label("adminUsers.subscriptionStatuses", s) }))]} />
            {providers.length > 0 ? <OptionSelect value={provider} onChange={setProvider} label={t("adminUsers.filters.provider")} options={[{ value: "", label: t("adminUsers.filters.anyProvider") }, ...providers.map((p) => ({ value: p.slug, label: p.name }))]} /> : null}
            <OptionSelect value={role} onChange={setRole} label={t("adminUsers.columns.role")} options={[{ value: "", label: t("adminUsers.filters.anyRole") }, ...roleChoices("superadmin", "user").map((r) => ({ value: r, label: label("adminUsers.roles", r) }))]} />
            <OptionSelect value={status} onChange={setStatus} label={t("adminUsers.columns.status")} options={[{ value: "", label: t("adminUsers.filters.anyStatus") }, ...STATUSES.map((s) => ({ value: s, label: label("adminUsers.statuses", s) }))]} />
            {mode === "period" ? <OptionSelect value={tier} onChange={setTier} label={t("adminUsers.form.tier")} options={[{ value: "", label: t("adminUsers.filters.anyTier") }, ...tierChoices(planCodes).map((c) => ({ value: c, label: planNames[c] ?? c }))]} /> : null}
            <OptionSelect value={sort} onChange={(v) => setSort(v as UserSort)} label={t("adminUsers.filters.sort")} options={USER_SORTS.map((s) => ({ value: s, label: t(`adminUsers.sorts.${s}`) }))} />
          </Toolbar>

          <BulkBar count={selected.length} onClear={selection.clear}>
            <span className="flex items-center gap-1">
              <OptionSelect value={bulkRole} onChange={setBulkRole} label={t("adminUsers.columns.role")} options={roleChoices(viewerRole, "user").map((r) => ({ value: r, label: label("adminUsers.roles", r) }))} className="h-8 w-32 sm:min-w-0" />
              <Button
                size="sm"
                variant="outline"
                disabled={bulk.pending}
                onClick={() =>
                  void bulk.run({
                    targets: selected,
                    alreadyDone: (u) => u.role === bulkRole,
                    optimistic: (u) => ({ ...u, role: bulkRole }),
                    request: (u) => patchUser(u.id, { role: bulkRole, reason: bulkReason("role") }),
                    successKey: "adminCommon.toast.bulkDone",
                  })
                }
              >
                {t("adminUsers.bulk.applyRole")}
              </Button>
            </span>
            <span className="flex items-center gap-1">
              <OptionSelect value={bulkStatus} onChange={setBulkStatus} label={t("adminUsers.columns.status")} options={SETTABLE_STATUSES.map((s) => ({ value: s, label: label("adminUsers.statuses", s) }))} className="h-8 w-36 sm:min-w-0" />
              <Button
                size="sm"
                variant="outline"
                disabled={bulk.pending}
                onClick={() =>
                  void bulk.run({
                    targets: selected,
                    alreadyDone: (u) => u.status === bulkStatus,
                    optimistic: (u) => ({ ...u, status: bulkStatus }),
                    request: (u) => patchUser(u.id, { status: bulkStatus, reason: bulkReason("status") }),
                    successKey: "adminCommon.toast.bulkDone",
                  })
                }
              >
                {t("adminUsers.bulk.applyStatus")}
              </Button>
            </span>
            <span className="flex items-center gap-1">
              <SelectMenu value={bulkZone} onChange={setBulkZone} options={zones} searchable className="h-8 w-44" label={t("adminUsers.form.timezone")} />
              <Button
                size="sm"
                variant="outline"
                disabled={bulk.pending}
                onClick={() =>
                  void bulk.run({
                    targets: selected,
                    alreadyDone: (u) => (u.timezone || "Etc/UTC") === bulkZone,
                    optimistic: (u) => ({ ...u, timezone: bulkZone }),
                    request: (u) => patchUser(u.id, { timezone: bulkZone }),
                    successKey: "adminCommon.toast.bulkDone",
                  })
                }
              >
                {t("adminUsers.bulk.applyTimezone")}
              </Button>
            </span>
            {mode !== "self" ? (
              <span className="flex items-center gap-1">
                <Input inputMode="decimal" value={bulkBalance} onChange={(e) => setBulkBalance(e.target.value)} placeholder={t("adminUsers.form.balance")} aria-label={t("adminUsers.form.balance")} className="h-8 w-28" />
                <Button size="sm" variant="outline" disabled={bulk.pending} onClick={applyBulkBalance}>
                  {t("adminUsers.bulk.applyBalance")}
                </Button>
              </span>
            ) : null}
            <Button size="sm" variant="destructive" disabled={bulk.pending} onClick={() => setConfirm({ kind: "bulkDelete", users: selected })}>
              {t("common.actions.delete")}
            </Button>
          </BulkBar>

          <DataTable
            columns={columns}
            rows={visible}
            rowKey={getKey}
            loading={list.loading} error={list.error} onRetry={list.reload}
            fetching={list.fetching}
            selection={selection}
            selectable={(u) => canManage(viewerRole, u) && !rows.isPending(u.id)}
            empty={t("adminCommon.table.noResults")}
            rowLabel={(u) => u.displayName || u.username} label={t("adminUsers.title")}
          />
          <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
        </div>
      </Section>

      <UserFormSheet
        target={form}
        mode={mode}
        viewerRole={viewerRole}
        planCodes={planCodes}
        onOpenChange={(open) => !open && setForm(null)}
        onPartial={(user) => list.setRows((r) => r.map((x) => (x.id === user.id ? user : x)))}
        onSaved={(user, created) => {
          if (created) {
            notify.success("adminUsers.toast.created");
            if (list.page !== 1) list.setPage(1);
            else list.reload();
          } else {
            notify.success("adminUsers.toast.updated");
            list.setRows((r) => r.map((x) => (x.id === user.id ? user : x)));
          }
        }}
      />
      <AvatarDialog
        open={avatarFor !== null}
        value={avatarFor?.avatarURL ?? ""}
        seed={avatarFor ?? {}}
        onOpenChange={(open) => !open && setAvatarFor(null)}
        onSave={async (value) => {
          const u = avatarFor;
          if (!u || value === u.avatarURL) return;
          try {
            const saved = await patchUser(u.id, { avatarURL: value, reason: "admin_update_avatar" });
            list.setRows((r) => r.map((x) => (x.id === saved.id ? saved : x)));
            notify.success("adminUsers.toast.updated");
          } catch (error) {
            notify.error("adminUsers.toast.updateFailed", error);
            throw error;
          }
        }}
      />
      <ResetPasswordDialog user={resetFor ? { id: resetFor.id, name: nameOf(resetFor) } : null} onOpenChange={(open) => !open && setResetFor(null)} />
      <ImportDialog open={importing} onOpenChange={setImporting} onImported={onImported} />
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={confirmCopy?.title ?? ""}
        description={confirmCopy?.body}
        confirmLabel={confirmCopy?.action}
        destructive={confirmCopy?.destructive}
        onConfirm={runConfirm}
      />
    </AdminPage>
  );
}

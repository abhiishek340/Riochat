"use client";

import { Link2, MoreHorizontal, Plus } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Table, TableBody, TableCell, TableEmptyRow, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { codeToKey } from "@/lib/i18n/errors";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { LoadErrorNotice, SettingsPage } from "../parts";
import { useLoad } from "../lib/use-load";
import { deleteProvider, fetchProviderModels, listProviders, updateProvider, type PersonalProvider } from "./api";
import { iconFor } from "./icons";
import { ImportLinkDialog } from "./import-link-dialog";
import { protocolLabel } from "./protocols";
import { AddProviderDialog, EditProviderDialog } from "./provider-dialogs";
import { useProviderAccess } from "./use-provider-access";
import { recognizeProvider } from "./well-known";

/** Tooltip triggers must take keyboard focus, or their text is out of reach. */
const FOCUSABLE = "rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring";

/** Enabled model names grouped by protocol label, for the count's tooltip. */
function ModelsTooltip({ provider }: { provider: PersonalProvider }) {
  const groups = new Map<string, string[]>();
  for (const m of provider.models) {
    const label = m.protocols.map(protocolLabel).join(" + ") || protocolLabel(provider.protocol);
    groups.set(label, [...(groups.get(label) ?? []), m.name]);
  }
  return (
    <div className="max-h-56 max-w-72 overflow-y-auto text-left">
      {[...groups].map(([label, names]) => (
        <div key={label} className="mb-1.5 last:mb-0">
          <div className="text-[11px] opacity-70">{label}</div>
          <div className="font-mono text-xs leading-snug">{names.join(", ")}</div>
        </div>
      ))}
    </div>
  );
}

export function ModelsSettingsPage() {
  const t = useT();
  const { lookup } = useI18n();
  const errorMessage = useErrorMessage();
  const router = useRouter();
  usePageTitle(t("settings.title"));
  const { access, loaded } = useProviderAccess();
  const enabled = loaded && access?.enabled === true;

  useEffect(() => {
    if (loaded && !enabled) router.replace("/settings/general");
  }, [loaded, enabled, router]);

  const list = useLoad((signal) => listProviders(signal), "providers", enabled);
  const [override, setOverride] = useState<PersonalProvider[] | null>(null);
  const providers = override ?? list.data ?? [];
  const loadedRef = useRef<PersonalProvider[]>([]);
  loadedRef.current = list.data ?? [];
  useEffect(() => {
    if (list.data) setOverride(null);
  }, [list.data]);
  // Row actions finish at different times: each one edits the latest list, not
  // the list that was on screen when it started.
  const edit = (change: (current: PersonalProvider[]) => PersonalProvider[]) => setOverride((prev) => change(prev ?? loadedRef.current));
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [addOpen, setAddOpen] = useState(false);
  const [linkOpen, setLinkOpen] = useState(false);
  const [editing, setEditing] = useState<PersonalProvider | null>(null);
  const [removing, setRemoving] = useState<PersonalProvider | null>(null);

  useEffect(() => {
    if (list.error) toast.error(t("settings.models.toasts.loadFailed"), { description: errorMessage(list.error) });
    // Report each failure once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.error]);

  const max = access?.maxPerUser ?? 0;
  const atLimit = max > 0 && providers.length >= max;
  const listLoading = list.loading && !list.data;
  const listFailed = Boolean(list.error) && !list.data && !override;

  const replace = (next: PersonalProvider) => edit((current) => current.map((p) => (p.id === next.id ? next : p)));
  const mark = (id: string, on: boolean) =>
    setBusy((current) => {
      const next = new Set(current);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const toggleEnabled = async (p: PersonalProvider) => {
    const turnOn = p.status === "disabled";
    mark(p.id, true);
    try {
      replace(await updateProvider(p.id, { enabled: turnOn }));
      toast.success(turnOn ? t("settings.models.toasts.enabled") : t("settings.models.toasts.disabled"));
    } catch (e) {
      toast.error(t("settings.models.toasts.saveFailed"), { description: errorMessage(e) });
    } finally {
      mark(p.id, false);
    }
  };

  const check = async (p: PersonalProvider) => {
    mark(p.id, true);
    try {
      const models = await fetchProviderModels(p.id, p.protocol);
      toast.success(t("settings.models.toasts.checkPassed"), { description: t("settings.models.toasts.checkCount", { count: models.length }) });
    } catch (e) {
      toast.error(t("settings.models.toasts.checkFailed"), { description: errorMessage(e) });
    } finally {
      mark(p.id, false);
      // The server records the check result on the row either way.
      list.reload();
    }
  };

  const remove = async (p: PersonalProvider) => {
    await deleteProvider(p.id);
    edit((current) => current.filter((x) => x.id !== p.id));
    toast.success(t("settings.models.toasts.deleted"));
  };

  const errorText = (raw: string): string => lookup(codeToKey(raw)) ?? raw;

  const statusBadge = (p: PersonalProvider) => {
    if (p.status === "suspended")
      return (
        <Tooltip label={t("settings.models.status.suspendedHint")}>
          <Badge tone="danger" tabIndex={0} className={FOCUSABLE}>
            {t("settings.models.status.suspended")}
          </Badge>
        </Tooltip>
      );
    if (p.status === "disabled") return <Badge tone="neutral">{t("settings.models.status.disabled")}</Badge>;
    if (p.lastError)
      return (
        <Tooltip label={errorText(p.lastError)}>
          <Badge tone="warning" tabIndex={0} className={FOCUSABLE}>
            {t("settings.models.status.error")}
          </Badge>
        </Tooltip>
      );
    return <Badge tone="success">{t("settings.models.status.active")}</Badge>;
  };

  if (!enabled || !access) return null;

  const addDisabled = listLoading || listFailed || atLimit;
  const actions = (
    <>
      <Button variant="outline" size="sm" onClick={() => setLinkOpen(true)}>
        <Link2 />
        {t("settings.models.importLink")}
      </Button>
      <Tooltip label={atLimit ? t("settings.models.limitReached", { max }) : ""}>
        {/* A disabled button cannot take focus: at the limit the wrapper does, so the reason is reachable by keyboard. */}
        <span
          role={atLimit ? "group" : undefined}
          tabIndex={atLimit ? 0 : undefined}
          aria-label={atLimit ? t("settings.models.limitReached", { max }) : undefined}
          className="inline-flex rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Button size="sm" disabled={addDisabled} onClick={() => setAddOpen(true)}>
            <Plus />
            {t("settings.models.add")}
          </Button>
        </span>
      </Tooltip>
    </>
  );

  return (
    <SettingsPage title={t("settings.nav.models")} description={t("settings.models.description")} actions={actions}>
      <div className="grid gap-2">
        {listFailed ? <LoadErrorNotice title={t("settings.models.toasts.loadFailed")} error={list.error} onRetry={list.reload} /> : null}
        <Table aria-label={t("settings.nav.models")}>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>{t("settings.models.columns.name")}</TableHead>
              <TableHead>{t("settings.models.columns.baseURL")}</TableHead>
              <TableHead className="text-right">{t("settings.models.columns.models")}</TableHead>
              <TableHead>{t("settings.models.columns.status")}</TableHead>
              <TableHead className="w-12">
                <span className="sr-only">{t("settings.models.columns.actions")}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {listLoading ? (
              <TableEmptyRow colSpan={5}>{t("common.states.loading")}</TableEmptyRow>
            ) : listFailed ? (
              <TableEmptyRow colSpan={5}>—</TableEmptyRow>
            ) : providers.length === 0 ? (
              <TableEmptyRow colSpan={5}>{t("settings.models.empty")}</TableEmptyRow>
            ) : (
              providers.map((p) => {
                const Icon = iconFor(p.icon, recognizeProvider(p.baseURL)?.icon);
                const working = busy.has(p.id);
                const suspended = p.status === "suspended";
                return (
                  <TableRow key={p.id} tone={p.status === "active" ? undefined : "muted"}>
                    <TableCell>
                      <span className="flex items-center gap-2.5 font-medium">
                        <Icon className="size-4 shrink-0 text-muted-foreground" />
                        <span className="truncate">{p.name || p.host}</span>
                      </span>
                    </TableCell>
                    <TableCell className="max-w-64 truncate font-mono text-[13px]">{p.baseURL}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {p.models.length > 0 ? (
                        <Tooltip label={<ModelsTooltip provider={p} />} side="left">
                          <span tabIndex={0} className={`cursor-default underline decoration-dotted underline-offset-4 ${FOCUSABLE}`}>
                            {p.models.length}
                          </span>
                        </Tooltip>
                      ) : (
                        "0"
                      )}
                    </TableCell>
                    <TableCell>{statusBadge(p)}</TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm" disabled={working} aria-label={t("settings.models.rowActions", { name: p.name || p.host })}>
                            <MoreHorizontal />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent>
                          <DropdownMenuItem disabled={suspended} onSelect={() => setEditing(p)}>
                            {t("common.actions.edit")}
                          </DropdownMenuItem>
                          <DropdownMenuItem disabled={suspended} onSelect={() => void check(p)}>
                            {t("settings.models.actions.check")}
                          </DropdownMenuItem>
                          <DropdownMenuItem disabled={suspended} onSelect={() => void toggleEnabled(p)}>
                            {p.status === "disabled" ? t("settings.models.actions.enable") : t("settings.models.actions.disable")}
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem destructive onSelect={() => setRemoving(p)}>
                            {t("common.actions.delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
        <p className="text-[13px] text-muted-foreground">{t("settings.models.footnote", { max })}</p>
      </div>

      <AddProviderDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        access={access}
        source="manual"
        onCreated={(p) => {
          edit((current) => [...current, p]);
          toast.success(t("settings.models.toasts.added"));
        }}
      />
      <EditProviderDialog provider={editing} onOpenChange={(open) => !open && setEditing(null)} access={access} onSaved={replace} />
      <ImportLinkDialog open={linkOpen} onOpenChange={setLinkOpen} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        destructive
        title={t("settings.models.deleteTitle")}
        description={t("settings.models.deleteBody", { name: removing?.name || removing?.host || "" })}
        confirmLabel={t("common.actions.delete")}
        onConfirm={async () => {
          if (removing) await remove(removing);
        }}
      />
    </SettingsPage>
  );
}

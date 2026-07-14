"use client";

import { Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { toast } from "@/components/ui/toast-store";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { AdminPage, Section } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { deleteGroup } from "../llm-api";
import { invalidateModels, loadPermissionGroups, invalidateReference } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import type { PermissionGroup } from "../lib/wire";
import { GroupSheet } from "./group-sheet";
import { factorFromPercent } from "./rules";

export function AdminGroupsPage() {
  return (
    <AdminSection feature="multiUser">
      <GroupsContent />
    </AdminSection>
  );
}

function GroupsContent() {
  const t = useT();
  const notify = useNotify();
  const [groups, setGroups] = useState<PermissionGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);
  const [editing, setEditing] = useState<PermissionGroup | "new" | null>(null);
  const [removing, setRemoving] = useState<PermissionGroup | null>(null);

  // The first load shows placeholders; later reloads (after a save or delete) are silent.
  useEffect(() => {
    let live = true;
    invalidateReference("groups");
    loadPermissionGroups().then(
      (rows) => {
        if (!live) return;
        setGroups(rows);
        setFailed(false);
        setLoading(false);
      },
      (error: unknown) => {
        if (!live || isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminGroups.toast.loadFailed", error);
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce]);

  const columns: Column<PermissionGroup>[] = [
    {
      id: "name",
      sticky: "left",
      header: t("adminGroups.columns.name"),
      cell: (g) => (
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">{g.name}</span>
            {g.isDefault ? <Badge tone="primary">{t("adminGroups.default")}</Badge> : null}
          </div>
          {g.description ? <div className="truncate text-xs text-muted-foreground">{g.description}</div> : null}
        </div>
      ),
    },
    { id: "multiplier", header: t("adminGroups.columns.multiplier"), className: "tabular-nums", cell: (g) => `${factorFromPercent(g.rateMultiplierPercent)}x` },
    {
      id: "models",
      header: t("adminGroups.columns.models"),
      className: "tabular-nums",
      cell: (g) => (
        <Tooltip label={t("adminGroups.breakdown.models", { manual: g.manualModelCount, rule: g.ruleModelCount })}>
          <span>{g.modelCount}</span>
        </Tooltip>
      ),
    },
    {
      id: "users",
      header: t("adminGroups.columns.users"),
      className: "tabular-nums",
      cell: (g) =>
        g.isDefault ? (
          <span className="text-muted-foreground">{t("adminGroups.everyone")}</span>
        ) : (
          <Tooltip label={t("adminGroups.breakdown.users", { manual: g.manualUserCount, subscription: g.subscriptionUserCount })}>
            <span>{g.userCount}</span>
          </Tooltip>
        ),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (g) => (
        <CellActions>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${g.name}`} onClick={() => setEditing(g)}>
              <Pencil />
            </Button>
          </Tooltip>
          <Tooltip label={g.isDefault ? t("adminGroups.cannotDeleteDefault") : t("common.actions.delete")}>
            <span>
              <Button variant="ghost" size="icon-sm" disabled={g.isDefault} aria-label={`${t("common.actions.delete")}: ${g.name}`} onClick={() => setRemoving(g)}>
                <Trash2 className={g.isDefault ? undefined : "text-destructive"} />
              </Button>
            </span>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  return (
    <AdminPage
      title={t("adminGroups.title")}
      description={t("adminGroups.description")}
      actions={
        <Button onClick={() => setEditing("new")}>
          <Plus />
          {t("adminGroups.create")}
        </Button>
      }
    >
      <Section flush>
        <div className="p-4">
          <DataTable columns={columns} rows={groups} rowKey={(g) => g.id} loading={loading} error={failed} onRetry={() => setNonce((n) => n + 1)} rowLabel={(g) => g.name} onRowClick={setEditing} empty={t("adminGroups.empty")} label={t("adminGroups.title")} />
        </div>
      </Section>
      <GroupSheet target={editing} onOpenChange={(open) => !open && setEditing(null)} onChanged={(reload) => reload && setNonce((n) => n + 1)} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminGroups.confirm.deleteTitle", { name: removing?.name ?? "" })}
        description={t("adminGroups.confirm.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            const summary = await deleteGroup(removing.id);
            invalidateModels();
            setGroups((list) => list.filter((g) => g.id !== removing.id));
            toast.success(t("adminGroups.toast.deleted"), { description: t("adminGroups.toast.deletedBody", { models: summary.manualModelCount, rules: summary.ruleCount, users: summary.manualUserCount }) });
          } catch (error) {
            notify.error("adminGroups.toast.deleteFailed", error);
            throw error;
          }
        }}
      />
    </AdminPage>
  );
}

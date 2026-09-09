"use client";

import { Pencil, Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { toast } from "@/components/ui/toast-store";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { MarkdownPreview } from "../components/markdown-preview";
import { AdminPage, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { Sheet } from "../components/sheet";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { formatDateTime } from "../lib/format";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSnapshot } from "../lib/use-snapshot";
import { createAnnouncement, deleteAnnouncement, listAnnouncements, updateAnnouncement } from "./api";
import { ANNOUNCEMENT_TYPES, bodyOf, EMPTY_FORM, formOf, isCurrentlyVisible, normalizeType, parsePriorityInput, type Announcement, type AnnouncementForm } from "./rules";

const TYPE_TONE = { critical: "danger", warning: "warning", info: "primary", normal: "success", general: "neutral" } as const;

export function AdminAnnouncementsPage() {
  return (
    <AdminSection feature="announcements">
      <AnnouncementsContent />
    </AdminSection>
  );
}

function AnnouncementsContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [pinned, setPinned] = useState("");
  const [editing, setEditing] = useState<Announcement | "new" | null>(null);
  const [removing, setRemoving] = useState<Announcement | null>(null);
  const filters = useMemo(() => ({ q, status, type, pinned }), [q, status, type, pinned]);

  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listAnnouncements({ page, pageSize, ...f }, signal),
    onError: (error) => notify.error("adminAnnounce.toast.loadFailed", error),
  });

  const getKey = useCallback((a: Announcement) => a.id, []);
  const rows = useRowUpdates<Announcement, number>({ getKey, setRows: list.setRows });

  const patch = (row: Announcement, change: Partial<Announcement>, body: Parameters<typeof updateAnnouncement>[1], failKey: "adminAnnounce.toast.statusFailed" | "adminAnnounce.toast.updateFailed") =>
    rows.update(row, (r) => ({ ...r, ...change }), () => updateAnnouncement(row.id, body), { failKey });

  const columns: Column<Announcement>[] = [
    {
      id: "title",
      header: t("adminAnnounce.columns.title"),
      sticky: "left",
      className: "min-w-56 max-w-[22rem]",
      cell: (a) => (
        <div className="min-w-0 space-y-1">
          <div className="flex items-center gap-2">
            <Badge tone={TYPE_TONE[normalizeType(a.type)]}>{t(`adminAnnounce.types.${normalizeType(a.type)}`)}</Badge>
            <span className="truncate font-medium">{a.title}</span>
          </div>
          <OptionSelect
            value={normalizeType(a.type)}
            label={`${t("adminAnnounce.columns.type")}: ${a.title}`}
            className="h-7 w-32 text-xs sm:min-w-0"
            options={ANNOUNCEMENT_TYPES.map((x) => ({ value: x, label: t(`adminAnnounce.types.${x}`) }))}
            onChange={(next) => {
              if (next === normalizeType(a.type)) return;
              void patch(a, { type: next }, { type: next }, "adminAnnounce.toast.updateFailed");
            }}
          />
        </div>
      ),
    },
    {
      id: "pinned",
      header: t("adminAnnounce.columns.pinned"),
      cell: (a) => <Switch checked={a.pinned} disabled={rows.isPending(a.id)} aria-label={`${t("adminAnnounce.columns.pinned")}: ${a.title}`} onCheckedChange={(on) => void patch(a, { pinned: on }, { pinned: on }, "adminAnnounce.toast.updateFailed")} />,
    },
    {
      id: "status",
      header: t("adminAnnounce.columns.status"),
      cell: (a) => (
        <Switch
          checked={a.status === "active"}
          disabled={rows.isPending(a.id)}
          aria-label={`${t("adminAnnounce.columns.status")}: ${a.title}`}
          onCheckedChange={(on) => void patch(a, { status: on ? "active" : "inactive" }, { status: on ? "active" : "inactive" }, "adminAnnounce.toast.statusFailed")}
        />
      ),
    },
    {
      id: "priority",
      header: t("adminAnnounce.columns.priority"),
      cell: (a) => (
        <PriorityCell
          value={a.priority}
          disabled={rows.isPending(a.id)}
          label={`${t("adminAnnounce.columns.priority")}: ${a.title}`}
          onCommit={(next) => void patch(a, { priority: next }, { priority: next }, "adminAnnounce.toast.updateFailed")}
          onInvalid={() => toast.error(t("adminAnnounce.toast.invalidPriority"))}
        />
      ),
    },
    {
      id: "window",
      header: t("adminAnnounce.columns.window"),
      className: "whitespace-nowrap text-[13px] text-muted-foreground",
      cell: (a) =>
        a.startsAt || a.expiresAt
          ? t("adminAnnounce.window.range", {
              start: a.startsAt ? formatDateTime(a.startsAt, locale) : t("adminAnnounce.window.open"),
              end: a.expiresAt ? formatDateTime(a.expiresAt, locale) : t("adminAnnounce.window.open"),
            })
          : "-",
    },
    {
      id: "visibility",
      header: t("adminAnnounce.columns.visibility"),
      cell: (a) => (isCurrentlyVisible(a) ? <Badge tone="success">{t("adminAnnounce.visibility.visible")}</Badge> : <Badge tone="neutral">{t("adminAnnounce.visibility.hidden")}</Badge>),
    },
    { id: "updated", header: t("adminAnnounce.columns.updated"), className: "whitespace-nowrap text-muted-foreground", cell: (a) => formatDateTime(a.updatedAt, locale) },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (a) => (
        <CellActions>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${a.title}`} onClick={() => setEditing(a)}>
              <Pencil />
            </Button>
          </Tooltip>
          <Tooltip label={t("common.actions.delete")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${a.title}`} onClick={() => setRemoving(a)}>
              <Trash2 className="text-destructive" />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  return (
    <AdminPage
      title={t("adminAnnounce.title")}
      description={t("adminAnnounce.description")}
      actions={
        <Button onClick={() => setEditing("new")}>
          <Plus />
          {t("adminAnnounce.create")}
        </Button>
      }
    >
      <Section flush>
        <div className="space-y-3 p-4">
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminAnnounce.filters.search")} className="sm:w-72" />
            <OptionSelect
              value={status}
              onChange={setStatus}
              label={t("adminCommon.table.status")}
              options={[
                { value: "", label: t("adminAnnounce.filters.allStatuses") },
                { value: "active", label: t("adminAnnounce.status.active") },
                { value: "inactive", label: t("adminAnnounce.status.inactive") },
              ]}
            />
            <OptionSelect value={type} onChange={setType} label={t("adminAnnounce.columns.type")} options={[{ value: "", label: t("adminAnnounce.filters.allTypes") }, ...ANNOUNCEMENT_TYPES.map((x) => ({ value: x, label: t(`adminAnnounce.types.${x}`) }))]} />
            <OptionSelect
              value={pinned}
              onChange={setPinned}
              label={t("adminAnnounce.columns.pinned")}
              options={[
                { value: "", label: t("adminAnnounce.filters.allPinned") },
                { value: "true", label: t("adminAnnounce.filters.pinned") },
                { value: "false", label: t("adminAnnounce.filters.unpinned") },
              ]}
            />
          </Toolbar>
          <DataTable columns={columns} rows={list.rows} rowKey={getKey} loading={list.loading} error={list.error} onRetry={list.reload} fetching={list.fetching} empty={t("adminAnnounce.empty")} label={t("adminAnnounce.title")} />
          <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
        </div>
      </Section>

      <EditorSheet
        target={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={(saved, created) => {
          if (created) {
            list.setRows((r) => [saved, ...r].slice(0, list.pageSize));
            list.setTotal((n) => n + 1);
          } else {
            list.setRows((r) => r.map((x) => (x.id === saved.id ? saved : x)));
          }
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminAnnounce.confirm.deleteTitle", { title: removing?.title ?? "" })}
        description={t("adminAnnounce.confirm.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            await deleteAnnouncement(removing.id);
          } catch (error) {
            notify.error("adminAnnounce.toast.deleteFailed", error);
            throw error;
          }
          list.setRows((r) => r.filter((x) => x.id !== removing.id));
          list.setTotal((n) => n - 1);
          notify.success("adminAnnounce.toast.deleted");
        }}
      />
    </AdminPage>
  );
}

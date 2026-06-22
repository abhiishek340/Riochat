"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { MessagesSquare, Plus, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { HeaderActions } from "@/components/shell/header-context";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useI18n } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { announceConversationChange } from "../lib/conversation-events";
import { downloadBlob, fileStem } from "../lib/download";
import { normalizeQuery } from "../lib/search-model";
import { NEW_CHAT_EVENT } from "../lib/shortcuts";
import {
  exportAllJsonl,
  exportConversationJson,
  fetchDeleteFilesDefault,
  fetchProjects,
  moveManyToProject,
  moveToProject,
  deleteConversation,
  revokeShares,
  setArchived,
  setStarred,
  type ProjectRef,
  type ShareInfo,
} from "./api";
import { ARCHIVE_CHUNK, DELETE_CHUNK, processInChunks, REVOKE_CHUNK } from "./bulk";
import { ConversationItem, type RowActions } from "./conversation-item";
import { DeleteDialog, LabelsDialog, RenameDialog, ShareDialog } from "./dialogs";
import {
  archiveActionFor,
  chunk,
  DEFAULT_FILTERS,
  exportFileName,
  filterWords,
  groupByProject,
  isArchived,
  isShared,
  masterState,
  readExportManifest,
  rowsNeedingChange,
  type ConversationRow,
  type Filters,
  type ShareFilter,
  type StarFilter,
  type StatusFilter,
} from "./model";
import { IdleTools, MasterCheckbox, Segmented, SelectionBar } from "./toolbar";
import { useConversationList } from "./use-conversation-list";

const BULK_TOAST = "recent-bulk";

type Dialog =
  | { kind: "none" }
  | { kind: "rename" | "labels" | "share" | "delete"; row: ConversationRow }
  | { kind: "bulk-archive" | "bulk-delete" | "bulk-share" };

/** `/recent`: every conversation, with search, filters, per-row actions and multi-select bulk actions. */
export function RecentPage() {
  const { t } = useI18n();
  const errorMessage = useErrorMessage();
  const router = useRouter();
  const params = useSearchParams();
  const { flags } = useCapabilities();
  const sharingEnabled = flags.sharing;

  const [text, setText] = useState("");
  const query = useDebouncedValue(normalizeQuery(text), 250);
  const [status, setStatus] = useState<StatusFilter>(DEFAULT_FILTERS.status);
  const [starred, setStarredFilter] = useState<StarFilter>(DEFAULT_FILTERS.starred);
  const [share, setShare] = useState<ShareFilter>(DEFAULT_FILTERS.share);
  const project = params.get("project")?.trim() || "all";
  const filters = useMemo<Filters>(() => ({ status, starred, share, project, query }), [status, starred, share, project, query]);
  const list = useConversationList(filters);

  const [projects, setProjects] = useState<ProjectRef[]>([]);
  const [deleteFilesDefault, setDeleteFilesDefault] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [selectMode, setSelectMode] = useState(false);
  const [dialog, setDialog] = useState<Dialog>({ kind: "none" });
  const [pendingDelete, setPendingDelete] = useState(false);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    let live = true;
    fetchProjects().then(
      (p) => live && setProjects(p),
      () => undefined,
    );
    void fetchDeleteFilesDefault().then((v) => live && setDeleteFilesDefault(v));
    return () => {
      live = false;
    };
  }, []);

  // A new filter or search is a fresh list: nothing stays selected.
  const filterSignature = `${status}|${starred}|${share}|${project}|${query}`;
  useEffect(() => {
    setSelected(new Set());
    setSelectMode(false);
  }, [filterSignature]);

  // Rows that vanished (moved out of the filter, deleted elsewhere) leave the selection.
  useEffect(() => {
    setSelected((current) => {
      const present = new Set(list.rows.map((r) => r.publicID));
      const next = new Set([...current].filter((id) => present.has(id)));
      return next.size === current.size ? current : next;
    });
  }, [list.rows]);

  const sentinel = useRef<HTMLDivElement>(null);
  const { loadMore } = list;
  useEffect(() => {
    const el = sentinel.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && loadMore(), { rootMargin: "160px" });
    observer.observe(el);
    return () => observer.disconnect();
  }, [loadMore, list.rows.length]);

  const selectionMode = selectMode || selected.size > 0;
  const selectedRows = useMemo(() => list.rows.filter((r) => selected.has(r.publicID)), [list.rows, selected]);
  const sharedSelected = useMemo(() => selectedRows.filter(isShared), [selectedRows]);
  const sections = useMemo(() => groupByProject(list.rows, projects), [list.rows, projects]);
  const now = useMemo(() => new Date(), [list.rows]); // eslint-disable-line react-hooks/exhaustive-deps -- relative times refresh with the list
  const projectName = projects.find((p) => p.publicID === project)?.name;

  const failure = (title: string, error: unknown) => toast.error(title, { description: errorMessage(error) });

  // --- per-row actions ------------------------------------------------------

  const toggleSelect = useCallback((row: ConversationRow) => {
    setSelected((current) => {
      const next = new Set(current);
      if (!next.delete(row.publicID)) next.add(row.publicID);
      return next;
    });
  }, []);

  const actions: RowActions = {
    toggleSelect,
    star: async (row) => {
      try {
        await setStarred(row.publicID, !row.isStarred);
        list.patch(row.publicID, { isStarred: !row.isStarred });
        announceConversationChange({ type: "patch", id: row.publicID, patch: { isStarred: !row.isStarred } });
      } catch (error) {
        failure(t("recentAll.errors.star"), error);
      }
    },
    rename: (row) => setDialog({ kind: "rename", row }),
    labels: (row) => setDialog({ kind: "labels", row }),
    move: async (row, projectId) => {
      if (row.projectID === projectId) return;
      try {
        await moveToProject(row.publicID, projectId);
        const name = projects.find((p) => p.publicID === projectId)?.name ?? "";
        list.patch(row.publicID, { projectID: projectId, projectName: name });
        announceConversationChange({ type: "patch", id: row.publicID, patch: { projectID: projectId, projectName: name } });
      } catch (error) {
        failure(t("recentAll.errors.move"), error);
      }
    },
    share: (row) => setDialog({ kind: "share", row }),
    exportJson: async (row) => {
      try {
        downloadBlob(await exportConversationJson(row.publicID), `${fileStem(row.title)}.json`);
        toast.success(t("recentAll.export.succeeded"));
      } catch (error) {
        failure(t("recentAll.export.failed"), error);
      }
    },
    archive: async (row) => {
      const archive = !isArchived(row);
      try {
        await setArchived(row.publicID, archive);
        const patch = { status: archive ? "archived" : "active" };
        list.patch(row.publicID, patch);
        announceConversationChange({ type: "patch", id: row.publicID, patch });
      } catch (error) {
        failure(t("recentAll.errors.archive"), error);
      }
    },
    remove: (row) => setDialog({ kind: "delete", row }),
  };

  const onRenamed = (id: string, title: string) => {
    list.patch(id, { title });
    announceConversationChange({ type: "patch", id, patch: { title } });
  };
  const onLabels = (id: string, labels: string[]) => {
    list.patch(id, { labels });
    announceConversationChange({ type: "patch", id, patch: { labels } });
  };
  const onShareChanged = (id: string, info: ShareInfo | null) => {
    const patch: Partial<ConversationRow> = info
      ? { shareStatus: "active", shareID: info.shareID, sharedAt: info.createdAt || null, lastShareAccessedAt: info.lastAccessedAt }
      : { shareStatus: "revoked", shareID: "", sharedAt: null, lastShareAccessedAt: null };
    list.patch(id, patch);
    announceConversationChange({ type: "patch", id, patch });
  };

  // --- bulk actions ---------------------------------------------------------

  const progress = (done: number, total: number) => toast.loading(t("recentAll.processing"), { id: BULK_TOAST, description: `${done} / ${total}` });

  const bulkMove = async (projectId: string) => {
    const ids = selectedRows.map((r) => r.publicID);
    if (ids.length === 0) return;
    try {
      let moved = 0;
      for (const part of chunk(ids, 1000)) moved += await moveManyToProject(part, projectId);
      if (moved !== ids.length) throw new Error(t("recentAll.bulk.moveMismatch"));
      const name = projects.find((p) => p.publicID === projectId)?.name ?? "";
      for (const id of ids) {
        list.patch(id, { projectID: projectId, projectName: name });
        announceConversationChange({ type: "patch", id, patch: { projectID: projectId, projectName: name } });
      }
      setSelected(new Set());
      toast.success(t("recentAll.bulk.moved", { count: ids.length, project: name || t("recentAll.projectDefault") }));
    } catch (error) {
      failure(t("recentAll.bulk.moveFailed"), error);
    }
  };

  const confirmStopSharing = async () => {
    const ids = sharedSelected.map((r) => r.publicID);
    const patch: Partial<ConversationRow> = { shareStatus: "revoked", shareID: "", sharedAt: null, lastShareAccessedAt: null };
    const settle = (closed: readonly string[]) => {
      for (const id of closed) {
        list.patch(id, patch);
        announceConversationChange({ type: "patch", id, patch });
      }
    };
    const closed: string[] = [];
    try {
      for (const part of chunk(ids, REVOKE_CHUNK)) {
        progress(closed.length, ids.length);
        await revokeShares(part);
        closed.push(...part);
      }
    } catch (error) {
      // Chunks that went through are really closed: show them so, then let the dialog report the failure.
      settle(closed);
      throw error;
    } finally {
      toast.dismiss(BULK_TOAST);
    }
    settle(closed);
    toast.success(t("recentAll.bulk.sharesClosed"));
  };

  const archiveAction = archiveActionFor(selectedRows);
  const confirmArchive = async () => {
    const targets = rowsNeedingChange(selectedRows, archiveAction);
    progress(0, targets.length);
    const results = await processInChunks(targets, ARCHIVE_CHUNK, (row) => setArchived(row.publicID, archiveAction === "archive").then(() => undefined), {
      parallelWithinChunk: true,
      onProgress: progress,
    });
    toast.dismiss(BULK_TOAST);
    const patch = { status: archiveAction === "archive" ? "archived" : "active" };
    for (const r of results) {
      if (!r.ok) continue;
      list.patch(r.item.publicID, patch);
      announceConversationChange({ type: "patch", id: r.item.publicID, patch });
    }
    const failed = results.filter((r) => !r.ok);
    if (failed.length) failure(t("recentAll.errors.archive"), failed[0]?.error);
    else toast.success(archiveAction === "archive" ? t("recentAll.bulk.archived", { count: targets.length }) : t("recentAll.bulk.unarchived", { count: targets.length }));
  };

  const runDelete = async (rows: ConversationRow[], deleteFiles: boolean) => {
    setPendingDelete(true);
    progress(0, rows.length);
    try {
      const results = await processInChunks(rows, DELETE_CHUNK, (row) => deleteConversation(row.publicID, deleteFiles).then(() => undefined), { onProgress: progress });
      const gone = results.filter((r) => r.ok).map((r) => r.item.publicID);
      list.remove(gone);
      if (gone.length) announceConversationChange({ type: "remove", ids: gone });
      setSelected((current) => new Set([...current].filter((id) => !gone.includes(id))));
      if (gone.length > 1) setSelectMode(false);
      const failed = results.filter((r) => !r.ok);
      if (failed.length) failure(t("recentAll.delete.failed"), failed[0]?.error);
      else toast.success(t("recentAll.delete.done", { count: gone.length }));
    } finally {
      toast.dismiss(BULK_TOAST);
      setPendingDelete(false);
      setDialog({ kind: "none" });
    }
  };

  const exportAll = async () => {
    if (exporting) return;
    setExporting(true);
    try {
      const text = await exportAllJsonl();
      const outcome = readExportManifest(text);
      downloadBlob(new Blob([text], { type: "application/x-ndjson" }), exportFileName(new Date()));
      if (outcome.kind === "partial") toast.warning(t("recentAll.export.partial", { exported: outcome.exported, failed: outcome.failed }));
      else if (outcome.kind === "complete") toast.success(t("recentAll.export.allDone", { count: outcome.exported }));
      else toast.success(t("recentAll.export.downloaded"));
    } catch (error) {
      failure(t("recentAll.export.allFailed"), error);
    } finally {
      setExporting(false);
    }
  };

  const newChat = () => {
    window.dispatchEvent(new Event(NEW_CHAT_EVENT));
    const real = project !== "all" && project !== "unassigned";
    router.push(real ? `/chat?project_id=${encodeURIComponent(project)}` : "/chat");
  };

  // --- render ---------------------------------------------------------------

  const words = filterWords(filters);
  const emptyTitle = query
    ? t("recentAll.empty.noMatch")
    : words.length
      ? t("recentAll.empty.filtered", { filters: words.map((w) => t(`recentAll.empty.words.${w}`)).join(t("recentAll.empty.and")) })
      : t("recentAll.empty.none");
  const emptyBody = query ? t("recentAll.empty.noMatchBody") : words.length ? t("recentAll.empty.filteredBody") : t("recentAll.empty.noneBody");

  const showEmpty = list.loaded && !list.error && list.rows.length === 0;
  const master = masterState(selected, list.rows);

  return (
    <div className="mx-auto w-full max-w-4xl px-4 pb-16 pt-5 sm:px-6">
      <HeaderActions>
        <Button size="sm" onClick={newChat}>
          <Plus />
          <span className="max-sm:sr-only">{t("recentAll.newChat")}</span>
        </Button>
      </HeaderActions>

      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t("recentAll.searchPlaceholder")}
          aria-label={t("recentAll.searchPlaceholder")}
          className="pl-9"
          maxLength={200}
        />
      </div>

      {project !== "all" ? (
        <div className="mt-3 inline-flex items-center gap-1.5 rounded-full bg-secondary py-1 pl-3 pr-1 text-xs">
          {project === "unassigned" ? t("recentAll.projectDefault") : (projectName ?? t("recentAll.projectFilter"))}
          <button type="button" onClick={() => router.replace("/recent")} aria-label={t("recentAll.clearProject")} className="rounded-full p-1 hover:bg-background">
            <X className="size-3" />
          </button>
        </div>
      ) : null}

      <div className="group/toolbar mt-4 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b pb-3">
        <div className="flex min-w-0 items-center gap-2">
          <MasterCheckbox state={master} visible={selectionMode} onToggle={() => setSelected(master === "all" ? new Set() : new Set(list.rows.map((r) => r.publicID)))} />
          {selectionMode ? (
            <SelectionBar
              count={selected.size}
              sharedCount={sharedSelected.length}
              sharingEnabled={sharingEnabled}
              archiveAction={archiveAction}
              projects={projects}
              onMove={(id) => void bulkMove(id)}
              onStopSharing={() => setDialog({ kind: "bulk-share" })}
              onArchive={() => setDialog({ kind: "bulk-archive" })}
              onDelete={() => setDialog({ kind: "bulk-delete" })}
              onCancel={() => {
                setSelectMode(false);
                setSelected(new Set());
              }}
            />
          ) : (
            <>
              <p className="hidden truncate text-sm text-muted-foreground sm:block">{t("recentAll.description")}</p>
              <p className="truncate text-sm font-medium sm:hidden">{t("recentAll.title")}</p>
              <IdleTools exporting={exporting} onSelect={() => setSelectMode(true)} onExport={() => void exportAll()} />
            </>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Segmented<StatusFilter>
            label={t("recentAll.filters.status")}
            value={status}
            onChange={setStatus}
            options={[
              { value: "all", label: t("recentAll.filters.all") },
              { value: "active", label: t("recentAll.filters.active") },
              { value: "archived", label: t("recentAll.filters.archived") },
            ]}
          />
          <Segmented<StarFilter>
            label={t("recentAll.filters.starred")}
            value={starred}
            onChange={setStarredFilter}
            options={[
              { value: "all", label: t("recentAll.filters.all") },
              { value: "starred", label: t("recentAll.filters.starredOnly") },
              { value: "unstarred", label: t("recentAll.filters.unstarred") },
            ]}
          />
          {sharingEnabled ? (
            <Segmented<ShareFilter>
              label={t("recentAll.filters.share")}
              value={share}
              onChange={setShare}
              options={[
                { value: "all", label: t("recentAll.filters.all") },
                { value: "shared", label: t("recentAll.filters.sharedOnly") },
                { value: "unshared", label: t("recentAll.filters.unshared") },
              ]}
            />
          ) : null}
        </div>
      </div>

      {!list.loaded ? (
        <div role="status" aria-label={t("common.states.loading")} className="mt-4 grid gap-2">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-14 w-full rounded-xl" />
          ))}
        </div>
      ) : list.error ? (
        <EmptyState
          icon={<MessagesSquare />}
          title={t("recentAll.list.failedTitle")}
          description={t("recentAll.list.failedBody")}
          action={<Button onClick={list.reload}>{t("common.actions.retry")}</Button>}
        />
      ) : showEmpty ? (
        <EmptyState icon={<MessagesSquare />} title={emptyTitle} description={emptyBody} />
      ) : (
        <div className="mt-2 grid gap-5">
          {sections.map((section) => (
            <section key={section.id} aria-label={section.name ?? t("recentAll.projectDefault")}>
              <h2 className="flex items-baseline gap-2 px-2 pb-1 pt-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {section.name ?? t("recentAll.projectDefault")}
                <span className="font-normal normal-case tabular-nums">{section.rows.length}</span>
              </h2>
              <ul>
                {section.rows.map((row, index) => {
                  const prev = section.rows[index - 1];
                  const next = section.rows[index + 1];
                  return (
                    <ConversationItem
                      key={row.publicID}
                      row={row}
                      now={now}
                      selected={selected.has(row.publicID)}
                      selectionMode={selectionMode}
                      projects={projects}
                      actions={actions}
                      mergeTop={!!prev && selected.has(prev.publicID)}
                      mergeBottom={!!next && selected.has(next.publicID)}
                    />
                  );
                })}
              </ul>
            </section>
          ))}
          {list.loadingMore ? (
            <div role="status" aria-label={t("common.states.loading")} className="grid gap-2">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-14 w-full rounded-xl" />
              ))}
            </div>
          ) : null}
          {list.moreError ? (
            <div className="flex items-center justify-center gap-3 py-3 text-sm text-muted-foreground">
              {t("recentAll.list.moreFailed")}
              <Button size="sm" variant="outline" onClick={list.retryMore}>
                {t("common.actions.retry")}
              </Button>
            </div>
          ) : null}
          <div ref={sentinel} aria-hidden className="h-px" />
        </div>
      )}

      <RenameDialog
        row={dialog.kind === "rename" ? dialog.row : null}
        open={dialog.kind === "rename"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        onRenamed={onRenamed}
      />
      <LabelsDialog
        row={dialog.kind === "labels" ? dialog.row : null}
        open={dialog.kind === "labels"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        onSaved={onLabels}
      />
      <ShareDialog
        row={dialog.kind === "share" ? dialog.row : null}
        open={dialog.kind === "share"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        onChanged={onShareChanged}
      />
      <DeleteDialog
        open={dialog.kind === "delete" || dialog.kind === "bulk-delete"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        description={
          dialog.kind === "delete"
            ? t("recentAll.delete.description", { label: `“${dialog.row.title.trim() || t("recentAll.untitled")}”` })
            : t("recentAll.delete.description", { label: t("recentAll.bulk.selected", { count: selected.size }) })
        }
        defaultDeleteFiles={deleteFilesDefault}
        pending={pendingDelete}
        onConfirm={(deleteFiles) => runDelete(dialog.kind === "delete" ? [dialog.row] : selectedRows, deleteFiles)}
      />
      <ConfirmDialog
        open={dialog.kind === "bulk-archive"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        title={archiveAction === "archive" ? t("recentAll.bulk.archive") : t("recentAll.bulk.unarchive")}
        description={
          archiveAction === "archive"
            ? t("recentAll.bulk.archiveConfirm", { count: selected.size })
            : t("recentAll.bulk.unarchiveConfirm", { count: selected.size })
        }
        confirmLabel={archiveAction === "archive" ? t("recentAll.bulk.archive") : t("recentAll.bulk.unarchive")}
        onConfirm={confirmArchive}
      />
      <ConfirmDialog
        open={dialog.kind === "bulk-share"}
        onOpenChange={(open) => !open && setDialog({ kind: "none" })}
        title={t("recentAll.bulk.stopSharing")}
        description={t("recentAll.bulk.stopSharingConfirm", { count: sharedSelected.length })}
        confirmLabel={t("recentAll.bulk.stopSharing")}
        destructive
        onConfirm={confirmStopSharing}
      />
    </div>
  );
}

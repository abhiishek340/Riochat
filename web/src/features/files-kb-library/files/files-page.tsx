"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { ConfirmDialog } from "@/components/ui";
import { useIsMobile } from "@/hooks/use-media-query";
import { useStoredBoolean } from "@/hooks/use-stored-boolean";
import { useT } from "@/lib/i18n/provider";
import type { FileObject } from "../shared/types";
import { FileContent, type ContentTab } from "./file-content";
import { FileSidebar } from "./file-sidebar";
import { useFileActions } from "./use-file-actions";
import { useFileLibrary } from "./use-file-library";

/** Reads `?file=` and `?tab=`; any tab other than "extract" means the preview. */
export function parseDeepLink(params: { get(name: string): string | null }): { file: string | null; tab: ContentTab } {
  const file = params.get("file")?.trim() || null;
  return { file, tab: params.get("tab") === "extract" ? "extract" : "preview" };
}

export function FilesPage() {
  const t = useT();
  const params = useSearchParams();
  const link = parseDeepLink(params);
  const isMobile = useIsMobile();
  const [collapsed, setCollapsed] = useStoredBoolean("riochat:files-sidebar-collapsed", false);
  const lib = useFileLibrary(link.file);
  const actions = useFileActions(lib);
  const [tab, setTab] = useState<ContentTab>(link.tab);
  const [view, setView] = useState<"list" | "detail">(link.file ? "detail" : "list");
  const [deleteTarget, setDeleteTarget] = useState<FileObject | null>(null);
  const [bulkOpen, setBulkOpen] = useState(false);

  const { state } = lib;
  const selected = state.items.find((f) => f.fileID === state.selectedId) ?? null;

  // A new deep link (same page, different query) re-applies the tab and pane.
  useEffect(() => {
    setTab(link.tab);
    if (link.file) setView("detail");
  }, [link.file, link.tab]);

  // Nothing left to show in the detail pane on a phone: go back to the list.
  useEffect(() => {
    if (!selected && state.phase === "idle") setView("list");
  }, [selected, state.phase]);

  const openFile = (id: string) => {
    lib.select(id);
    setTab("preview");
    setView("detail");
  };

  const showList = !isMobile || view === "list";
  const showDetail = !isMobile || view === "detail";

  return (
    <div className="flex h-full min-h-0 w-full bg-background">
      {showList ? (
        <FileSidebar
          lib={lib}
          actions={actions}
          collapsed={collapsed}
          canCollapse={!isMobile}
          onToggleCollapsed={() => setCollapsed(!collapsed)}
          onOpenFile={openFile}
          onRequestDelete={setDeleteTarget}
          onRequestBulkDelete={() => setBulkOpen(true)}
        />
      ) : null}
      {showDetail ? (
        <FileContent
          file={selected}
          waiting={state.phase === "loading" || state.phase === "syncing"}
          tab={tab}
          onTabChange={setTab}
          onBack={isMobile ? () => setView("list") : undefined}
          deleting={selected ? actions.deletingIds.has(selected.fileID) : false}
          submitting={selected ? actions.submittingIds.has(selected.fileID) : false}
          vectorizeBusy={actions.vectorizeBusy}
          onDelete={() => selected && setDeleteTarget(selected)}
          onVectorize={() => selected && void actions.vectorize([selected.fileID])}
          onToggleRag={() => selected && void actions.toggleRag(selected.fileID)}
        />
      ) : null}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("filesWs.dialogs.deleteTitle")}
        description={t("filesWs.dialogs.deleteBody", { name: deleteTarget?.fileName || t("filesWs.dialogs.fallbackName") })}
        confirmLabel={t("filesWs.dialogs.deleteConfirm")}
        destructive
        onConfirm={async () => {
          if (deleteTarget) await actions.deleteOne(deleteTarget.fileID);
        }}
      />
      <ConfirmDialog
        open={bulkOpen}
        onOpenChange={setBulkOpen}
        title={t("filesWs.dialogs.bulkTitle")}
        description={t("filesWs.dialogs.bulkBody", { count: state.checked.size })}
        confirmLabel={t("filesWs.dialogs.deleteConfirm")}
        destructive
        onConfirm={() => actions.bulkDelete()}
      />
    </div>
  );
}

"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useIsMobile } from "@/hooks/use-media-query";
import { useStoredBoolean } from "@/hooks/use-stored-boolean";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import type { KnowledgeBase } from "../shared/types";
import { announceKbChanged } from "../shared/invalidation";
import type { KbMode } from "./api";
import { KbAddDialog } from "./kb-add-dialog";
import { KbDeleteDialog } from "./kb-delete-dialog";
import { KbDetail } from "./kb-detail";
import { KbEditorDialog } from "./kb-editor-dialog";
import { KbSidebar } from "./kb-sidebar";
import { isEditable } from "./model";
import { useKbActions } from "./use-kb-actions";
import { useKbFiles } from "./use-kb-files";
import { useKbList } from "./use-kb-list";

/**
 * Two panes: the list of knowledge bases and the files of the selected one.
 * `mode="admin"` is the administrator console's variant (built-in bases and
 * platform sources); `mode="user"` is the personal workspace.
 */
export function KnowledgeBaseWorkspace({ mode = "user" }: { mode?: KbMode }) {
  const router = useRouter();
  const { policy, loaded } = useFeaturePolicy();
  // The user route only mounts when the server's policy allows knowledge bases.
  const allowed = mode === "admin" || (loaded && policy.knowledgeBaseEnabled);
  const blocked = mode === "user" && loaded && !policy.knowledgeBaseEnabled;

  useEffect(() => {
    if (blocked) router.replace("/");
  }, [blocked, router]);

  if (!allowed) return null;
  return <Workspace mode={mode} />;
}

function Workspace({ mode }: { mode: KbMode }) {
  const isMobile = useIsMobile();
  const [collapsed, setCollapsed] = useStoredBoolean(`riochat:kb-sidebar-collapsed:${mode}`, false);
  const [view, setView] = useState<"list" | "detail">("list");
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<KnowledgeBase | null>(null);
  const [deleteTargets, setDeleteTargets] = useState<readonly KnowledgeBase[]>([]);
  const [addOpen, setAddOpen] = useState(false);

  const list = useKbList(mode, true);
  const actions = useKbActions(mode, list, { onCreated: () => setView("detail") });
  const { state } = list;
  const selected = state.items.find((kb) => kb.publicID === state.selectedId) ?? null;
  const editable = selected ? isEditable(selected, mode) : false;

  const { replaceItem, load } = list;
  const files = useKbFiles({
    mode,
    kb: selected,
    editable,
    onSummary: replaceItem,
    onMutated: (id) => void load({ preferred: id, silent: true }),
  });

  // Nothing selected (and not loading): the phone goes back to the list.
  useEffect(() => {
    if (!selected && !state.loading) setView("list");
  }, [selected, state.loading]);

  const openKb = useCallback(
    (id: string) => {
      list.select(id);
      setView("detail");
    },
    [list],
  );

  const showList = !isMobile || view === "list";
  const showDetail = !isMobile || view === "detail";

  return (
    <div className="flex h-full min-h-0 w-full bg-background">
      {showList ? (
        <KbSidebar
          mode={mode}
          list={list}
          collapsed={collapsed}
          canCollapse={!isMobile}
          deleting={actions.deleting}
          onToggleCollapsed={() => setCollapsed(!collapsed)}
          onOpen={openKb}
          onCreate={() => {
            setEditing(null);
            setEditorOpen(true);
          }}
          onEdit={(kb) => {
            list.select(kb.publicID);
            setEditing(kb);
            setEditorOpen(true);
          }}
          onDelete={(kb) => setDeleteTargets([kb])}
          onBulkDelete={() => setDeleteTargets(state.items.filter((kb) => state.checked.has(kb.publicID)))}
        />
      ) : null}
      {showDetail ? (
        <KbDetail
          mode={mode}
          kb={selected}
          files={files}
          toggling={selected !== null && actions.togglingId === selected.publicID}
          onBack={isMobile ? () => setView("list") : undefined}
          onAddFiles={() => setAddOpen(true)}
          onToggleEnabled={(on) => selected && void actions.toggleEnabled(selected, on)}
        />
      ) : null}

      <KbEditorDialog
        open={editorOpen}
        editing={editing}
        saving={actions.saving}
        onSave={(draft) => actions.save(editing, draft)}
        onClose={() => setEditorOpen(false)}
      />
      <KbDeleteDialog
        targets={deleteTargets}
        deleting={actions.deleting}
        onConfirm={(deleteFiles) => actions.remove(deleteTargets, deleteFiles)}
        onClose={() => setDeleteTargets([])}
      />
      {selected && editable ? (
        <KbAddDialog
          mode={mode}
          kb={selected}
          open={addOpen}
          onClose={() => setAddOpen(false)}
          onAdded={() => {
            files.reload();
            void load({ preferred: selected.publicID, silent: true });
            announceKbChanged(selected.publicID);
          }}
        />
      ) : null}
    </div>
  );
}

"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { RotateCw, Sparkles, ScrollText } from "lucide-react";
import { Button, ConfirmDialog, EmptyState, Skeleton, Spinner } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { ItemCard } from "./item-card";
import { ItemEditorDialog, ItemViewDialog } from "./item-dialogs";
import { matchesSearch, mergeVisible, orderItems, type LibraryItem, type LibraryKind } from "./model";
import { useLibraryMutations } from "./use-library-mutations";
import { useLibrarySource } from "./use-library-source";

/** The card list of one kind (skills or prompts) with its dialogs. */
export function LibrarySection({
  kind,
  search,
  creating,
  onCreatingChange,
}: {
  kind: LibraryKind;
  search: string;
  creating: boolean;
  onCreatingChange: (open: boolean) => void;
}) {
  const t = useT();
  // Prompts are searched on the server and paged; skills are loaded once and filtered here.
  const source = useLibrarySource(kind, search, kind === "prompts");
  const mutations = useLibraryMutations(kind, source.mutateMine);
  const [editing, setEditing] = useState<LibraryItem | null>(null);
  const [viewing, setViewing] = useState<LibraryItem | null>(null);
  const [deleting, setDeleting] = useState<LibraryItem | null>(null);
  const sentinel = useRef<HTMLDivElement>(null);

  const items = useMemo(
    () => orderItems(kind, mergeVisible(source.mine, source.builtin)).filter((item) => matchesSearch(kind, item, search)),
    [kind, source.mine, source.builtin, search],
  );

  // Infinite scroll for prompts: load the next page shortly before the sentinel scrolls into view.
  const { hasMore, moreFailed, loading, loadingMore, loadMore } = source;
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !hasMore || moreFailed || loading || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && loadMore(), { rootMargin: "160px" });
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore, moreFailed, loading, loadingMore, loadMore]);

  const open = async (item: LibraryItem) => {
    const full = await mutations.loadFull(item);
    if (!full) return;
    if (full.scope === "user") setEditing(full);
    else setViewing(full);
  };

  const filtered = search.trim() !== "";
  const Icon = kind === "skills" ? Sparkles : ScrollText;

  return (
    <div>
      {source.loading && items.length === 0 ? (
        <div className="grid gap-2" aria-busy role="status" aria-label={t("common.states.loading")}>
          {Array.from({ length: kind === "skills" ? 6 : 8 }, (_, i) => (
            <Skeleton key={i} className="h-[58px] rounded-xl" />
          ))}
        </div>
      ) : source.failed && items.length === 0 ? (
        <EmptyState
          icon={<RotateCw />}
          title={t("libraryWs.loadFailedTitle")}
          description={t("libraryWs.loadFailedBody")}
          action={
            <Button size="sm" variant="outline" onClick={source.reload}>
              {t("common.actions.retry")}
            </Button>
          }
        />
      ) : items.length === 0 && !source.moreFailed ? (
        <EmptyState
          icon={<Icon />}
          title={filtered ? t(`libraryWs.${kind}.noMatchTitle`) : t(`libraryWs.${kind}.emptyTitle`)}
          description={filtered ? t(`libraryWs.${kind}.noMatchBody`) : t(`libraryWs.${kind}.emptyBody`)}
        />
      ) : (
        <ul className="grid gap-2">
          {items.map((item) => (
            <ItemCard
              key={`${item.scope}:${item.id}`}
              item={item}
              onOpen={() => void open(item)}
              onToggle={(enabled) => void mutations.toggle(item, enabled)}
              onDelete={() => setDeleting(item)}
            />
          ))}
        </ul>
      )}

      {source.moreFailed ? (
        <div className="mt-3 flex items-center justify-between gap-3 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm">
          <span className="text-destructive">{t("libraryWs.prompts.loadFailed")}</span>
          <Button size="sm" variant="outline" onClick={source.retry}>
            {t("common.actions.retry")}
          </Button>
        </div>
      ) : null}
      {source.loadingMore ? (
        <div className="flex items-center justify-center gap-2 py-4 text-sm text-muted-foreground" role="status">
          <Spinner />
          {t("libraryWs.prompts.loadingMore")}
        </div>
      ) : null}
      <div ref={sentinel} aria-hidden className="h-px" />

      <ItemEditorDialog
        kind={kind}
        open={creating || editing !== null}
        editing={editing}
        saving={mutations.saving}
        onSave={(form) => mutations.save(editing, form)}
        onClose={() => {
          setEditing(null);
          onCreatingChange(false);
        }}
      />
      <ItemViewDialog kind={kind} item={viewing} onClose={() => setViewing(null)} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(next) => !next && setDeleting(null)}
        title={t(`libraryWs.${kind}.deleteTitle`)}
        description={t(`libraryWs.${kind}.deleteBody`)}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          const target = deleting;
          if (!target) return;
          if (kind === "prompts") {
            // The confirmation closes at once; the toast reports the outcome.
            void mutations.remove(target);
            return;
          }
          await mutations.remove(target);
        }}
      />
    </div>
  );
}

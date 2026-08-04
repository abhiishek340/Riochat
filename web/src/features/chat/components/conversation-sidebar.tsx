"use client";

import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ChevronDown, ChevronRight, FolderPlus, MoreHorizontal, Pencil, Plus, Search, Star, Tags, Trash2, X, FolderInput, Share2, Download, Wand2, FolderClosed } from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Skeleton,
} from "@/components/ui";
import { useSession } from "@/lib/session/provider";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useConversationActions } from "../hooks/use-conversation-actions";
import { listConversations } from "../api";
import { groupConversations } from "../lib/grouping";
import { conversationStore, loadMoreConversations, parseLabels, refreshConversations, resetConversationStore } from "../state/conversation-store";
import { requestNewChat } from "../state/new-chat";
import { resetResources } from "../state/resources";
import { useConversationRunning, useRunFeed } from "../state/run-feed";
import { useChatPrefs } from "../state/resources";
import { useStore } from "../state/store";
import type { ConversationDTO, ProjectDTO } from "../types";
import {
  DeleteConversationDialog,
  DeleteProjectDialog,
  LabelsDialog,
  MoveDialog,
  ProjectDialog,
  RenameDialog,
  ShareDialog,
} from "./conversation-dialogs";

type DialogState =
  | { kind: "rename" | "labels" | "move" | "delete" | "share"; conversation: ConversationDTO }
  | { kind: "project"; project: ProjectDTO | null }
  | { kind: "deleteProject"; project: ProjectDTO }
  | null;

export function ConversationSidebar() {
  return (
    <Suspense fallback={null}>
      <SidebarInner />
    </Suspense>
  );
}

function SidebarInner() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const { user, accessToken } = useSession();
  const prefs = useChatPrefs();
  const activeId = params.get("conversation_id") ?? "";
  const state = useStore(conversationStore);
  const [query, setQuery] = useState("");
  const debounced = useDebouncedValue(query.trim(), 250);
  const [results, setResults] = useState<ConversationDTO[] | null>(null);
  const [dialog, setDialog] = useState<DialogState>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  useRunFeed(user?.publicID);

  // Reload everything when the signed-in user changes.
  const userId = user?.publicID ?? "";
  useEffect(() => {
    if (!accessToken || !userId) return;
    void refreshConversations();
    const onFocus = () => void refreshConversations();
    window.addEventListener("focus", onFocus);
    return () => {
      window.removeEventListener("focus", onFocus);
    };
  }, [accessToken, userId]);
  useEffect(
    () => () => {
      resetConversationStore();
      resetResources();
    },
    [userId],
  );

  useEffect(() => {
    if (!debounced) {
      setResults(null);
      return;
    }
    let live = true;
    listConversations({ q: debounced, pageSize: 50, status: "active" }).then(
      (res) => live && setResults(res.results),
      () => live && setResults([]),
    );
    return () => {
      live = false;
    };
  }, [debounced]);

  const afterDelete = useCallback(
    (id: string) => {
      if (id === activeId) router.push("/chat");
    },
    [activeId, router],
  );
  const actions = useConversationActions(afterDelete);

  const projectIds = useMemo(() => new Set(state.projects.map((p) => p.publicID)), [state.projects]);
  const free = useMemo(() => state.conversations.filter((c) => !c.projectID || !projectIds.has(c.projectID)), [state.conversations, projectIds]);
  const groups = useMemo(() => groupConversations(free, new Date()), [free]);
  const loading = !state.loaded && state.loading;

  const row = (c: ConversationDTO) => (
    <ConversationRow
      key={c.publicID}
      conversation={c}
      active={c.publicID === activeId}
      onAction={(kind) => {
        if (kind === "star") void actions.toggleStar(c);
        else if (kind === "autoRename") void actions.autoRename(c);
        else if (kind === "export") void actions.exportJson(c);
        else setDialog({ kind, conversation: c });
      }}
    />
  );

  return (
    <div className="flex flex-col gap-3" data-testid="conversation-sidebar">
      <div className="flex items-center gap-1.5">
        <Button asChild size="sm" className="flex-1 justify-start">
          <Link href="/chat" onClick={() => requestNewChat(null)}>
            <Plus /> {t("chatNav.sidebar.newChat")}
          </Link>
        </Button>
      </div>

      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("chatNav.sidebar.search")}
          aria-label={t("chatNav.sidebar.search")}
          className="h-8 w-full rounded-lg border border-transparent bg-sidebar-accent/60 pl-8 pr-7 text-[13px] outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:bg-background"
        />
        {query ? (
          <button type="button" onClick={() => setQuery("")} aria-label={t("chatNav.sidebar.clearSearch")} className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:text-foreground">
            <X className="size-3.5" />
          </button>
        ) : null}
      </div>

      {debounced ? (
        <ul className="grid gap-0.5">
          {results === null ? <Skeleton className="h-8 w-full" /> : results.length === 0 ? <li className="px-2 py-3 text-xs text-muted-foreground">{t("chatNav.sidebar.noResults")}</li> : results.map((c) => <li key={c.publicID}>{row(c as ConversationDTO)}</li>)}
        </ul>
      ) : (
        <>
          <section aria-label={t("chatNav.sidebar.projects")} className="grid gap-0.5">
            <div className="flex items-center justify-between px-2 pt-1">
              <h2 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t("chatNav.sidebar.projects")}</h2>
              <button type="button" onClick={() => setDialog({ kind: "project", project: null })} aria-label={t("chatNav.sidebar.newProject")} title={t("chatNav.sidebar.newProject")} className="rounded p-1 text-muted-foreground hover:bg-sidebar-accent hover:text-foreground">
                <FolderPlus className="size-3.5" />
              </button>
            </div>
            {state.projects.map((p) => {
              const items = state.conversations.filter((c) => c.projectID === p.publicID);
              const isOpen = !collapsed[p.publicID];
              return (
                <div key={p.publicID}>
                  <div className="group/proj flex items-center rounded-lg hover:bg-sidebar-accent">
                    <button type="button" onClick={() => setCollapsed((c) => ({ ...c, [p.publicID]: isOpen }))} aria-expanded={isOpen} aria-label={isOpen ? t("chatNav.sidebar.collapse") : t("chatNav.sidebar.expand")} className="p-1.5 text-muted-foreground">
                      {isOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                    </button>
                    <Link href={`/chat?project_id=${encodeURIComponent(p.publicID)}`} onClick={() => requestNewChat(p.publicID)} title={t("chatNav.sidebar.startInProject")} className="flex min-w-0 flex-1 items-center gap-1.5 py-1.5 text-[13px]">
                      <FolderClosed className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="truncate">{p.name}</span>
                    </Link>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button type="button" aria-label={t("chatNav.sidebar.projectMenu")} className="mr-1 rounded p-1 text-muted-foreground opacity-0 hover:text-foreground focus-visible:opacity-100 group-hover/proj:opacity-100 data-[state=open]:opacity-100">
                          <MoreHorizontal className="size-3.5" />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="start">
                        <DropdownMenuItem onSelect={() => setDialog({ kind: "project", project: p })}>
                          <Pencil /> {t("chatNav.project.edit")}
                        </DropdownMenuItem>
                        <DropdownMenuItem destructive onSelect={() => setDialog({ kind: "deleteProject", project: p })}>
                          <Trash2 /> {t("chatNav.project.delete")}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                  {isOpen && items.length > 0 ? <div className="ml-4 border-l pl-1">{items.map(row)}</div> : null}
                </div>
              );
            })}
          </section>

          {loading ? (
            <div className="grid gap-1.5">
              {Array.from({ length: 6 }, (_, i) => (
                <Skeleton key={i} className="h-8 w-full" />
              ))}
            </div>
          ) : state.error && state.conversations.length === 0 ? (
            <p className="px-2 text-xs text-destructive">{t("chatNav.sidebar.loadFailed")}</p>
          ) : state.conversations.length === 0 ? (
            <p className="px-2 py-3 text-xs text-muted-foreground">{t("chatNav.sidebar.empty")}</p>
          ) : (
            groups.map((g) => (
              <section key={g.id} aria-label={t(`chatNav.sidebar.${g.id}`)} className="grid gap-0.5">
                <h2 className="px-2 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t(`chatNav.sidebar.${g.id}`)}</h2>
                {g.items.map(row)}
              </section>
            ))
          )}
          {state.conversations.length < state.total ? (
            <Button variant="ghost" size="sm" onClick={() => void loadMoreConversations()} loading={state.loading}>
              {t("chatNav.sidebar.loadMore")}
            </Button>
          ) : null}
        </>
      )}

      <SidebarDialogs dialog={dialog} setDialog={setDialog} actions={actions} defaultDeleteFiles={prefs.deleteFilesByDefault} projects={state.projects} />
    </div>
  );
}

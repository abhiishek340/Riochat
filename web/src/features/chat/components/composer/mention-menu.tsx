"use client";

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type RefObject } from "react";
import { BookOpen, Bot, Check, FileText, MessageSquareText, Sparkles, Wrench } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { listFiles, listKnowledgeBases, listPromptPresets, listSkills } from "../../api";
import { groupModels, matchesModel } from "../../lib/model-groups";
import { modelLabel } from "../../lib/model-caps";
import { browserIO, readRecents, type RecentKind } from "../../lib/persist";
import { isComposingKey, type KeyLike } from "../../lib/settings";
import type { FileDTO, KnowledgeBaseDTO, McpToolDTO, ModelDTO, PromptPresetDTO, SkillSummaryDTO } from "../../types";

export type MentionKind = RecentKind;

export interface MentionHandle {
  /** Returns true when the key was used by the menu. */
  handleKey: (e: KeyLike & { preventDefault: () => void }) => boolean;
}

interface Row {
  kind: MentionKind;
  id: string;
  label: string;
  detail?: string;
  checked?: boolean;
  disabled?: boolean;
  group?: string;
  payload: unknown;
}

const PER_KIND_IN_ALL = 4;
const RECENT_SHOWN = 5;

// A short-lived cache of library file pages.
const fileCache = new Map<string, { at: number; value: { total: number; results: FileDTO[] } }>();
const FILE_TTL = 60_000;
const FILE_CACHE_MAX = 80;

async function filesPage(q: string, page: number): Promise<{ total: number; results: FileDTO[] }> {
  const key = `${q}\u0000${page}`;
  const hit = fileCache.get(key);
  if (hit && Date.now() - hit.at < FILE_TTL) return hit.value;
  const res = await listFiles({ q, page, pageSize: 20, sort: "last_used" });
  fileCache.set(key, { at: Date.now(), value: res });
  if (fileCache.size > FILE_CACHE_MAX) fileCache.delete(fileCache.keys().next().value as string);
  return res;
}

export interface MentionMenuProps {
  open: boolean;
  trigger: "@" | "/";
  query: string;
  anchorRef: RefObject<HTMLElement | null>;
  kinds: MentionKind[];
  models: ModelDTO[];
  selectedModel: string;
  tools: McpToolDTO[];
  selectedToolIds: number[];
  selectedSkillIds: number[];
  selectedKbIds: string[];
  kbAvailable: boolean;
  onPick: (kind: MentionKind, payload: unknown) => void;
  onClose: () => void;
  /** DOM id of the list, so the text field can point at it with `aria-controls`. */
  listId?: string;
}

export const MentionMenu = forwardRef<MentionHandle, MentionMenuProps>(function MentionMenu(props, ref) {
  const { open, trigger, query, anchorRef, kinds, models, selectedModel, tools, selectedToolIds, selectedSkillIds, selectedKbIds, kbAvailable, onPick, onClose } = props;
  const t = useT();
  const [tab, setTab] = useState<"all" | MentionKind>("all");
  const [active, setActive] = useState(0);
  const [recents, setRecents] = useState(() => readRecents(browserIO));
  const [direction, setDirection] = useState<"up" | "down">("up");
  const debounced = useDebouncedValue(query, 180);
  const listRef = useRef<HTMLDivElement>(null);

  const [files, setFiles] = useState<{ items: FileDTO[]; total: number; page: number; loading: boolean }>({ items: [], total: 0, page: 1, loading: false });
  const [kbs, setKbs] = useState<KnowledgeBaseDTO[]>([]);
  const [skills, setSkills] = useState<SkillSummaryDTO[]>([]);
  const [prompts, setPrompts] = useState<PromptPresetDTO[]>([]);

  // The recents snapshot is frozen per session.
  useEffect(() => {
    if (open) {
      setRecents(readRecents(browserIO));
      setTab("all");
      setActive(0);
      const rect = anchorRef.current?.getBoundingClientRect();
      if (rect) setDirection(window.innerHeight - rect.bottom >= 300 ? "down" : "up");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, trigger]);

  useEffect(() => setActive(0), [tab, query]);

  const wants = useCallback((k: MentionKind) => kinds.includes(k) && (tab === "all" || tab === k), [kinds, tab]);

  useEffect(() => {
    if (!open || !wants("file")) return;
    let live = true;
    setFiles((f) => ({ ...f, loading: true }));
    filesPage(debounced, 1).then(
      (res) => live && setFiles({ items: res.results, total: res.total, page: 1, loading: false }),
      () => live && setFiles((f) => ({ ...f, loading: false })),
    );
    return () => {
      live = false;
    };
  }, [open, debounced, wants]);
  useEffect(() => {
    if (!open || !wants("knowledge") || !kbAvailable) return;
    let live = true;
    listKnowledgeBases({ q: debounced, pageSize: 50 }).then((r) => live && setKbs(r.results), () => undefined);
    return () => {
      live = false;
    };
  }, [open, debounced, wants, kbAvailable]);
  useEffect(() => {
    if (!open || !wants("skill")) return;
    let live = true;
    listSkills({ q: debounced, pageSize: 50 }).then((r) => live && setSkills(r.results.filter((s) => s.enabled)), () => undefined);
    return () => {
      live = false;
    };
  }, [open, debounced, wants]);
  useEffect(() => {
    if (!open || !wants("prompt")) return;
    let live = true;
    listPromptPresets({ q: debounced, pageSize: 50 }).then((r) => live && setPrompts(r.results), () => undefined);
    return () => {
      live = false;
    };
  }, [open, debounced, wants]);

  const loadMoreFiles = useCallback(async () => {
    if (files.loading || files.items.length >= files.total) return;
    const next = files.page + 1;
    setFiles((f) => ({ ...f, loading: true }));
    try {
      const res = await filesPage(debounced, next);
      setFiles((f) => ({ items: [...f.items, ...res.results.filter((r) => !f.items.some((x) => x.fileID === r.fileID))], total: res.total, page: next, loading: false }));
    } catch {
      setFiles((f) => ({ ...f, loading: false }));
    }
  }, [files, debounced]);

  const q = query.trim().toLowerCase();

  const byKind = useMemo(() => {
    const out = new Map<MentionKind, Row[]>();
    if (kinds.includes("model")) {
      const groups = groupModels(models.filter((m) => matchesModel(m, q)), t("chatModel.otherGroup"));
      out.set("model", groups.flatMap((g) => g.models.map((m): Row => ({ kind: "model", id: m.platformModelName, label: modelLabel(m.platformModelName), detail: m.vendorName || undefined, checked: m.platformModelName === selectedModel, group: g.name, payload: m }))));
    }
    if (kinds.includes("file")) out.set("file", files.items.map((f): Row => ({ kind: "file", id: f.fileID, label: f.fileName, detail: f.fileCategory || undefined, payload: f })));
    if (kinds.includes("knowledge") && kbAvailable)
      out.set("knowledge", kbs.map((k): Row => ({ kind: "knowledge", id: k.publicID, label: k.name, detail: k.readyFileCount > 0 ? t("chatComposer.kb.readyFiles", { count: k.readyFileCount }) : t("chatComposer.kb.notReady"), checked: selectedKbIds.includes(k.publicID), disabled: k.readyFileCount === 0 && !selectedKbIds.includes(k.publicID), payload: k })));
    if (kinds.includes("tool")) {
      const list = tools.filter((x) => !q || `${x.displayName} ${x.name} ${x.serverName} ${x.description}`.toLowerCase().includes(q));
      out.set("tool", list.map((x): Row => ({ kind: "tool", id: String(x.id), label: x.displayName || x.name, detail: x.serverName || undefined, checked: selectedToolIds.includes(x.id), group: x.serverName, payload: x })));
    }
    if (kinds.includes("skill")) out.set("skill", skills.map((s): Row => ({ kind: "skill", id: String(s.id), label: s.title, detail: s.description || undefined, checked: selectedSkillIds.includes(s.id), group: s.scope === "builtin" ? t("chatComposer.mention.builtin") : t("chatComposer.mention.mine"), payload: s })));
    if (kinds.includes("prompt")) out.set("prompt", prompts.map((p): Row => ({ kind: "prompt", id: String(p.id), label: p.title, detail: p.description || undefined, group: p.scope === "builtin" ? t("chatComposer.mention.builtin") : t("chatComposer.mention.mine"), payload: p })));
    return out;
  }, [kinds, models, q, selectedModel, files.items, kbs, kbAvailable, tools, skills, prompts, selectedKbIds, selectedToolIds, selectedSkillIds, t]);

  // Rows shown in order, with section headers.
  const sections = useMemo(() => {
    const list: { id: string; title: string; rows: Row[]; more?: { kind: MentionKind; count: number } }[] = [];
    const recentRows = (k: MentionKind): Row[] => {
      const all = byKind.get(k) ?? [];
      const ids = recents[k].slice(0, RECENT_SHOWN).map((r) => r.id);
      return ids.map((id) => all.find((r) => r.id === id)).filter((r): r is Row => !!r);
    };
    if (tab === "all") {
      if (!q) {
        // The five most recently used items across all kinds, newest first.
        const stamped = kinds.flatMap((k) =>
          recents[k].flatMap((r) => {
            const row = (byKind.get(k) ?? []).find((x) => x.id === r.id);
            return row ? [{ row, at: r.usedAt }] : [];
          }),
        );
        const rows = stamped.sort((a, b) => b.at - a.at).slice(0, RECENT_SHOWN).map((x) => x.row);
        if (rows.length > 0) list.push({ id: "recent", title: t("chatComposer.mention.recent"), rows });
      }
      for (const k of kinds) {
        const rows = byKind.get(k) ?? [];
        if (rows.length === 0) continue;
        list.push({ id: k, title: t(`chatComposer.mention.kind.${k}` as "chatComposer.mention.kind.model"), rows: rows.slice(0, PER_KIND_IN_ALL), more: rows.length > PER_KIND_IN_ALL ? { kind: k, count: rows.length } : undefined });
      }
    } else {
      const rows = byKind.get(tab) ?? [];
      const rec = q ? [] : recentRows(tab);
      if (rec.length > 0) list.push({ id: "recent", title: t("chatComposer.mention.recent"), rows: rec });
      const groups = new Map<string, Row[]>();
      for (const r of rows) {
        const g = r.group ?? "";
        const bucket = groups.get(g);
        if (bucket) bucket.push(r);
        else groups.set(g, [r]);
      }
      for (const [g, rs] of groups) list.push({ id: `${tab}:${g}`, title: g, rows: rs });
    }
    return list;
  }, [tab, q, kinds, byKind, recents, t]);

  const flat = useMemo(() => sections.flatMap((s) => s.rows).filter((r) => !r.disabled), [sections]);

  const pick = useCallback(
    (row: Row) => {
      if (row.disabled) return;
      onPick(row.kind, row.payload);
    },
    [onPick],
  );

  useImperativeHandle(ref, () => ({
    handleKey(e) {
      if (!open) return false;
      if (isComposingKey(e)) return false;
      const tabs: ("all" | MentionKind)[] = kinds.length > 1 ? ["all", ...kinds] : [];
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return true;
      }
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (flat.length > 0) setActive((i) => (e.key === "ArrowDown" ? (i + 1) % flat.length : (i - 1 + flat.length) % flat.length));
        return true;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        if (tabs.length > 0) {
          const at = tabs.indexOf(tab);
          setTab(tabs[(at + (e.shiftKey ? -1 : 1) + tabs.length) % tabs.length] as "all" | MentionKind);
        } else if (flat[active]) pick(flat[active] as Row);
        return true;
      }
      if (e.key === "Enter" && !e.shiftKey) {
        const row = flat[active];
        if (row) {
          e.preventDefault();
          pick(row);
          return true;
        }
      }
      return false;
    },
  }));

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView?.({ block: "nearest" });
  }, [active, sections]);

  if (!open) return null;

  const icon = (k: MentionKind) => (k === "model" ? <Bot className="size-4" /> : k === "file" ? <FileText className="size-4" /> : k === "knowledge" ? <BookOpen className="size-4" /> : k === "tool" ? <Wrench className="size-4" /> : k === "skill" ? <Sparkles className="size-4" /> : <MessageSquareText className="size-4" />);
  const tabsList: ("all" | MentionKind)[] = kinds.length > 1 ? ["all", ...kinds] : [];
  let flatIndex = -1;
  const empty = sections.every((s) => s.rows.length === 0);
  return (
    <div
      className={cn("absolute inset-x-0 z-30 overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-lg", direction === "up" ? "bottom-full mb-2" : "top-full mt-2")}
      onMouseDown={(e) => e.preventDefault()}
    >
      {tabsList.length > 0 ? (
        <div className="flex gap-1 overflow-x-auto border-b px-2 py-1.5" role="tablist">
          {tabsList.map((k) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} onClick={() => setTab(k)} className={cn("whitespace-nowrap rounded-md px-2 py-1 text-xs", tab === k ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground hover:text-foreground")}>
              {k === "all" ? t("chatComposer.mention.all") : t(`chatComposer.mention.kind.${k}` as "chatComposer.mention.kind.model")}
              {q && k !== "all" ? <span className="ml-1 opacity-70">{(byKind.get(k) ?? []).length}</span> : null}
            </button>
          ))}
        </div>
      ) : null}
      <div
        ref={listRef}
        id={props.listId}
        role={empty ? undefined : "listbox"}
        aria-label={trigger === "@" ? t("chatComposer.mention.resources") : t("chatComposer.mention.context")}
        className="overflow-auto p-1.5"
        style={{ maxHeight: tabsList.length > 0 ? 340 : 280 }}
        onScroll={(e) => {
          const el = e.currentTarget;
          if (tab === "file" && el.scrollHeight - el.scrollTop - el.clientHeight < 48) void loadMoreFiles();
        }}
      >
        {empty ? <p role="status" className="px-3 py-6 text-center text-sm text-muted-foreground">{files.loading ? t("common.states.loading") : t(`chatComposer.mention.empty.${tab === "all" ? (trigger === "@" ? "resources" : "context") : tab}` as "chatComposer.mention.empty.model")}</p> : null}
        {sections.map((s) => (
          <div key={s.id} role="group" aria-label={s.title || undefined}>
            {s.title ? <div aria-hidden className="px-2 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{s.title}</div> : null}
            {s.rows.map((r) => {
              if (!r.disabled) flatIndex++;
              const isActive = !r.disabled && flatIndex === active;
              return (
                <button
                  key={`${s.id}:${r.id}`}
                  type="button"
                  role="option"
                  aria-selected={isActive}
                  aria-disabled={r.disabled || undefined}
                  data-active={isActive || undefined}
                  onMouseEnter={() => !r.disabled && setActive(flatIndex)}
                  onClick={() => pick(r)}
                  className={cn("flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm", isActive && "bg-accent", r.disabled && "opacity-50")}
                >
                  <span className="text-muted-foreground">{icon(r.kind)}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{r.label}</span>
                    {r.detail ? <span className="block truncate text-xs text-muted-foreground">{r.detail}</span> : null}
                  </span>
                  {r.checked ? <Check className="size-4 text-primary" /> : null}
                </button>
              );
            })}
            {s.more ? (
              <button type="button" onClick={() => setTab(s.more!.kind)} className="w-full rounded-lg px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent/50">
                {t("chatComposer.mention.viewAll", { count: s.more.count })}
              </button>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
});

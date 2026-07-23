"use client";

/**
 * Tools, skills and knowledge bases chosen for the next message, kept per
 * conversation in browser storage; new chats start from the project's (or the
 * user's) defaults once per context. Also the per-device choices for
 * interactive components and the visual-layout prompt.
 */
import { useCallback, useEffect, useMemo, useReducer, useRef, useSyncExternalStore } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { listKnowledgeBases, listSkills } from "../api";
import { browserIO, loadSelection, moveSelection, NEW_CHAT_KEY, saveSelection } from "../lib/persist";
import {
  effectiveUiComponents,
  MAX_KNOWLEDGE_BASES,
  normalizeTools,
  parseStoredIds,
  storableUiChoice,
  toggleKnowledgeBase,
  toggleServer,
  toggleSkill,
  toggleTool,
} from "../lib/selection";
import { DRAFT_KEY, TEMP_KEY } from "../state/engine-types";
import type { McpToolDTO, ProjectDTO, SkillSummaryDTO, UiComponentDTO } from "../types";
import { readStorage, removeStorage, writeStorage } from "@/lib/utils/storage";
import { storageKeyFor } from "./use-draft";

interface Sel {
  toolIds: number[];
  skills: SkillSummaryDTO[];
  kbIds: string[];
}
const EMPTY: Sel = { toolIds: [], skills: [], kbIds: [] };
const KB_PAGE = 50;
const KB_MAX_PAGES = 10;

export const UI_COMPONENTS_KEY = "riochat:ui-components";
export const VISUAL_PROMPT_KEY = "riochat:visual-prompt";

// --- device-level preferences with cross-tab sync ------------------------------------------

function subscribeStorage(key: string) {
  return (notify: () => void) => {
    const on = (e: StorageEvent) => {
      if (e.key === key || e.key === null) notify();
    };
    const local = () => notify();
    window.addEventListener("storage", on);
    window.addEventListener(`riochat:pref:${key}`, local);
    return () => {
      window.removeEventListener("storage", on);
      window.removeEventListener(`riochat:pref:${key}`, local);
    };
  };
}

function writePref(key: string, value: string | null): void {
  if (value === null) removeStorage(key);
  else writeStorage(key, value);
  window.dispatchEvent(new Event(`riochat:pref:${key}`));
}

export function useVisualPrompt(): [boolean, (on: boolean) => void] {
  const raw = useSyncExternalStore(
    subscribeStorage(VISUAL_PROMPT_KEY),
    () => readStorage(VISUAL_PROMPT_KEY),
    () => null,
  );
  return [raw === "true", (on) => writePref(VISUAL_PROMPT_KEY, on ? "true" : "false")];
}

export function useUiComponentChoice(components: readonly UiComponentDTO[]): {
  effective: number[];
  explicit: boolean;
  set: (ids: number[]) => void;
  reset: () => void;
} {
  const raw = useSyncExternalStore(
    subscribeStorage(UI_COMPONENTS_KEY),
    () => readStorage(UI_COMPONENTS_KEY),
    () => null,
  );
  const stored = useMemo(() => parseStoredIds(raw), [raw]);
  const effective = useMemo(() => effectiveUiComponents(components, stored), [components, stored]);
  return {
    effective,
    explicit: stored !== null,
    set: (ids) => {
      const store = storableUiChoice(components, ids);
      writePref(UI_COMPONENTS_KEY, store === null ? null : JSON.stringify(store));
    },
    reset: () => writePref(UI_COMPONENTS_KEY, null),
  };
}

// --- per-conversation selection -------------------------------------------------------------

export interface UseSelections {
  toolIds: number[];
  skills: SkillSummaryDTO[];
  kbIds: string[];
  toolCap: number;
  toggleTool: (tool: McpToolDTO) => boolean;
  toggleServerTools: (tools: McpToolDTO[]) => void;
  toggleSkill: (skill: SkillSummaryDTO) => boolean;
  removeSkill: (id: number) => void;
  toggleKb: (id: string) => boolean;
  removeKb: (id: string) => void;
  /** Starting a new chat explicitly clears the draft selection. */
  clearDraft: () => void;
  /** A draft chat became this conversation. */
  adopt: (conversationId: string) => void;
}

export function useSelections(args: {
  key: string;
  userId: string;
  project: ProjectDTO | null;
  newChatRevision: number;
  tools: McpToolDTO[];
  toolsLoaded: boolean;
  cap: number;
  userDefaultToolIds: number[];
  kbEnabled: boolean;
  authed: boolean;
}): UseSelections {
  const { key, userId, project, newChatRevision, tools, toolsLoaded, cap, userDefaultToolIds, kbEnabled, authed } = args;
  const t = useT();
  const store = useRef(new Map<string, Sel>());
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const keyRef = useRef(key);
  keyRef.current = key;
  const manual = useRef(new Set<string>());
  const defaultsApplied = useRef(new Set<string>());

  const persistKey = storageKeyFor(key);
  const isNew = key === DRAFT_KEY || key === TEMP_KEY;

  const read = useCallback(
    (k: string): Sel => {
      let v = store.current.get(k);
      if (!v) {
        const sk = storageKeyFor(k);
        const saved = sk && userId ? loadSelection(browserIO, userId, sk, Date.now()) : null;
        v = saved ? { toolIds: saved.toolIds, skills: saved.skills, kbIds: saved.knowledgeBaseIds } : EMPTY;
        store.current.set(k, v);
      }
      return v;
    },
    [userId],
  );

  const write = useCallback(
    (k: string, next: Sel, byUser: boolean) => {
      store.current.set(k, next);
      if (byUser) manual.current.add(k === TEMP_KEY || k === DRAFT_KEY ? `${k}:${contextOf(k)}` : k);
      const sk = storageKeyFor(k);
      if (sk && userId) saveSelection(browserIO, userId, sk, { toolIds: next.toolIds, skills: next.skills, knowledgeBaseIds: next.kbIds }, Date.now());
      if (k === keyRef.current) bump();
    },
    [userId],
  );

  const contextRef = useRef("");
  const contextOf = (_k: string) => contextRef.current;
  contextRef.current = `${newChatRevision}:${project?.publicID ?? ""}`;

  // Defaults for a new chat, once per context (revision + project).
  const context = `${key}:${newChatRevision}:${project?.publicID ?? ""}`;
  useEffect(() => {
    if (!isNew || !toolsLoaded || !authed) return;
    if (defaultsApplied.current.has(context)) return;
    defaultsApplied.current.add(context);
    const ctx = `${key}:${contextRef.current}`;
    if (manual.current.has(ctx)) return;
    const toolIds = project ? (project.mcpDefaultMode.toLowerCase() === "custom" ? project.defaultMCPToolIDs : userDefaultToolIds) : userDefaultToolIds;
    const normalized = normalizeTools(toolIds, tools, cap);
    const base: Sel = { toolIds: normalized, skills: [], kbIds: [] };
    // Not cancelled when unrelated inputs (the tool list, the project object) are refreshed
    // mid-fetch: it only stops once the new-chat context itself moved on or the user chose.
    const apply = (next: Sel) => {
      if (`${keyRef.current}:${contextRef.current}` !== ctx || manual.current.has(ctx)) return;
      write(key, next, false);
    };
    apply(base);
    if (project) {
      void (async () => {
        let skills: SkillSummaryDTO[] = [];
        let kbIds: string[] = [];
        try {
          if (project.defaultSkillIDs.length > 0) {
            const wanted = project.defaultSkillIDs.slice(0, 128);
            const found = new Map<number, SkillSummaryDTO>();
            for (let i = 0; i < wanted.length; i += 100) {
              const res = await listSkills({ ids: wanted.slice(i, i + 100), pageSize: 100 });
              for (const s of res.results) if (s.enabled) found.set(s.id, s);
            }
            skills = wanted.map((id) => found.get(id)).filter((s): s is SkillSummaryDTO => !!s);
          }
        } catch {
          /* defaults are best effort */
        }
        try {
          if (kbEnabled && project.defaultKnowledgeBaseIDs.length > 0) {
            const res = await listKnowledgeBases({ pageSize: 50 });
            const ready = new Set(res.results.filter((k) => k.readyFileCount > 0).map((k) => k.publicID));
            kbIds = project.defaultKnowledgeBaseIDs.filter((id) => ready.has(id)).slice(0, MAX_KNOWLEDGE_BASES);
          }
        } catch {
          /* ignore */
        }
        apply({ ...base, skills, kbIds });
      })();
    }
  }, [context, isNew, toolsLoaded, authed, key, project, userDefaultToolIds, tools, cap, kbEnabled, write]);

  // Keep the stored tool ids in line with what is available.
  const current = read(key);
  useEffect(() => {
    if (!toolsLoaded) return;
    const normalized = normalizeTools(current.toolIds, tools, cap);
    if (normalized.length !== current.toolIds.length || normalized.some((id, i) => id !== current.toolIds[i])) {
      write(key, { ...current, toolIds: normalized }, false);
    }
  }, [tools, toolsLoaded, cap, key, current, write]);

  // Knowledge bases that are no longer ready (or no longer visible) leave the selection.
  const kbChecked = useRef("");
  const kbSignature = current.kbIds.join(",");
  useEffect(() => {
    if (!kbEnabled || !authed || !kbSignature) return;
    const mark = `${key}|${kbSignature}`;
    if (kbChecked.current === mark) return;
    kbChecked.current = mark;
    void (async () => {
      try {
        const ready = new Set<string>();
        let total = Infinity;
        for (let page = 1; (page - 1) * KB_PAGE < total && page <= KB_MAX_PAGES; page++) {
          const res = await listKnowledgeBases({ page, pageSize: KB_PAGE });
          total = res.total;
          for (const kb of res.results) if (kb.readyFileCount > 0) ready.add(kb.publicID);
          if (res.results.length === 0) break;
        }
        const now = read(key);
        const kept = now.kbIds.filter((id) => ready.has(id));
        if (kept.length !== now.kbIds.length) {
          kbChecked.current = `${key}|${kept.join(",")}`; // what is left was just checked
          write(key, { ...now, kbIds: kept }, false);
        }
      } catch {
        kbChecked.current = ""; // checked again on the next change
      }
    })();
  }, [kbEnabled, authed, key, kbSignature, read, write]);

  const toggleToolFn = useCallback(
    (tool: McpToolDTO): boolean => {
      const cur = read(keyRef.current);
      const res = toggleTool(cur.toolIds, tool, tools, cap);
      if (!res.ok) {
        if (res.reason === "limit") toast.error(t("chatComposer.tools.limitTitle"), { description: t("chatComposer.tools.limitDescription", { limit: cap }) });
        else toast.error(t("chatComposer.tools.processorTitle"), { description: t("chatComposer.tools.processorDescription") });
        return false;
      }
      write(keyRef.current, { ...cur, toolIds: res.ids }, true);
      return true;
    },
    [cap, read, t, tools, write],
  );

  const toggleServerTools = useCallback(
    (serverTools: McpToolDTO[]) => {
      const cur = read(keyRef.current);
      const res = toggleServer(cur.toolIds, serverTools, tools, cap);
      if (res.hitLimit) toast.error(t("chatComposer.tools.limitTitle"), { description: t("chatComposer.tools.limitDescription", { limit: cap }) });
      else if (res.skippedProcessor) toast.error(t("chatComposer.tools.processorTitle"), { description: t("chatComposer.tools.processorDescription") });
      write(keyRef.current, { ...cur, toolIds: res.ids }, true);
    },
    [cap, read, t, tools, write],
  );

  const toggleSkillFn = useCallback(
    (skill: SkillSummaryDTO): boolean => {
      const cur = read(keyRef.current);
      const res = toggleSkill(cur.skills, skill, cap);
      if (!res.ok) {
        toast.error(t("chatComposer.skills.limitTitle"), { description: t("chatComposer.skills.limitDescription", { limit: cap }) });
        return false;
      }
      write(keyRef.current, { ...cur, skills: res.list }, true);
      return true;
    },
    [cap, read, t, write],
  );

  const removeSkill = useCallback(
    (id: number) => {
      const cur = read(keyRef.current);
      write(keyRef.current, { ...cur, skills: cur.skills.filter((s) => s.id !== id) }, true);
    },
    [read, write],
  );

  const toggleKb = useCallback(
    (id: string): boolean => {
      const cur = read(keyRef.current);
      const res = toggleKnowledgeBase(cur.kbIds, id);
      if (!res.ok) {
        toast.error(t("chatComposer.kb.limitTitle", { limit: MAX_KNOWLEDGE_BASES }));
        return false;
      }
      write(keyRef.current, { ...cur, kbIds: res.ids }, true);
      return true;
    },
    [read, t, write],
  );

  const removeKb = useCallback(
    (id: string) => {
      const cur = read(keyRef.current);
      write(keyRef.current, { ...cur, kbIds: cur.kbIds.filter((x) => x !== id) }, true);
    },
    [read, write],
  );

  const clearDraft = useCallback(() => {
    store.current.set(DRAFT_KEY, EMPTY);
    manual.current.clear();
    defaultsApplied.current.clear();
    if (userId) saveSelection(browserIO, userId, NEW_CHAT_KEY, { toolIds: [], skills: [], knowledgeBaseIds: [] }, Date.now());
    bump();
  }, [userId]);

  const adopt = useCallback(
    (conversationId: string) => {
      const draft = store.current.get(DRAFT_KEY);
      if (draft) store.current.set(conversationId, draft);
      store.current.delete(DRAFT_KEY);
      if (userId) moveSelection(browserIO, userId, NEW_CHAT_KEY, conversationId, Date.now());
      bump();
    },
    [userId],
  );

  void persistKey;
  return {
    toolIds: current.toolIds,
    skills: current.skills,
    kbIds: current.kbIds,
    toolCap: cap,
    toggleTool: toggleToolFn,
    toggleServerTools,
    toggleSkill: toggleSkillFn,
    removeSkill,
    toggleKb,
    removeKb,
    clearDraft,
    adopt,
  };
}

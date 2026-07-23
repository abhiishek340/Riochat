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

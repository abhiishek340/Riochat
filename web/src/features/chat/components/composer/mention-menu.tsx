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

/**
 * Builders for the bodies the generation endpoints expect. They are pure: the
 * caller supplies the already-decided task, ids and selections.
 */
import type { BranchReason, ChatAttachment } from "../types";
import { extensionOptions, type TaskKind } from "./task-decision";
import { stripReserved, type Options } from "./options";
import type { ControlChoices } from "./controls";

export const LIMITS = {
  files: 20,
  knowledgeBases: 8,
  tools: 128,
  skills: 128,
  uiComponents: 32,
  uiStates: 32,
  tempHistory: 99,
  tempFiles: 20,
  tempImages: 10,
  concurrentRuns: 5,
} as const;

export const UI_BLOCK_VERSION = 2;

export interface UiStateEntry {
  messagePublicID: string;
  blockID: string;
  title?: string;
  state: Record<string, unknown>;
}

export interface Selections {
  toolIds: number[];
  skillIds: number[];
  knowledgeBaseIds: string[];
  uiComponentIds: number[];
  visualPrompt: boolean;
}

export const EMPTY_SELECTIONS: Selections = { toolIds: [], skillIds: [], knowledgeBaseIds: [], uiComponentIds: [], visualPrompt: false };

export interface SendInput {
  task: TaskKind;
  content: string;
  attachments: readonly ChatAttachment[];
  model: string;
  options: Options;
  /** "disabled" suppresses every option. */
  optionPolicyMode?: string;
  controls?: ControlChoices;
  clientRunId: string;
  parentId: string | null;
  sourceId: string | null;
  branchReason: BranchReason;
  selections: Selections;
  uiStates: readonly UiStateEntry[];
}

/** Options as they go on the wire: reserved keys removed, nothing when disabled or empty. */
export function wireOptions(options: Options, policyMode?: string): Options | undefined {
  if (policyMode === "disabled") return undefined;
  const clean = stripReserved(options);
  return Object.keys(clean).length > 0 ? clean : undefined;
}

export function keepNewest<T>(list: readonly T[], max: number): T[] {
  return list.length > max ? list.slice(list.length - max) : [...list];
}

export function buildChatBody(input: SendInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    content: input.content,
    contentType: input.attachments.length > 0 ? "mixed" : "text",
    model: input.model,
    clientRunID: input.clientRunId,
    branchReason: input.branchReason,
    knowledgeBaseIDs: input.selections.knowledgeBaseIds.slice(0, LIMITS.knowledgeBases),
  };
  const options = wireOptions(input.options, input.optionPolicyMode);
  if (options) body.options = options;
  if (input.controls && Object.keys(input.controls).length > 0) body.controls = input.controls;
  if (input.attachments.length > 0) body.fileIDs = input.attachments.slice(0, LIMITS.files).map((a) => a.fileId);
  if (input.parentId) body.parentMessagePublicID = input.parentId;
  if (input.sourceId) body.sourceMessagePublicID = input.sourceId;
  if (input.selections.toolIds.length > 0) body.selectedToolIDs = input.selections.toolIds.slice(0, LIMITS.tools);
  if (input.selections.skillIds.length > 0) body.skillIDs = input.selections.skillIds.slice(0, LIMITS.skills);
  if (input.selections.visualPrompt) body.htmlVisualPrompt = true;
  if (input.selections.uiComponentIds.length > 0) {
    body.uiComponentIDs = input.selections.uiComponentIds.slice(0, LIMITS.uiComponents);
    body.uiBlockVersion = UI_BLOCK_VERSION;
  }
  if (input.uiStates.length > 0) body.uiStates = keepNewest(input.uiStates, LIMITS.uiStates);
  return body;
}

export function buildMediaBody(input: SendInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    prompt: input.content,
    model: input.model,
    clientRunID: input.clientRunId,
    branchReason: input.branchReason,
  };
  if (input.task === "video_extension") {
    body.options = extensionOptions(input.options);
    const source = input.attachments[0];
    if (source) body.sourceVideoFileID = source.fileId;
  } else {
    const options = wireOptions(input.options, input.optionPolicyMode);
    if (options) body.options = options;
    if (input.attachments.length > 0) body.fileIDs = input.attachments.slice(0, LIMITS.files).map((a) => a.fileId);
  }
  if (input.parentId) body.parentMessagePublicID = input.parentId;
  if (input.sourceId) body.sourceMessagePublicID = input.sourceId;
  return body;
}

export function buildSendBody(input: SendInput): Record<string, unknown> {
  return input.task === "chat" ? buildChatBody(input) : buildMediaBody(input);
}

// --- temporary chat ---------------------------------------------------------------------

export interface TempHistoryEntry {
  role: "user" | "assistant";
  content: string;
  /** Local files attached to a user entry. */
  files?: File[];
}

export interface TempBuildInput {
  sessionId: string;
  clientRunId: string;
  model: string;
  options: Options;
  optionPolicyMode?: string;
  controls?: ControlChoices;
  selections: Selections;
  /** History before the new turn. */
  history: readonly TempHistoryEntry[];
  /** The new user turn. */
  turn: TempHistoryEntry;
  uiStates: readonly { messageIndex: number; blockID: string; title?: string; state: Record<string, unknown> }[];
  omittedNote: string;
  attachmentNote: (names: string[]) => string;
}

export interface TempRequest {
  payload: Record<string, unknown>;
  files: { file: File; messageIndex: number }[];
  truncated: boolean;
}

/**
 * The whole local conversation, capped: at most 99 history entries plus the new
 * turn; a truncated history says so in its first entry. Attachments: newest
 * first, at most 20 files and 10 images; what does not fit is named in a note
 * on its message.
 */
export function buildTempRequest(input: TempBuildInput): TempRequest {
  const entries: TempHistoryEntry[] = [...input.history, input.turn];
  const cut = Math.max(0, entries.length - (LIMITS.tempHistory + 1));
  const kept = entries.slice(cut);
  const truncated = cut > 0;
  const offset = cut;

  type Candidate = { file: File; index: number; order: number; image: boolean };
  const candidates: Candidate[] = [];
  let order = 0;
  kept.forEach((e, index) => {
    for (const file of e.files ?? []) candidates.push({ file, index, order: order++, image: file.type.startsWith("image/") });
  });
  const chosen = new Set<Candidate>();
  let images = 0;
  for (const c of [...candidates].sort((a, b) => b.order - a.order)) {
    if (chosen.size >= LIMITS.tempFiles) break;
    if (c.image) {
      if (images >= LIMITS.tempImages) continue;
      images++;
    }
    chosen.add(c);
  }
  const omittedByIndex = new Map<number, string[]>();
  for (const c of candidates) {
    if (chosen.has(c)) continue;
    const list = omittedByIndex.get(c.index) ?? [];
    list.push(c.file.name);
    omittedByIndex.set(c.index, list);
  }

  const messages = kept.map((e, i) => {
    let content = e.content;
    const omitted = omittedByIndex.get(i);
    if (omitted && omitted.length > 0) content = `${content}\n\n${input.attachmentNote(omitted)}`;
    if (i === 0 && truncated) content = `${input.omittedNote}\n\n${content}`;
    return { role: e.role, content };
  });

  const payload: Record<string, unknown> = {
    sessionID: input.sessionId,
    clientRunID: input.clientRunId,
    model: input.model,
    knowledgeBaseIDs: input.selections.knowledgeBaseIds.slice(0, LIMITS.knowledgeBases),
    messages,
  };
  const options = wireOptions(input.options, input.optionPolicyMode);
  if (options) payload.options = options;
  if (input.controls && Object.keys(input.controls).length > 0) payload.controls = input.controls;
  if (input.selections.toolIds.length > 0) payload.selectedToolIDs = input.selections.toolIds.slice(0, LIMITS.tools);
  if (input.selections.skillIds.length > 0) payload.skillIDs = input.selections.skillIds.slice(0, LIMITS.skills);
  if (input.selections.visualPrompt) payload.htmlVisualPrompt = true;
  if (input.selections.uiComponentIds.length > 0) {
    payload.uiComponentIDs = input.selections.uiComponentIds.slice(0, LIMITS.uiComponents);
    payload.uiBlockVersion = UI_BLOCK_VERSION;
  }
  const states = input.uiStates
    .map((s) => ({ ...s, messageIndex: s.messageIndex - offset }))
    .filter((s) => s.messageIndex >= 0 && s.messageIndex < kept.length);
  if (states.length > 0) payload.uiStates = keepNewest(states, LIMITS.uiStates);

  const files = [...chosen].sort((a, b) => a.order - b.order).map((c) => ({ file: c.file, messageIndex: c.index }));
  return { payload, files, truncated };
}

/** Multipart form for a temporary request with attachments. */
export function tempFormData(req: TempRequest): FormData {
  const form = new FormData();
  form.append("payload", JSON.stringify(req.payload));
  form.append("attachmentMessageIndexes", JSON.stringify(req.files.map((f) => f.messageIndex)));
  for (const f of req.files) form.append("attachments", f.file, f.file.name);
  return form;
}

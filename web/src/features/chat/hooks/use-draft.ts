"use client";

/**
 * The composer's text and attachments for one conversation key, with browser
 * persistence, uploads, policy checks and processing-status polling.
 */
import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { toast } from "@/components/ui";
import { fileProcessingStatuses, uploadFile } from "../api";
import { attachmentFailed, attachmentFromFile, isLocalAttachment, localAttachment, mergeAttachments } from "../lib/attachments";
import { checkFile, fitToSlots, isAllowedInTemporary, isProcessing, messageFileCap, type FileRejection } from "../lib/file-policy";
import { formatBytes } from "../lib/format";
import { appendToDraft, browserIO, loadDraft, NEW_CHAT_KEY, removeDraft, saveDraft } from "../lib/persist";
import type { ChatAttachment, FilePolicyDTO } from "../types";
import { DRAFT_KEY, TEMP_KEY } from "../state/engine-types";
import { useT, type Translate } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";

export interface DraftValue {
  text: string;
  attachments: ChatAttachment[];
}

export interface Uploading {
  id: string;
  name: string;
}

const EMPTY: DraftValue = { text: "", attachments: [] };
const PERSIST_DELAY = 250;
const UPLOAD_CONCURRENCY = 3;
const POLL_MS = 1500;

export function storageKeyFor(key: string): string | null {
  if (key === TEMP_KEY) return null;
  return key === DRAFT_KEY ? NEW_CHAT_KEY : key;
}

function rejectionText(t: Translate, r: FileRejection): string {
  if (r.reason === "type") return t("chatComposer.attach.reasonType");
  const limit = formatBytes(r.limit);
  return r.reason === "fullContextOnly" ? t("chatComposer.attach.reasonFullContext", { limit }) : t("chatComposer.attach.reasonSize", { limit });
}

export interface UseDraft {
  text: string;
  attachments: ChatAttachment[];
  uploading: Uploading[];
  setText: (text: string) => void;
  /** Adds files (upload, or local handles in temporary mode). */
  addFiles: (files: File[]) => Promise<ChatAttachment[]>;
  /** Attaches existing library files or generated results. */
  addExisting: (list: ChatAttachment[]) => void;
  removeAttachment: (fileId: string) => void;
  /** Takes everything out of the composer, handing previews to the caller. */
  take: () => DraftValue;
  /** Puts a taken draft back. */
  restore: (value: DraftValue) => void;
  clear: () => void;
  busy: boolean;
}

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

export function useDraft(args: {
  key: string;
  userId: string;
  policy: FilePolicyDTO | null;
  preserve: boolean;
  authed: boolean;
}): UseDraft {
  const { key, userId, policy, preserve, authed } = args;
  const t = useT();
  const errorMessage = useErrorMessage();
  const store = useRef(new Map<string, DraftValue>());
  const uploadingRef = useRef(new Map<string, Uploading[]>());
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const keyRef = useRef(key);
  keyRef.current = key;
  const notifiedFailures = useRef(new Set<string>());

  const persistKey = storageKeyFor(key);

  const read = useCallback(
    (k: string): DraftValue => {
      let v = store.current.get(k);
      if (!v) {
        const sk = storageKeyFor(k);
        const saved = sk && preserve && userId ? loadDraft(browserIO, userId, sk, Date.now()) : null;
        v = saved ? { text: saved.text, attachments: saved.attachments } : EMPTY;
        store.current.set(k, v);
      }
      return v;
    },
    [preserve, userId],
  );

  const flush = useCallback(
    (k: string) => {
      const pending = timers.current.get(k);
      if (pending) clearTimeout(pending);
      timers.current.delete(k);
      const sk = storageKeyFor(k);
      if (!sk || !userId) return;
      const v = store.current.get(k) ?? EMPTY;
      if (!preserve) removeDraft(browserIO, userId, sk, Date.now());
      else saveDraft(browserIO, userId, sk, v, Date.now());
    },
    [preserve, userId],
  );

  const schedule = useCallback(
    (k: string) => {
      if (!storageKeyFor(k)) return;
      const pending = timers.current.get(k);
      if (pending) clearTimeout(pending);
      timers.current.set(
        k,
        setTimeout(() => flush(k), PERSIST_DELAY),
      );
    },
    [flush],
  );

  // Flush the previous key's draft when the key changes and everything on unmount.
  const prevKey = useRef(key);
  useEffect(() => {
    if (prevKey.current !== key) {
      flush(prevKey.current);
      prevKey.current = key;
    }
  }, [key, flush]);
  useEffect(() => {
    const map = timers.current;
    return () => {
      for (const k of [...map.keys()]) flush(k);
    };
  }, [flush]);

  // A change of the persistence setting drops what is stored.
  useEffect(() => {
    if (!preserve && persistKey && userId) removeDraft(browserIO, userId, persistKey, Date.now());
  }, [preserve, persistKey, userId]);

  // Other tabs wrote the draft: forget the cached copy.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key && e.key.startsWith("riochat:drafts:")) {
        const timersActive = timers.current.size > 0;
        if (!timersActive) {
          store.current.clear();
          bump();
        }
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const current = read(key);

  const update = useCallback(
    (k: string, fn: (v: DraftValue) => DraftValue) => {
      store.current.set(k, fn(read(k)));
      schedule(k);
      if (k === keyRef.current) bump();
    },
    [read, schedule],
  );

  const setText = useCallback((text: string) => update(keyRef.current, (v) => ({ ...v, text })), [update]);

  const cap = messageFileCap(policy);

  const setUploading = useCallback((k: string, fn: (list: Uploading[]) => Uploading[]) => {
    uploadingRef.current.set(k, fn(uploadingRef.current.get(k) ?? []));
    if (k === keyRef.current) bump();
  }, []);

  const addFiles = useCallback(
    async (input: File[]): Promise<ChatAttachment[]> => {
      const target = keyRef.current;
      if (input.length === 0) return [];
      const temporary = target === TEMP_KEY;
      const have = read(target).attachments.length + (uploadingRef.current.get(target)?.length ?? 0);
      const free = cap - have;
      if (free <= 0) {
        toast.error(t("chatComposer.attach.limitReached", { count: cap }));
        return [];
      }
      const { accepted: fitted, ignored } = fitToSlots(input, free);
      if (ignored > 0) toast.warning(t("chatComposer.attach.truncated", { count: ignored }));

      // client-side policy
      const ok: File[] = [];
      const rejected: string[] = [];
      for (const file of fitted) {
        if (temporary && !isAllowedInTemporary(file)) {
          rejected.push(`${file.name}: ${t("chatComposer.attach.tempNoMedia")}`);
          continue;
        }
        const r = checkFile(policy, file);
        if (r) rejected.push(`${file.name}: ${rejectionText(t, r)}`);
        else ok.push(file);
      }
      if (rejected.length > 0) toast.error(t("chatComposer.attach.rejectedTitle"), { description: rejected.join("\n") });
      if (ok.length === 0) return [];

      if (temporary) {
        const items = ok.map((f) => localAttachment(f, f.type.startsWith("image/") ? URL.createObjectURL(f) : undefined));
        update(target, (v) => ({ ...v, attachments: mergeAttachments(v.attachments, items, cap).list }));
        return items;
      }
      if (!authed) {
        toast.error(t("chatComposer.attach.signIn"));
        return [];
      }

      const placeholders = ok.map((f) => ({ id: `up-${Math.random().toString(36).slice(2)}`, name: f.name }));
      setUploading(target, (list) => [...list, ...placeholders]);
      const results: { attachment: ChatAttachment; reused: boolean }[] = [];
      const failures: unknown[] = [];
      let next = 0;
      const worker = async () => {
        while (next < ok.length) {
          const i = next++;
          const file = ok[i] as File;
          const ph = placeholders[i] as Uploading;
          try {
            const res = await uploadFile(file);
            const preview = file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined;
            results.push({ attachment: attachmentFromFile(res.file, preview), reused: res.reused });
          } catch (error) {
            failures.push(error);
          } finally {
            setUploading(target, (list) => list.filter((u) => u.id !== ph.id));
          }
        }
      };
      await Promise.all(Array.from({ length: Math.min(UPLOAD_CONCURRENCY, ok.length) }, worker));

      const incoming = results.map((r) => r.attachment);
      if (incoming.length > 0) {
        if (keyRef.current === target || !persistKeyOf(target)) {
          update(target, (v) => {
            const merged = mergeAttachments(v.attachments, incoming, cap);
            for (const a of incoming) if (a.previewUrl && !merged.added.includes(a)) URL.revokeObjectURL(a.previewUrl);
            return { ...v, attachments: merged.list };
          });
        } else {
          // The user moved on: the finished upload joins the stored draft of the conversation it was meant for.
          const sk = storageKeyFor(target);
          if (sk && userId) appendToDraft(browserIO, userId, sk, incoming, Date.now());
          store.current.delete(target);
          for (const a of incoming) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
        }
        if (results.some((r) => r.reused)) toast.info(t("chatComposer.attach.duplicate"));
      }
      if (failures.length > 0) {
        const reason = errorMessage(failures[0]);
        if (incoming.length === 0) toast.error(t("chatComposer.attach.uploadFailed"), { description: reason });
        else toast.warning(t("chatComposer.attach.partialFailed", { reason }));
      }
      return incoming;
    },
    [authed, cap, errorMessage, policy, read, setUploading, t, update, userId],
  );

  const addExisting = useCallback(
    (list: ChatAttachment[]) => {
      const target = keyRef.current;
      const have = read(target).attachments;
      if (have.length >= cap && list.some((a) => !have.some((h) => h.fileId === a.fileId))) {
        toast.error(t("chatComposer.attach.limitReached", { count: cap }));
        return;
      }
      update(target, (v) => ({ ...v, attachments: mergeAttachments(v.attachments, list, cap).list }));
    },
    [cap, read, t, update],
  );

  const removeAttachment = useCallback(
    (fileId: string) =>
      update(keyRef.current, (v) => {
        const gone = v.attachments.find((a) => a.fileId === fileId);
        if (gone?.previewUrl) URL.revokeObjectURL(gone.previewUrl);
        return { ...v, attachments: v.attachments.filter((a) => a.fileId !== fileId) };
      }),
    [update],
  );

  const take = useCallback((): DraftValue => {
    const v = read(keyRef.current);
    store.current.set(keyRef.current, EMPTY);
    flush(keyRef.current);
    bump();
    return v;
  }, [flush, read]);

  const restore = useCallback(
    (value: DraftValue) => {
      update(keyRef.current, (v) => ({
        text: v.text ? v.text : value.text,
        attachments: mergeAttachments(value.attachments, v.attachments, Math.max(cap, value.attachments.length)).list,
      }));
    },
    [cap, update],
  );

  const clear = useCallback(() => {
    const v = read(keyRef.current);
    for (const a of v.attachments) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
    store.current.set(keyRef.current, EMPTY);
    flush(keyRef.current);
    bump();
  }, [flush, read]);

  // Release temporary-chat file handles and previews when leaving.
  useEffect(() => {
    const map = store.current;
    return () => {
      const temp = map.get(TEMP_KEY);
      if (temp) for (const a of temp.attachments) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
    };
  }, []);

  // --- processing status polling ----------------------------------------------------
  const processingIds = useMemo(
    () => current.attachments.filter((a) => !isLocalAttachment(a) && (isProcessing(a.processingStatus, a.embedStatus) || (!a.processingReady && !attachmentFailed(a)))).map((a) => a.fileId),
    [current.attachments],
  );
  const idsKey = processingIds.join(",");
  useEffect(() => {
    if (!idsKey || !authed) return;
    const ids = idsKey.split(",");
    const target = key;
    let live = true;
    const tick = async () => {
      try {
        const rows: Awaited<ReturnType<typeof fileProcessingStatuses>> = [];
        for (let i = 0; i < ids.length; i += 100) rows.push(...(await fileProcessingStatuses(ids.slice(i, i + 100))));
        if (!live) return;
        const byId = new Map(rows.map((r) => [r.fileID, r] as const));
        update(target, (v) => ({
          ...v,
          attachments: v.attachments
            .filter((a) => !ids.includes(a.fileId) || byId.has(a.fileId))
            .map((a) => {
              const r = byId.get(a.fileId);
              if (!r) return a;
              const next: ChatAttachment = {
                ...a,
                processingStatus: r.processingStatus,
                processingReady: r.processingReady,
                processingErrorCode: r.errorCode,
                processingErrorMessage: r.errorMessage ?? "",
                embedStatus: r.embedStatus,
                extractStatus: r.extractStatus,
                ragReady: r.ragReady,
              };
              if (attachmentFailed(next) && !notifiedFailures.current.has(a.fileId)) {
                notifiedFailures.current.add(a.fileId);
                toast.error(t("chatComposer.attach.processingFailed", { name: a.fileName, reason: next.processingErrorMessage || next.processingErrorCode }));
              }
              return next;
            }),
        }));
      } catch {
        /* retried on the next tick */
      }
    };
    const timer = setInterval(tick, POLL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [idsKey, authed, key, update, t]);

  const uploading = uploadingRef.current.get(key) ?? [];
  return {
    text: current.text,
    attachments: current.attachments,
    uploading,
    setText,
    addFiles,
    addExisting,
    removeAttachment,
    take,
    restore,
    clear,
    busy: uploading.length > 0,
  };
}

function persistKeyOf(k: string): string | null {
  return storageKeyFor(k);
}

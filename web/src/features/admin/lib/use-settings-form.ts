"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api/client";
import { isAbortError } from "@/lib/api/errors";
import { useNotify } from "./use-notify";
import type { Problem } from "./problem";
import {
  buildDraft,
  buildPatch,
  dirtyFields,
  fieldId,
  isVisible,
  rebase,
  type Draft,
  type FieldDef,
  type SettingsTree,
} from "./settings-form";

export async function fetchSettings(signal?: AbortSignal): Promise<SettingsTree> {
  const tree = await api.get<SettingsTree | null>("/admin/settings", { signal });
  return tree ?? {};
}

export async function patchSettings(items: Array<{ namespace: string; key: string; value: string }>): Promise<SettingsTree> {
  const tree = await api.patch<SettingsTree | null>("/admin/settings", { items });
  return tree ?? {};
}

export interface SettingsFormOptions {
  fields: readonly FieldDef[];
  /** Cascading rules, applied after every edit and to freshly loaded values. */
  normalize?: (next: Draft, changedId: string | null, prev: Draft) => Draft;
  enabled?: boolean;
}

export interface SaveOptions {
  /** Fields sent even when unchanged (companion values). */
  force?: readonly FieldDef[];
  validate?: (draft: Draft, configured: ReadonlySet<string>) => Problem | null;
  /** Skip the generic success toast. */
  silent?: boolean;
}

export interface SaveResult {
  ok: boolean;
  tree?: SettingsTree;
  /** Fields that were part of the request. */
  sent: FieldDef[];
}

/** Loads the settings tree once and keeps drafts for the fields of a screen. */
export function useSettingsForm({ fields, normalize, enabled = true }: SettingsFormOptions) {
  const notify = useNotify();
  const normalizeRef = useRef(normalize);
  normalizeRef.current = normalize;
  const [saved, setSaved] = useState<Draft>({});
  const [draft, setDraft] = useState<Draft>({});
  const [configured, setConfigured] = useState<Set<string>>(new Set());
  const [tree, setTree] = useState<SettingsTree | null>(null);
  const [loading, setLoading] = useState(enabled);
  const [failed, setFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [nonce, setNonce] = useState(0);
  const savingRef = useRef(false);
  const fieldsRef = useRef(fields);
  fieldsRef.current = fields;

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setLoading(true);
    fetchSettings(controller.signal).then(
      (next) => {
        const built = buildDraft(fieldsRef.current, next);
        const norm = normalizeRef.current;
        const base = norm ? norm(built.saved, null, built.saved) : built.saved;
        setTree(next);
        setSaved(base);
        setDraft(base);
        setConfigured(built.configured);
        setFailed(false);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setFailed(true);
        setLoading(false);
        notify.error("adminCommon.settings.loadFailed", error);
      },
    );
    return () => controller.abort();
    // `notify` is stable per translator; fields are read through a ref on purpose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, nonce]);

  const set = useCallback((id: string, value: string) => {
    setDraft((prev) => {
      const next = { ...prev, [id]: value };
      const norm = normalizeRef.current;
      return norm ? norm(next, id, prev) : next;
    });
  }, []);

  const dirtyOf = useCallback((group: readonly FieldDef[]) => dirtyFields(group, draft, saved), [draft, saved]);

  const revert = useCallback(
    (group: readonly FieldDef[]) => {
      setDraft((prev) => {
        const next = { ...prev };
        for (const f of group) next[fieldId(f)] = saved[fieldId(f)] ?? "";
        return next;
      });
    },
    [saved],
  );

  const save = useCallback(
    async (group: readonly FieldDef[], options: SaveOptions = {}): Promise<SaveResult> => {
      if (savingRef.current) return { ok: false, sent: [] };
      const norm = normalizeRef.current;
      const current = norm ? norm(draft, null, draft) : draft;
      const dirty = dirtyFields(group, current, saved);
      const force = (options.force ?? []).filter((f) => isVisible(f, current));
      const sent = [...dirty];
      for (const f of force) if (!sent.some((s) => fieldId(s) === fieldId(f))) sent.push(f);
      if (dirty.length === 0 && force.length === 0) return { ok: true, sent: [] };
      const problem = options.validate?.(current, configured);
      if (problem) {
        notify.invalid("adminCommon.settings.saveFailed", problem);
        return { ok: false, sent };
      }
      const { items, invalid } = buildPatch(sent, current);
      if (invalid.length > 0) {
        notify.invalid("adminCommon.settings.saveFailed", { key: "adminCommon.settings.invalidNumber", args: { field: invalid[0]?.key ?? "" } });
        return { ok: false, sent };
      }
      savingRef.current = true;
      setSaving(true);
      try {
        const next = await patchSettings(items);
        const built = buildDraft(fieldsRef.current, next);
        const reset = new Set(sent.map(fieldId));
        const nextSaved = norm ? norm(built.saved, null, built.saved) : built.saved;
        setDraft((prev) => rebase(saved, prev, nextSaved, reset));
        setSaved(nextSaved);
        setConfigured(built.configured);
        setTree(next);
        if (!options.silent) notify.success("adminCommon.settings.saved");
        return { ok: true, tree: next, sent };
      } catch (error) {
        notify.error("adminCommon.settings.saveFailed", error);
        return { ok: false, sent };
      } finally {
        savingRef.current = false;
        setSaving(false);
      }
    },
    [configured, draft, notify, saved],
  );

  /**
   * Writes explicit values for some fields (for example a merged list built by
   * a one-click fix) without touching the other drafts. The values go out as
   * given after the usual trimming.
   */
  const saveValues = useCallback(
    async (values: Readonly<Record<string, string>>): Promise<SaveResult> => {
      if (savingRef.current) return { ok: false, sent: [] };
      const sent = fieldsRef.current.filter((f) => values[fieldId(f)] !== undefined);
      if (sent.length === 0) return { ok: true, sent: [] };
      const items = sent.map((f) => ({ namespace: f.ns, key: f.key, value: f.keepWhitespace ? (values[fieldId(f)] as string) : (values[fieldId(f)] as string).trim() }));
      savingRef.current = true;
      setSaving(true);
      try {
        const next = await patchSettings(items);
        const built = buildDraft(fieldsRef.current, next);
        const norm = normalizeRef.current;
        const nextSaved = norm ? norm(built.saved, null, built.saved) : built.saved;
        const reset = new Set(sent.map(fieldId));
        setDraft((prev) => rebase(saved, prev, nextSaved, reset));
        setSaved(nextSaved);
        setConfigured(built.configured);
        setTree(next);
        notify.success("adminCommon.settings.saved");
        return { ok: true, tree: next, sent };
      } catch (error) {
        notify.error("adminCommon.settings.saveFailed", error);
        return { ok: false, sent };
      } finally {
        savingRef.current = false;
        setSaving(false);
      }
    },
    [notify, saved],
  );

  return useMemo(
    () => ({
      draft,
      saved,
      configured,
      tree,
      loading,
      failed,
      saving,
      set,
      setDraft,
      dirtyOf,
      revert,
      save,
      saveValues,
      reload: () => setNonce((n) => n + 1),
    }),
    [draft, saved, configured, tree, loading, failed, saving, set, dirtyOf, revert, save, saveValues],
  );
}

export type SettingsForm = ReturnType<typeof useSettingsForm>;

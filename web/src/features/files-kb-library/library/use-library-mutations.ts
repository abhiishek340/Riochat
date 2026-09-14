"use client";

import { useCallback, useState } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { hasSession } from "../shared/session";
import { createItem, deleteItem, getItem, setItemEnabled, updateItem } from "./api";
import { checkForm, LIMITS, upsert, type ItemForm, type LibraryItem, type LibraryKind } from "./model";

type Mutate = (fn: (prev: LibraryItem[]) => LibraryItem[]) => void;

/** Create, edit, switch and delete for one kind of library item. */
export function useLibraryMutations(kind: LibraryKind, mutateMine: Mutate) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [saving, setSaving] = useState(false);

  /** Validates and saves; resolves true when the dialog may close. */
  const save = useCallback(
    async (editing: LibraryItem | null, form: ItemForm): Promise<boolean> => {
      const check = checkForm(form);
      if (!check.ok) {
        toast.error(
          check.reason === "missing"
            ? t(`libraryWs.${kind}.needFields`)
            : t(`libraryWs.${kind}.tooLong`, { name: LIMITS.name, description: LIMITS.description, body: LIMITS.body }),
        );
        return false;
      }
      if (!(await hasSession())) return false;
      setSaving(true);
      try {
        const saved = editing ? await updateItem(kind, editing.id, check.value) : await createItem(kind, check.value);
        // The write response carries the body for skills; keep it for the next edit.
        mutateMine((prev) => upsert(prev, saved));
        toast.success(t(editing ? `libraryWs.${kind}.updated` : `libraryWs.${kind}.created`));
        return true;
      } catch (error) {
        toast.error(t(editing ? `libraryWs.${kind}.updateFailed` : `libraryWs.${kind}.createFailed`), { description: errorMessage(error) });
        return false;
      } finally {
        setSaving(false);
      }
    },
    [kind, mutateMine, t, errorMessage],
  );

  const toggle = useCallback(
    async (item: LibraryItem, enabled: boolean) => {
      const replace = (next: LibraryItem) => mutateMine((prev) => prev.map((i) => (i.id === next.id ? next : i)));
      if (!(await hasSession())) return;
      replace({ ...item, enabled });
      try {
        const saved = await setItemEnabled(kind, item.id, enabled);
        replace({ ...saved, body: saved.body ?? item.body });
      } catch (error) {
        replace(item);
        toast.error(t(`libraryWs.${kind}.updateFailed`), { description: errorMessage(error) });
      }
    },
    [kind, mutateMine, t, errorMessage],
  );

  /** Resolves true on success. */
  const remove = useCallback(
    async (item: LibraryItem): Promise<boolean> => {
      try {
        await deleteItem(kind, item.id);
        mutateMine((prev) => prev.filter((i) => i.id !== item.id));
        toast.success(t(`libraryWs.${kind}.deleted`));
        return true;
      } catch (error) {
        toast.error(t(`libraryWs.${kind}.deleteFailed`), { description: errorMessage(error) });
        return false;
      }
    },
    [kind, mutateMine, t, errorMessage],
  );

  /** The full object for the dialogs; list summaries may lack the body. */
  const loadFull = useCallback(
    async (item: LibraryItem): Promise<LibraryItem | null> => {
      if (item.body !== undefined) return item;
      try {
        return await getItem(kind, item.id);
      } catch (error) {
        toast.error(t(`libraryWs.${kind}.loadFailed`), { description: errorMessage(error) });
        return null;
      }
    },
    [kind, t, errorMessage],
  );

  return { save, saving, toggle, remove, loadFull };
}

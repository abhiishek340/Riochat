"use client";

import { useEffect, useState, type FormEvent } from "react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldGroup,
  FieldLabel,
  Input,
  Textarea,
} from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import type { KnowledgeBase } from "../shared/types";
import type { KbDraft } from "./api";

export const NAME_MAX = 80;
export const DESCRIPTION_MAX = 255;

/** Create (editing = null) or edit a knowledge base's name and description. */
export function KbEditorDialog({
  open,
  editing,
  saving,
  onSave,
  onClose,
}: {
  open: boolean;
  editing: KnowledgeBase | null;
  saving: boolean;
  /** Resolves true when the dialog may close. */
  onSave: (draft: KbDraft) => Promise<boolean>;
  onClose: () => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  useEffect(() => {
    if (open) {
      setName(editing?.name ?? "");
      setDescription(editing?.description ?? "");
    }
  }, [open, editing]);

  const canSave = name.trim() !== "" && !saving;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canSave) return;
    if (await onSave({ name, description })) onClose();
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !saving && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{editing ? t("kbWs.editor.editTitle") : t("kbWs.editor.createTitle")}</DialogTitle>
            <DialogDescription className="sr-only">{editing ? t("kbWs.editor.editTitle") : t("kbWs.editor.createTitle")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field required>
              <FieldLabel>{t("kbWs.editor.name")}</FieldLabel>
              <Input
                autoFocus
                value={name}
                maxLength={NAME_MAX}
                placeholder={t("kbWs.editor.namePlaceholder")}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel>{t("kbWs.editor.description")}</FieldLabel>
              <Textarea
                value={description}
                maxLength={DESCRIPTION_MAX}
                rows={3}
                placeholder={t("kbWs.editor.descriptionPlaceholder")}
                onChange={(e) => setDescription(e.target.value)}
              />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={onClose}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" disabled={!canSave} loading={saving}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

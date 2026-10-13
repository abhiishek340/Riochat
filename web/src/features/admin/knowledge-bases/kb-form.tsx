"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useSnapshot } from "../lib/use-snapshot";
import { createKnowledgeBase, updateKnowledgeBase, type KnowledgeBase } from "./api";

export function KbFormDialog({ target, onOpenChange, onSaved }: { target: KnowledgeBase | "new" | null; onOpenChange: (open: boolean) => void; onSaved: (kb: KnowledgeBase, created: boolean) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [needName, setNeedName] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (target === null) return;
    const kb = target === "new" ? null : target;
    setName(kb?.name ?? "");
    setDescription(kb?.description ?? "");
    setEnabled(kb ? kb.enabled : true);
    setNeedName(false);
    setError(null);
  }, [target]);

  const submit = async () => {
    if (busy) return;
    if (!name.trim()) {
      setNeedName(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const input = { name: name.trim(), description: description.trim(), enabled };
      const saved = existing ? await updateKnowledgeBase(existing.publicID, input) : await createKnowledgeBase(input);
      onSaved(saved, !existing);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={target !== null} onOpenChange={(open) => !busy && onOpenChange(open)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{existing ? t("adminKb.form.editTitle") : t("adminKb.form.createTitle")}</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field required invalid={needName}>
            <FieldLabel>{t("adminKb.form.name")}</FieldLabel>
            <Input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} autoComplete="off" />
            <FieldError>{needName ? t("adminKb.form.nameRequired") : null}</FieldError>
          </Field>
          <Field>
            <FieldLabel>{t("adminKb.form.description")}</FieldLabel>
            <Textarea rows={3} value={description} maxLength={255} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={enabled} onCheckedChange={setEnabled} />
            {t("adminKb.form.enabled")}
          </label>
          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

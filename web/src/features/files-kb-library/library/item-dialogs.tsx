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
  FieldHint,
  FieldLabel,
  Input,
  Switch,
  Textarea,
} from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { charCount, displayName, EMPTY_FORM, formFromItem, LIMITS, type ItemForm, type LibraryItem, type LibraryKind } from "./model";

/** Create (editing = null) or edit one of the user's own items. */
export function ItemEditorDialog({
  kind,
  open,
  editing,
  saving,
  onSave,
  onClose,
}: {
  kind: LibraryKind;
  open: boolean;
  editing: LibraryItem | null;
  saving: boolean;
  /** Resolves true when the dialog may close. */
  onSave: (form: ItemForm) => Promise<boolean>;
  onClose: () => void;
}) {
  const t = useT();
  const [form, setForm] = useState<ItemForm>(EMPTY_FORM);
  useEffect(() => {
    if (open) setForm(editing ? formFromItem(editing) : EMPTY_FORM);
  }, [open, editing]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (await onSave(form)) onClose();
  };
  const patch = (next: Partial<ItemForm>) => setForm((f) => ({ ...f, ...next }));
  const title = editing ? t(`libraryWs.${kind}.editTitle`) : t(`libraryWs.${kind}.createTitle`);

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !saving && onClose()}>
      <DialogContent className="max-w-2xl">
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{t(`libraryWs.${kind}.dialogBody`)}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field required>
              <FieldLabel>{t(`libraryWs.${kind}.nameLabel`)}</FieldLabel>
              <div className="flex items-center rounded-md border border-input bg-background shadow-sm focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/40">
                <span aria-hidden className="select-none pl-3 font-mono text-sm text-muted-foreground">
                  /
                </span>
                <Input
                  autoFocus
                  value={form.name}
                  placeholder={t(`libraryWs.${kind}.namePlaceholder`)}
                  className="border-0 bg-transparent pl-1 shadow-none focus-visible:ring-0"
                  onChange={(e) => patch({ name: e.target.value })}
                />
              </div>
            </Field>
            <Field>
              <FieldLabel>{t(`libraryWs.${kind}.descriptionLabel`)}</FieldLabel>
              <Input value={form.description} onChange={(e) => patch({ description: e.target.value })} />
            </Field>
            <Field required>
              <FieldLabel>{t(`libraryWs.${kind}.bodyLabel`)}</FieldLabel>
              <Textarea
                value={form.body}
                rows={10}
                className="font-mono text-[13px]"
                onChange={(e) => patch({ body: e.target.value })}
              />
              <FieldHint>
                {charCount(form.body)} / {LIMITS.body}
              </FieldHint>
            </Field>
            <Field>
              <div className="flex items-center justify-between gap-4">
                <div className="grid gap-0.5">
                  <FieldLabel>{t("libraryWs.form.enabledLabel")}</FieldLabel>
                  <FieldHint>{t("libraryWs.form.enabledHint")}</FieldHint>
                </div>
                <Switch checked={form.enabled} aria-label={t("libraryWs.form.enabledLabel")} onCheckedChange={(enabled) => patch({ enabled })} />
              </div>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={onClose}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={saving}>
              {saving ? t("common.actions.saving") : t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Read-only view of a built-in item. */
export function ItemViewDialog({ kind, item, onClose }: { kind: LibraryKind; item: LibraryItem | null; onClose: () => void }) {
  const t = useT();
  return (
    <Dialog open={item !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(`libraryWs.${kind}.viewTitle`)}</DialogTitle>
          <DialogDescription>{t("libraryWs.view.readOnly")}</DialogDescription>
        </DialogHeader>
        {item ? (
          <dl className="grid gap-3 text-sm">
            <div className="grid gap-1">
              <dt className="text-xs font-medium text-muted-foreground">{t(`libraryWs.${kind}.nameLabel`)}</dt>
              <dd className="font-mono font-semibold">/{displayName(item)}</dd>
            </div>
            <div className="grid gap-1">
              <dt className="text-xs font-medium text-muted-foreground">{t(`libraryWs.${kind}.descriptionLabel`)}</dt>
              <dd>{item.description || <span className="text-muted-foreground">{t("libraryWs.view.descriptionEmpty")}</span>}</dd>
            </div>
            <div className="grid gap-1">
              <dt className="text-xs font-medium text-muted-foreground">{t(`libraryWs.${kind}.bodyLabel`)}</dt>
              <dd>
                <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted p-3 font-mono text-[13px] leading-relaxed">
                  {item.body ?? ""}
                </pre>
              </dd>
            </div>
          </dl>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.actions.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

"use client";

import { Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, NativeSelect } from "@/components/ui/input";
import { resolveApiBase } from "@/lib/api/base-url";
import { publicWebOrigin } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { CopyButton } from "../parts";
import { buildImportLink, emptyEntry, MAX_LINK_ENTRIES, type LinkEntryDraft } from "./link-builder";
import { IMPORT_PROTOCOLS } from "./protocols";

/** Form that assembles a shareable import link. Nothing is sent anywhere. */
export function ImportLinkDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const nextId = useRef(1);
  const [entries, setEntries] = useState<LinkEntryDraft[]>([]);
  const [origin, setOrigin] = useState("");

  // Closing discards everything typed, keys included.
  useEffect(() => {
    if (open) {
      nextId.current = 2;
      setEntries([emptyEntry(1)]);
      setOrigin(publicWebOrigin(resolveApiBase()));
    } else {
      setEntries([]);
    }
  }, [open]);

  const link = useMemo(() => buildImportLink(origin, entries), [origin, entries]);
  const patch = (id: number, change: Partial<LinkEntryDraft>) => setEntries((list) => list.map((e) => (e.id === id ? { ...e, ...change } : e)));
  const multiple = entries.length > 1;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("settings.models.linkBuilder.title")}</DialogTitle>
          <DialogDescription>{t("settings.models.linkBuilder.description")}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-3">
          <div className="flex items-center gap-1.5">
            <Input
              readOnly
              value={link}
              aria-label={t("settings.models.linkBuilder.link")}
              className="font-mono text-[13px]"
              onFocus={(e) => e.currentTarget.select()}
            />
            <CopyButton value={link} variant="outline" size="icon" successMessage={t("settings.models.linkBuilder.copied")} />
          </div>
          <p className="text-[13px] text-muted-foreground">{multiple ? t("settings.models.linkBuilder.hintMulti") : t("settings.models.linkBuilder.hint")}</p>
        </div>

        <div className="grid max-h-[50vh] gap-3 overflow-y-auto pr-1">
          {entries.map((entry, index) => (
            <fieldset key={entry.id} className="grid gap-2.5 rounded-lg border p-3">
              <legend className="sr-only">{t("settings.models.linkBuilder.entry", { n: index + 1 })}</legend>
              <div className="flex items-center justify-between gap-2">
                {multiple ? <span className="text-sm font-medium">{t("settings.models.linkBuilder.entry", { n: index + 1 })}</span> : <span />}
                {multiple ? (
                  <Button variant="ghost" size="icon-sm" aria-label={t("settings.models.linkBuilder.remove", { n: index + 1 })} onClick={() => setEntries((l) => l.filter((e) => e.id !== entry.id))}>
                    <Trash2 />
                  </Button>
                ) : null}
              </div>
              <ParamRow label="url" required>
                <Input value={entry.url} autoComplete="off" spellCheck={false} placeholder="https://api.example.com/v1" onChange={(e) => patch(entry.id, { url: e.target.value })} />
              </ParamRow>
              <ParamRow label="key" required>
                <Input value={entry.key} autoComplete="off" spellCheck={false} placeholder="sk-…" onChange={(e) => patch(entry.id, { key: e.target.value })} />
              </ParamRow>
              <ParamRow label="protocol">
                <NativeSelect value={entry.protocol} onChange={(e) => patch(entry.id, { protocol: e.target.value })}>
                  <option value="">{t("settings.models.linkBuilder.auto")}</option>
                  {IMPORT_PROTOCOLS.map((p) => (
                    <option key={p} value={p}>
                      {t(`settings.models.protocols.${p}`)}
                    </option>
                  ))}
                </NativeSelect>
              </ParamRow>
              <ParamRow label="name">
                <Input value={entry.name} maxLength={64} autoComplete="off" spellCheck={false} onChange={(e) => patch(entry.id, { name: e.target.value })} />
              </ParamRow>
            </fieldset>
          ))}
        </div>

        <DialogFooter className="sm:justify-between">
          <Button
            variant="outline"
            disabled={entries.length >= MAX_LINK_ENTRIES}
            onClick={() => setEntries((l) => [...l, emptyEntry(nextId.current++)])}
          >
            <Plus />
            {t("settings.models.linkBuilder.addEntry")}
          </Button>
          <Button onClick={() => onOpenChange(false)}>{t("common.actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ParamRow({ label, required, children }: { label: string; required?: boolean; children: React.ReactNode }) {
  const t = useT();
  // The row is the control's label, so every input has an accessible name.
  return (
    <label className="grid items-center gap-1.5 sm:grid-cols-[7rem_1fr] sm:gap-3">
      <span className="flex items-center gap-1.5 font-mono text-[13px] text-muted-foreground">
        {label}
        {required ? <Badge tone="outline">{t("settings.models.linkBuilder.required")}</Badge> : null}
      </span>
      {children}
    </label>
  );
}

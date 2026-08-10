"use client";

import { ImagePlus, Trash2, Upload } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { ApiError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { ModelIcon } from "../components/model-icon";
import { useNotify } from "../lib/use-notify";
import { deleteIconAsset, listIconAssets, uploadIconAsset, type IconAsset } from "../llm-api";
import { iconFileProblem, readIconUsage } from "./rules";
import { Labeled } from "./ui";

/** A text field for an icon with a preview and a button that opens the picker. */
export function IconField({ value, onChange, name, label, fallback, disabled }: { value: string; onChange: (value: string) => void; name?: string; label: string; /** Shown while the field is blank (for example the vendor's icon). */ fallback?: string; disabled?: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Labeled label={label} group>
      <div className="flex items-center gap-2">
        <ModelIcon icon={value || fallback} name={name} size={32} className="rounded-md border p-0.5" />
        <Input value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} placeholder={t("adminModels.icon.placeholder")} spellCheck={false} autoComplete="off" />
        <Button type="button" variant="outline" disabled={disabled} onClick={() => setOpen(true)}>
          <ImagePlus />
          {t("adminModels.icon.choose")}
        </Button>
      </div>
      <IconPicker open={open} onOpenChange={setOpen} value={value} name={name} onPick={(next) => onChange(next)} />
    </Labeled>
  );
}

export function IconPicker({ open, onOpenChange, value, name, onPick }: { open: boolean; onOpenChange: (open: boolean) => void; value: string; name?: string; onPick: (ref: string) => void }) {
  const t = useT();
  const notify = useNotify();
  const input = useRef<HTMLInputElement>(null);
  const [assets, setAssets] = useState<IconAsset[] | null>(null);
  const [uploading, setUploading] = useState(false);
  const [removing, setRemoving] = useState<IconAsset | null>(null);

  const load = useCallback(() => {
    listIconAssets().then(setAssets, (error: unknown) => {
      setAssets([]);
      notify.error("adminModels.icon.loadFailed", error);
    });
  }, [notify]);

  useEffect(() => {
    if (open) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const upload = async (file: File | undefined) => {
    if (!file) return;
    const bad = iconFileProblem(file);
    if (bad) {
      toast.error(t(bad === "type" ? "adminModels.icon.badType" : "adminModels.icon.tooLarge"));
      return;
    }
    setUploading(true);
    try {
      const asset = await uploadIconAsset(file);
      toast.success(t(asset.reused ? "adminModels.icon.reused" : "adminModels.icon.uploaded"));
      setAssets((list) => (list && list.some((a) => a.publicID === asset.publicID) ? list : [asset, ...(list ?? [])]));
      onPick(asset.ref);
    } catch (error) {
      notify.error("adminModels.icon.uploadFailed", error);
    } finally {
      setUploading(false);
      if (input.current) input.current.value = "";
    }
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await deleteIconAsset(removing.publicID);
    } catch (error) {
      if (error instanceof ApiError && error.errorCode === "llm.model_icon_asset_in_use") {
        toast.error(t("adminModels.icon.inUse", { count: readIconUsage(error.details) }));
        throw new Error(t("adminModels.icon.inUse", { count: readIconUsage(error.details) }));
      }
      notify.error("adminModels.icon.deleteFailed", error);
      throw error;
    }
    if (value === removing.ref) onPick("");
    setAssets((list) => (list ?? []).filter((a) => a.publicID !== removing.publicID));
    toast.success(t("adminModels.icon.deleted"));
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("adminModels.icon.title")}</DialogTitle>
            <DialogDescription>{t("adminModels.icon.description")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <Labeled group label={t("adminModels.icon.reference")} hint={t("adminModels.icon.referenceHint")}>
              <div className="flex items-center gap-2">
                <ModelIcon icon={value} name={name} size={32} className="rounded-md border p-0.5" />
                <Input value={value} aria-label={t("adminModels.icon.reference")} onChange={(e) => onPick(e.target.value)} spellCheck={false} autoComplete="off" />
                {value ? (
                  <Button variant="outline" onClick={() => onPick("")}>
                    {t("common.actions.reset")}
                  </Button>
                ) : null}
              </div>
            </Labeled>
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium">{t("adminModels.icon.uploads")}</span>
                <input ref={input} type="file" accept="image/png,image/jpeg,image/webp" className="sr-only" onChange={(e) => void upload(e.target.files?.[0])} />
                <Button size="sm" variant="outline" loading={uploading} onClick={() => input.current?.click()}>
                  <Upload />
                  {t("adminModels.icon.upload")}
                </Button>
              </div>
              {assets === null ? (
                <div className="grid grid-cols-6 gap-2">
                  {Array.from({ length: 6 }, (_, i) => (
                    <Skeleton key={i} className="size-14" />
                  ))}
                </div>
              ) : assets.length === 0 ? (
                <p className="rounded-md border border-dashed p-4 text-center text-[13px] text-muted-foreground">{t("adminModels.icon.none")}</p>
              ) : (
                <ul className="grid grid-cols-4 gap-2 sm:grid-cols-6">
                  {assets.map((a, i) => (
                    <li key={a.publicID} className={cn("group relative rounded-lg border p-1.5", value === a.ref && "border-primary ring-2 ring-primary/30")}>
                      <button type="button" aria-label={`${t("adminModels.icon.use")} ${i + 1}`} aria-pressed={value === a.ref} className="flex w-full items-center justify-center" onClick={() => onPick(a.ref)}>
                        <ModelIcon icon={a.ref} size={44} />
                      </button>
                      <button
                        type="button"
                        aria-label={`${t("common.actions.delete")} ${i + 1}`}
                        className="absolute -right-1.5 -top-1.5 hidden size-6 items-center justify-center rounded-full border bg-popover text-destructive shadow-sm group-hover:flex group-focus-within:flex"
                        onClick={() => setRemoving(a)}
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              <p className="text-[13px] text-muted-foreground">{t("adminModels.icon.limits")}</p>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>{t("adminCommon.common.close")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog open={removing !== null} onOpenChange={(next) => !next && setRemoving(null)} title={t("adminModels.icon.deleteTitle")} description={t("adminModels.icon.deleteBody")} confirmLabel={t("common.actions.delete")} destructive onConfirm={remove} />
    </>
  );
}

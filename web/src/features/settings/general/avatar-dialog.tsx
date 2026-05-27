"use client";

import { Dices, ImageUp } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { patchProfile, uploadAvatar } from "../api";
import { AvatarView } from "./avatar-view";
import { fileAvatar, generatedAvatar, parseAvatar, randomVariant } from "./avatar";
import type { AuthUser } from "@/lib/api/types";

/** The account stores at most this many characters of an avatar reference. */
const AVATAR_URL_MAX = 2048;

export function AvatarDialog({
  open,
  onOpenChange,
  seed,
  saved,
  displayName,
  username,
  publicID,
  version,
  previews,
  onPreview,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The avatar value the dialog starts from (the draft's). */
  seed: string;
  /** The avatar value stored on the account. */
  saved: string;
  displayName: string;
  username: string;
  publicID: string;
  version: string;
  previews: Record<string, string>;
  /** Remembers the local preview of an uploaded file. */
  onPreview: (fileID: string, objectURL: string) => void;
  onSaved: (user: AuthUser) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [value, setValue] = useState("");
  const [uploading, setUploading] = useState(false);
  const [applying, setApplying] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const busy = uploading || applying;

  useEffect(() => {
    if (open) setValue(seed.trim());
    // Re-seed only when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const choose = async (file: File | undefined) => {
    if (fileInput.current) fileInput.current.value = "";
    if (!file || uploading) return;
    if (!file.type.startsWith("image/")) {
      toast.error(t("settings.general.avatar.uploadInvalid"));
      return;
    }
    const local = URL.createObjectURL(file);
    setUploading(true);
    try {
      const id = await uploadAvatar(file);
      onPreview(id, local);
      setValue(fileAvatar(id));
      toast.success(t("settings.general.avatar.uploaded"));
    } catch (e) {
      URL.revokeObjectURL(local);
      toast.error(t("settings.general.avatar.uploadFailed"), { description: errorMessage(e) });
    } finally {
      setUploading(false);
    }
  };

  const apply = async () => {
    const next = value.trim();
    if (next === saved.trim()) {
      onOpenChange(false);
      return;
    }
    setApplying(true);
    try {
      const user = await patchProfile({ avatarURL: next });
      onSaved(user);
      onOpenChange(false);
      toast.success(t("settings.general.toasts.profileUpdated"));
    } catch (e) {
      toast.error(t("settings.general.toasts.saveProfileFailed"), { description: errorMessage(e) });
    } finally {
      setApplying(false);
    }
  };

  const urlValue = parseAvatar(value).kind === "url" ? value : "";

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("settings.general.avatar.title")}</DialogTitle>
          <DialogDescription>{t("settings.general.avatar.description")}</DialogDescription>
        </DialogHeader>
        <div className="flex justify-center py-1">
          <div className="size-28 text-3xl">
            <AvatarView value={value} displayName={displayName} username={username} publicID={publicID} version={version} previews={previews} />
          </div>
        </div>
        <div className="flex flex-wrap justify-center gap-2">
          <input ref={fileInput} type="file" accept="image/*" hidden onChange={(e) => void choose(e.target.files?.[0])} />
          <Button variant="outline" loading={uploading} disabled={applying} onClick={() => fileInput.current?.click()}>
            {uploading ? null : <ImageUp />}
            {uploading ? t("settings.general.avatar.uploading") : t("settings.general.avatar.upload")}
          </Button>
          <Button variant="outline" disabled={busy} onClick={() => setValue(generatedAvatar(randomVariant()))}>
            <Dices />
            {t("settings.general.avatar.generate")}
          </Button>
        </div>
        <Field>
          <FieldLabel>{t("settings.general.avatar.urlLabel")}</FieldLabel>
          <Input
            value={urlValue}
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            maxLength={AVATAR_URL_MAX}
            disabled={busy}
            placeholder="https://example.com/me.png"
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={applying} disabled={uploading} onClick={() => void apply()}>
            {t("settings.general.avatar.apply")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

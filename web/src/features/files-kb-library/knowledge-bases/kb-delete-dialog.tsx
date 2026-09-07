"use client";

import { useEffect, useState } from "react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import type { KnowledgeBase } from "../shared/types";
import { Checkbox } from "../shared/ui/checkbox";

/** Confirms deleting one or several knowledge bases, with the "also delete files" choice when it applies. */
export function KbDeleteDialog({
  targets,
  deleting,
  onConfirm,
  onClose,
}: {
  /** Empty = closed. */
  targets: readonly KnowledgeBase[];
  deleting: boolean;
  /** Resolves true when the dialog may close. */
  onConfirm: (deleteFiles: boolean) => Promise<boolean>;
  onClose: () => void;
}) {
  const t = useT();
  const open = targets.length > 0;
  const [alsoFiles, setAlsoFiles] = useState(false);
  useEffect(() => {
    if (open) setAlsoFiles(false);
  }, [open]);

  const single = targets.length === 1 ? targets[0] : undefined;
  const hasFiles = targets.some((kb) => kb.fileCount > 0);

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !deleting && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{single ? t("kbWs.dialogs.deleteTitle") : t("kbWs.dialogs.bulkTitle")}</DialogTitle>
          <DialogDescription>
            {single ? t("kbWs.dialogs.deleteBody", { name: single.name }) : t("kbWs.dialogs.bulkBody", { count: targets.length })}
          </DialogDescription>
        </DialogHeader>
        {hasFiles ? (
          <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3">
            <Checkbox className="mt-0.5" checked={alsoFiles} disabled={deleting} onCheckedChange={setAlsoFiles} />
            <span className="grid gap-0.5">
              <span className="text-sm font-medium">{t("kbWs.dialogs.alsoFiles")}</span>
              <span className="text-[13px] text-muted-foreground">{t("kbWs.dialogs.alsoFilesHint")}</span>
            </span>
          </label>
        ) : null}
        <DialogFooter>
          <Button variant="outline" disabled={deleting} onClick={onClose}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            variant="destructive"
            loading={deleting}
            onClick={async () => {
              if (await onConfirm(hasFiles && alsoFiles)) onClose();
            }}
          >
            {t("kbWs.dialogs.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

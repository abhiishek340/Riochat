"use client";

import { Check, Copy, ExternalLink } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useCopy } from "../../hooks/use-copy";
import { linkConfirmStore } from "../../state/link-confirm";
import { useStore } from "../../state/store";

/** Mounted once: external links open only after this confirmation. */
export function ExternalLinkDialog() {
  const t = useT();
  const url = useStore(linkConfirmStore);
  const { copied, copy } = useCopy();
  const close = () => linkConfirmStore.set(null);
  return (
    <Dialog open={url !== null} onOpenChange={(o) => !o && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("chatRich.md.link.title")}</DialogTitle>
          <DialogDescription>{t("chatRich.md.link.description")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-1.5">
          <span className="text-xs font-medium text-muted-foreground">{t("chatRich.md.link.address")}</span>
          <div className="flex items-start gap-2 rounded-lg border bg-muted/50 p-2.5">
            <code className="min-w-0 flex-1 break-all text-sm">{url}</code>
            <button type="button" onClick={() => url && void copy(url)} aria-label={t("chatRich.md.link.copy")} title={t("chatRich.md.link.copy")} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </button>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={close}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            onClick={() => {
              if (url) window.open(url, "_blank", "noopener,noreferrer");
              close();
            }}
          >
            <ExternalLink /> {t("chatRich.md.link.open")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

"use client";

import { Shuffle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { SelectMenu } from "../components/menu-select";
import { Sheet } from "../components/sheet";
import { OptionSelect } from "../components/toolbar";
import { problemText } from "../lib/problem";
import { useLabel } from "../lib/use-label";
import { useSnapshot } from "../lib/use-snapshot";
import type { BillingMode, UserRecord } from "../lib/wire";
import { createUser, patchUser, setBalance } from "./api";
import { avatarSrc, generatedAvatar } from "./avatar";
import { createBody, defaultTier, editFormOf, planEdit, roleChoices, STATUSES, tierChoices, validateCreate, type CreateForm, type EditForm } from "./rules";
import { timezoneList } from "./timezones";

const LOCALES = ["en-US"] as const;

export function UserAvatar({ user, size = 36 }: { user: Pick<UserRecord, "avatarURL" | "publicID" | "username" | "displayName">; size?: number }) {
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={avatarSrc(user.avatarURL, user)} alt="" width={size} height={size} referrerPolicy="no-referrer" className="shrink-0 rounded-full border bg-muted object-cover" style={{ width: size, height: size }} />;
}

export function AvatarDialog({ open, value, seed, onOpenChange, onSave }: { open: boolean; value: string; seed: { publicID?: string; username?: string; displayName?: string }; onOpenChange: (open: boolean) => void; onSave: (value: string) => Promise<void> | void }) {
  const t = useT();
  const message = useErrorMessage();
  const [draft, setDraft] = useState(value);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setDraft(value);
      setError(null);
    }
  }, [open, value]);
  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(draft);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("adminUsers.avatar.title")}</DialogTitle>
          <DialogDescription>{t("adminUsers.avatar.body")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col items-center gap-3">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={avatarSrc(draft, seed)} alt={t("adminUsers.avatar.preview")} width={96} height={96} className="size-24 rounded-full border bg-muted" />
          <Button variant="outline" size="sm" onClick={() => setDraft(generatedAvatar())}>
            <Shuffle />
            {t("adminUsers.avatar.randomize")}
          </Button>
        </div>
        <FieldError>{error}</FieldError>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={save}>
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export interface UserFormProps {
  /** An account to edit, "new" to create one, null when closed. */
  target: UserRecord | "new" | null;
  mode: BillingMode;
  viewerRole: string | undefined;
  planCodes: string[];
  onOpenChange: (open: boolean) => void;
  /** Called with the final row after a successful edit, or the new account. */
  onSaved: (user: UserRecord, created: boolean) => void;
  /** The account fields were saved but the balance write failed: refresh the row, the sheet stays open. */
  onPartial?: (user: UserRecord) => void;
}

const BLANK_CREATE = (tier: string): CreateForm => ({ avatarURL: generatedAvatar(), username: "", displayName: "", password: "", email: "", timezone: "Etc/UTC", locale: "en-US", tier, expiryDate: "" });

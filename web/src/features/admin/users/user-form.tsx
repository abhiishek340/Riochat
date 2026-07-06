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

export function UserFormSheet({ target, mode, viewerRole, planCodes, onOpenChange, onSaved, onPartial }: UserFormProps) {
  const t = useT();
  const message = useErrorMessage();
  const label = useLabel();
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const zones = useMemo(() => timezoneList(), []);
  const tiers = useMemo(() => tierChoices(planCodes), [planCodes]);
  const [create, setCreate] = useState<CreateForm>(BLANK_CREATE("free"));
  const [edit, setEdit] = useState<EditForm | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);

  useEffect(() => {
    if (target === null) return;
    setError(null);
    if (target === "new") setCreate(BLANK_CREATE(defaultTier(planCodes)));
    else setEdit(editFormOf(target));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  const zoneOptions = useMemo(() => {
    const current = existing ? existing.timezone : "";
    return (current && !zones.includes(current) ? [current, ...zones] : zones).map((z) => ({ value: z, label: z }));
  }, [zones, existing]);

  const submitCreate = async () => {
    const found = validateCreate(create, mode);
    if (found) {
      setError(problemText(t, found));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const user = await createUser(createBody(create, mode));
      onSaved(user, true);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const submitEdit = async () => {
    if (!existing || !edit) return;
    const result = planEdit(existing, edit, { mode });
    if (!result.ok) {
      setError(problemText(t, result.problem));
      return;
    }
    if (result.empty) {
      onOpenChange(false);
      return;
    }
    setBusy(true);
    setError(null);
    let row = existing;
    let patched = false;
    try {
      if (Object.keys(result.plan.patch).length > 0) {
        row = await patchUser(existing.id, result.plan.patch);
        patched = true;
      }
      if (result.plan.balance !== undefined) {
        const account = await setBalance(existing.id, result.plan.balance, t("adminUsers.balanceNote"));
        row = { ...row, billingBalanceUSD: account.balanceUSD, billingBalanceNanousd: account.balanceNanousd, billingAccountStatus: account.status };
      }
      onSaved(row, false);
      onOpenChange(false);
    } catch (e) {
      if (patched) onPartial?.(row);
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const timezone = (value: string, onChange: (v: string) => void) => <SelectMenu value={value || "Etc/UTC"} onChange={onChange} options={zoneOptions} searchable className="w-full" />;
  const localeSelect = (value: string, onChange: (v: string) => void) => (
    <OptionSelect value={value || "en-US"} onChange={onChange} options={LOCALES.map((l) => ({ value: l, label: "English (US)" }))} className="w-full" />
  );
  const periodFields = (tier: string, expiry: string, onTier: (v: string) => void, onExpiry: (v: string) => void) =>
    mode === "period" ? (
      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel>{t("adminUsers.form.tier")}</FieldLabel>
          <OptionSelect value={tier} onChange={onTier} options={tiers.map((x) => ({ value: x, label: x }))} className="w-full" />
        </Field>
        {tier && tier !== "free" ? (
          <Field>
            <FieldLabel>{t("adminUsers.form.expiry")}</FieldLabel>
            <Input type="date" value={expiry} onChange={(e) => onExpiry(e.target.value)} />
            <FieldHint>{t("adminUsers.form.expiryHint")}</FieldHint>
          </Field>
        ) : null}
      </div>
    ) : null;

  const isCreate = target === "new" || (target === null && !existing);
  return (
    <>
      <Sheet
        open={target !== null}
        onOpenChange={onOpenChange}
        busy={busy}
        size="md"
        title={isCreate ? t("adminUsers.form.createTitle") : t("adminUsers.form.editTitle", { name: existing?.displayName || existing?.username || "" })}
        footer={
          <>
            <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={busy} onClick={isCreate ? submitCreate : submitEdit}>
              {isCreate ? t("common.actions.create") : t("common.actions.save")}
            </Button>
          </>
        }
      >
        {isCreate ? (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              void submitCreate();
            }}
          >
            <div className="flex items-center gap-3">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={avatarSrc(create.avatarURL, { username: create.username })} alt="" width={56} height={56} className="size-14 rounded-full border bg-muted" />
              <Button type="button" variant="outline" size="sm" onClick={() => setAvatarOpen(true)}>
                {t("adminUsers.form.changeAvatar")}
              </Button>
            </div>
            <Field required>
              <FieldLabel>{t("adminUsers.form.username")}</FieldLabel>
              <Input value={create.username} onChange={(e) => setCreate({ ...create, username: e.target.value })} autoComplete="off" spellCheck={false} />
              <FieldHint>{t("adminUsers.form.usernameHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("adminUsers.form.displayName")}</FieldLabel>
              <Input value={create.displayName} onChange={(e) => setCreate({ ...create, displayName: e.target.value })} autoComplete="off" />
            </Field>
            <Field required>
              <FieldLabel>{t("adminUsers.form.password")}</FieldLabel>
              <Input type="password" value={create.password} onChange={(e) => setCreate({ ...create, password: e.target.value })} autoComplete="new-password" />
              <FieldHint>{t("adminUsers.form.passwordHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("adminUsers.form.email")}</FieldLabel>
              <Input type="email" value={create.email} onChange={(e) => setCreate({ ...create, email: e.target.value })} autoComplete="off" />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel>{t("adminUsers.form.timezone")}</FieldLabel>
                {timezone(create.timezone, (v) => setCreate({ ...create, timezone: v }))}
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.form.locale")}</FieldLabel>
                {localeSelect(create.locale, (v) => setCreate({ ...create, locale: v }))}
              </Field>
            </div>
            {periodFields(create.tier, create.expiryDate, (v) => setCreate({ ...create, tier: v }), (v) => setCreate({ ...create, expiryDate: v }))}
            <FieldError>{error}</FieldError>
          </form>
        ) : edit && existing ? (
          <div className="grid gap-4">
            <div className="flex items-center gap-3">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={avatarSrc(edit.avatarURL, existing)} alt="" width={56} height={56} className="size-14 rounded-full border bg-muted" />
              <Button type="button" variant="outline" size="sm" onClick={() => setAvatarOpen(true)}>
                {t("adminUsers.form.changeAvatar")}
              </Button>
              <span className="text-sm text-muted-foreground">@{existing.username}</span>
            </div>
            <Field required>
              <FieldLabel>{t("adminUsers.form.displayName")}</FieldLabel>
              <Input value={edit.displayName} onChange={(e) => setEdit({ ...edit, displayName: e.target.value })} autoComplete="off" />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel>{t("adminUsers.form.email")}</FieldLabel>
                <Input value={edit.email} onChange={(e) => setEdit({ ...edit, email: e.target.value })} autoComplete="off" />
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.form.phone")}</FieldLabel>
                <Input value={edit.phone} onChange={(e) => setEdit({ ...edit, phone: e.target.value })} autoComplete="off" />
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.columns.role")}</FieldLabel>
                <OptionSelect value={edit.role} onChange={(v) => setEdit({ ...edit, role: v })} options={roleChoices(viewerRole, existing.role).map((r) => ({ value: r, label: label("adminUsers.roles", r) }))} className="w-full" />
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.columns.status")}</FieldLabel>
                <OptionSelect
                  value={edit.status}
                  onChange={(v) => setEdit({ ...edit, status: v })}
                  options={[...((STATUSES as readonly string[]).includes(existing.status) ? [] : [existing.status]), ...STATUSES].map((s) => ({ value: s, label: label("adminUsers.statuses", s) }))}
                  className="w-full"
                />
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.form.timezone")}</FieldLabel>
                {timezone(edit.timezone, (v) => setEdit({ ...edit, timezone: v }))}
              </Field>
              <Field>
                <FieldLabel>{t("adminUsers.form.locale")}</FieldLabel>
                {localeSelect(edit.locale, (v) => setEdit({ ...edit, locale: v }))}
              </Field>
            </div>
            {edit.status !== existing.status ? (
              <Field>
                <FieldLabel>{t("adminUsers.form.reason")}</FieldLabel>
                <Input value={edit.reason} maxLength={255} onChange={(e) => setEdit({ ...edit, reason: e.target.value })} />
                <FieldHint>{t("adminUsers.form.reasonHint")}</FieldHint>
              </Field>
            ) : null}
            {periodFields(edit.tier, edit.expiryDate, (v) => setEdit({ ...edit, tier: v }), (v) => setEdit({ ...edit, expiryDate: v }))}
            {mode !== "self" ? (
              <Field>
                <FieldLabel>{t("adminUsers.form.balance")}</FieldLabel>
                <Input inputMode="decimal" value={edit.balance} onChange={(e) => setEdit({ ...edit, balance: e.target.value })} />
                <FieldHint>{t("adminUsers.form.balanceHint")}</FieldHint>
              </Field>
            ) : null}
            <Field>
              <FieldLabel>{t("adminUsers.form.profile")}</FieldLabel>
              <Textarea rows={3} value={edit.profilePreferences} maxLength={1024} onChange={(e) => setEdit({ ...edit, profilePreferences: e.target.value })} />
            </Field>
            <FieldError>{error}</FieldError>
          </div>
        ) : null}
      </Sheet>
      <AvatarDialog
        open={avatarOpen}
        value={isCreate ? create.avatarURL : (edit?.avatarURL ?? "")}
        seed={isCreate ? { username: create.username } : (existing ?? {})}
        onOpenChange={setAvatarOpen}
        onSave={(value) => (isCreate ? setCreate({ ...create, avatarURL: value }) : edit ? setEdit({ ...edit, avatarURL: value }) : undefined)}
      />
    </>
  );
}

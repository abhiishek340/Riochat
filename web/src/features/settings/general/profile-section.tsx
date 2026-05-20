"use client";

import { Camera } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { ApiError } from "@/lib/api/errors";
import type { AuthUser } from "@/lib/api/types";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { publishUserUpdate, useSession } from "@/lib/session/provider";
import { SettingsSection } from "../parts";
import { patchProfile, patchUsername } from "../api";
import { PROFILE_PREFERENCES_MAX, USERNAME_MAX, normalizeUsername } from "../lib/policy";
import { timeZoneOptions } from "../lib/timezones";
import { AvatarDialog } from "./avatar-dialog";
import { AvatarView } from "./avatar-view";
import { parseAvatar } from "./avatar";
import {
  canEditUsername,
  classifyUsernameFailure,
  fieldsFromUser,
  pendingUsername,
  profileFieldsChanged,
  profilePatchBody,
  saveBlock,
  type ProfileFields,
} from "./profile-draft";

const EMPTY: ProfileFields = { avatarURL: "", displayName: "", timezone: "", locale: "", profilePreferences: "" };

export function ProfileSection() {
  const t = useT();
  const errorMessage = useErrorMessage();
  const { user, status } = useSession();

  const saved = useMemo(() => (user ? fieldsFromUser(user) : EMPTY), [user]);
  const [draft, setDraft] = useState<ProfileFields>(saved);
  const [usernameDraft, setUsernameDraft] = useState(user?.username ?? "");
  const [saving, setSaving] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const [previews, setPreviews] = useState<Record<string, string>>({});
  const previewsRef = useRef(previews);
  previewsRef.current = previews;
  const prevSaved = useRef<ProfileFields>(saved);
  const prevUsername = useRef(user?.username ?? "");
  const initialToast = useRef(false);

  // Adopt a changed account snapshot unless there are unsaved edits to protect.
  useEffect(() => {
    if (!user) return;
    // Read the refs now: the updaters run later, after the refs below move on.
    const previousSaved = prevSaved.current;
    const previousUsername = prevUsername.current;
    setDraft((current) => (profileFieldsChanged(current, previousSaved) ? current : saved));
    setUsernameDraft((current) => (current === previousUsername ? user.username : current));
    prevSaved.current = saved;
    prevUsername.current = user.username;
  }, [user, saved]);

  useEffect(() => {
    if (user?.initialUsernameRequired && !initialToast.current) {
      initialToast.current = true;
      toast.info(t("settings.general.profile.initialUsernameRequired"));
    }
  }, [user, t]);

  useEffect(
    () => () => {
      for (const url of Object.values(previewsRef.current)) URL.revokeObjectURL(url);
    },
    [],
  );

  const releasePreviewsExcept = useCallback((keepAvatar: string) => {
    const keep = parseAvatar(keepAvatar);
    setPreviews((current) => {
      const next: Record<string, string> = {};
      for (const [id, url] of Object.entries(current)) {
        if (keep.kind === "file" && keep.fileID === id) next[id] = url;
        else URL.revokeObjectURL(url);
      }
      return next;
    });
  }, []);

  const loading = status === "loading";
  const disabled = !user || saving;
  const usernameEditable = canEditUsername(user);
  const pending = pendingUsername(user, usernameDraft);
  const fieldsChanged = profileFieldsChanged(draft, saved);
  const hasEdits = fieldsChanged || pending !== null;
  const zones = useMemo(() => timeZoneOptions(draft.timezone), [draft.timezone]);

  const usernameError = (e: unknown): string => {
    const api = e instanceof ApiError ? e : null;
    const kind = classifyUsernameFailure({ status: api?.status, errorCode: api?.errorCode, message: api?.message ?? (e instanceof Error ? e.message : "") });
    if (kind === "invalid") return t("settings.general.profile.usernameInvalid");
    if (kind === "alreadyChanged") return t("settings.general.profile.usernameAlreadyChanged");
    if (kind === "taken") return t("settings.general.profile.usernameTaken");
    return errorMessage(e);
  };

  const reset = () => {
    setDraft(saved);
    setUsernameDraft(user?.username ?? "");
    releasePreviewsExcept(saved.avatarURL);
  };

  const save = async () => {
    if (saving || !hasEdits || !user) return;
    const block = saveBlock({ pendingUsername: pending, fieldsChanged, displayName: draft.displayName });
    if (block === "usernameInvalid") {
      toast.error(t("settings.general.toasts.setUsernameFailed"), { description: t("settings.general.profile.usernameInvalid") });
      return;
    }
    if (block === "displayNameInvalid") {
      toast.error(t("settings.general.toasts.saveProfileFailed"), { description: t("settings.general.profile.displayNameInvalid") });
      return;
    }
    setSaving(true);
    try {
      let latest: AuthUser | null = null;
      if (pending !== null) {
        try {
          latest = await patchUsername(pending);
          // The name is changed for good now; tell the rest of the app even if
          // the profile request below fails.
          if (fieldsChanged) publishUserUpdate(latest);
        } catch (e) {
          toast.error(t("settings.general.toasts.setUsernameFailed"), { description: usernameError(e) });
          return;
        }
      }
      if (fieldsChanged) latest = await patchProfile(profilePatchBody(draft));
      if (latest) {
        const next = fieldsFromUser(latest);
        prevSaved.current = next;
        prevUsername.current = latest.username;
        setDraft(next);
        setUsernameDraft(latest.username);
        releasePreviewsExcept(next.avatarURL);
        publishUserUpdate(latest);
      }
      toast.success(fieldsChanged ? t("settings.general.toasts.profileUpdated") : t("settings.general.toasts.usernameUpdated"));
    } catch (e) {
      toast.error(t("settings.general.toasts.saveProfileFailed"), { description: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };

  const actions = hasEdits ? (
    <>
      <Button variant="outline" size="sm" disabled={saving} onClick={reset}>
        {t("common.actions.reset")}
      </Button>
      <Button size="sm" loading={saving} onClick={() => void save()}>
        {saving ? t("common.actions.saving") : t("common.actions.save")}
      </Button>
    </>
  ) : null;

  return (
    <SettingsSection title={t("settings.general.profile.title")} description={t("settings.general.profile.description")} actions={actions} bare>
      {loading ? (
        <div className="grid gap-4 rounded-xl border bg-card p-5">
          <Skeleton className="size-20 rounded-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      ) : (
        <form
          className="grid gap-5 rounded-xl border bg-card p-5 shadow-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <div className="flex items-center gap-4">
            <button
              type="button"
              disabled={disabled}
              onClick={() => setAvatarOpen(true)}
              aria-label={t("settings.general.avatar.change")}
              className="group relative size-20 shrink-0 rounded-full text-2xl outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:opacity-60"
            >
              <AvatarView
                value={draft.avatarURL}
                displayName={draft.displayName}
                username={user?.username ?? ""}
                publicID={user?.publicID}
                version={user?.updatedAt}
                previews={previews}
              />
              <span className="absolute inset-0 flex items-center justify-center rounded-full bg-black/45 text-white opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
                <Camera className="size-5" />
              </span>
            </button>
            <div className="grid min-w-0 gap-0.5">
              <span className="truncate text-base font-semibold">{draft.displayName || user?.username || "-"}</span>
              <span className="truncate text-sm text-muted-foreground">{user?.email || user?.username || ""}</span>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("settings.general.profile.username")}</FieldLabel>
              <Input
                value={usernameDraft}
                maxLength={USERNAME_MAX}
                autoComplete="off"
                spellCheck={false}
                disabled={disabled || !usernameEditable}
                onChange={(e) => setUsernameDraft(e.target.value.toLowerCase())}
                onBlur={() => setUsernameDraft((v) => (usernameEditable ? normalizeUsername(v) : v))}
              />
              <FieldHint>{usernameEditable ? t("settings.general.profile.usernameHintOnce") : t("settings.general.profile.usernameHintLocked")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("settings.general.profile.displayName")}</FieldLabel>
              <Input
                value={draft.displayName}
                disabled={disabled}
                autoComplete="nickname"
                onChange={(e) => setDraft({ ...draft, displayName: Array.from(e.target.value).slice(0, 16).join("") })}
              />
              <FieldHint>{t("settings.general.profile.displayNameHint")}</FieldHint>
            </Field>
          </div>

          <Field>
            <FieldLabel>{t("settings.general.profile.timezone")}</FieldLabel>
            <NativeSelect value={draft.timezone} disabled={disabled} onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}>
              {zones.map((zone) => (
                <option key={zone} value={zone}>
                  {zone}
                </option>
              ))}
            </NativeSelect>
          </Field>

          <Field>
            <FieldLabel>{t("settings.general.profile.preferences")}</FieldLabel>
            <Textarea
              value={draft.profilePreferences}
              disabled={disabled}
              maxLength={PROFILE_PREFERENCES_MAX}
              className="h-32 resize-none overflow-y-auto"
              placeholder={t("settings.general.profile.preferencesPlaceholder")}
              onChange={(e) => setDraft({ ...draft, profilePreferences: e.target.value })}
            />
            <FieldHint>
              {t("settings.general.profile.preferencesHint", { count: draft.profilePreferences.length, max: PROFILE_PREFERENCES_MAX })}
            </FieldHint>
          </Field>
          <button type="submit" hidden aria-hidden tabIndex={-1} />
        </form>
      )}
      {user ? (
        <AvatarDialog
          open={avatarOpen}
          onOpenChange={setAvatarOpen}
          seed={draft.avatarURL}
          saved={saved.avatarURL}
          displayName={draft.displayName}
          username={user.username}
          publicID={user.publicID}
          version={user.updatedAt}
          previews={previews}
          onPreview={(id, url) =>
            setPreviews((current) => {
              if (current[id]) URL.revokeObjectURL(current[id]);
              return { ...current, [id]: url };
            })
          }
          onSaved={(next) => {
            // Only the avatar was sent: other unsaved edits stay in the draft.
            const savedNext = fieldsFromUser(next);
            prevSaved.current = { ...prevSaved.current, avatarURL: savedNext.avatarURL };
            setDraft((d) => ({ ...d, avatarURL: savedNext.avatarURL }));
            publishUserUpdate(next);
          }}
        />
      ) : null}
    </SettingsSection>
  );
}

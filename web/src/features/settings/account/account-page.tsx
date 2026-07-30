"use client";

import { Fingerprint } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import type { LoginOptions } from "@/lib/api/types";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { endSession } from "@/lib/api/session-store";
import { publishUserUpdate, useSession } from "@/lib/session/provider";
import { CopyButton, LoadErrorNotice, SettingRow, SettingsPage, SettingsSection } from "../parts";
import { availableVerificationMethods, isEmailBootstrap } from "../lib/policy";
import { emailBootstrapUsed, emailSource, hasPassword, isEmailVerified } from "../lib/user";
import { useLoad } from "../lib/use-load";
import {
  fetchCurrentUser,
  fetchIdentities,
  fetchLoginOptions,
  fetchMfaStatus,
  fetchSessions,
  logoutAll,
  mfaSetupStart,
  type IdentityRow,
  type MfaStatus,
  type SessionRow,
  type TotpSetup,
} from "./api";
import { DeleteAccountDialog } from "./delete-account-dialog";
import { EmailDialog, type EmailMode } from "./email-dialog";
import { IdentitiesSection } from "./identities-section";
import { PasswordDialog } from "./password-dialog";
import { SessionsSection } from "./sessions-section";
import { TwoFactorDialog, type TwoFactorMode } from "./two-factor-dialog";

interface AccountData {
  options: LoginOptions | null;
  sessions: SessionRow[];
  identities: IdentityRow[];
  mfa: MfaStatus | null;
  error: unknown;
}

export function AccountSettingsPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  usePageTitle(t("settings.title"));
  const { user, accessToken } = useSession();
  const { flags } = useCapabilities();
  const security = flags.accountSecurity;
  const identityFeature = flags.identityProviders;

  const data = useLoad<AccountData>(
    async (signal) => {
      const [me, options, sessions, identities, mfa] = await Promise.allSettled([
        fetchCurrentUser(signal),
        fetchLoginOptions(),
        security ? fetchSessions(signal) : Promise.resolve([] as SessionRow[]),
        identityFeature ? fetchIdentities(signal) : Promise.resolve([] as IdentityRow[]),
        security ? fetchMfaStatus(signal) : Promise.resolve(null),
      ]);
      if (me.status === "fulfilled") publishUserUpdate(me.value);
      const failed = [me, options, sessions, identities, mfa].find((r) => r.status === "rejected");
      return {
        options: options.status === "fulfilled" ? options.value : null,
        sessions: sessions.status === "fulfilled" ? sessions.value : [],
        identities: identities.status === "fulfilled" ? identities.value : [],
        mfa: mfa.status === "fulfilled" ? mfa.value : null,
        error: failed && failed.status === "rejected" ? failed.reason : undefined,
      };
    },
    `${accessToken}|${security}|${identityFeature}`,
    Boolean(accessToken),
  );

  const loaded = data.data;
  useEffect(() => {
    if (loaded?.error) toast.error(t("settings.account.toasts.loadFailed"), { description: errorMessage(loaded.error) });
    // One toast per load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded]);

  const [sessionsOverride, setSessionsOverride] = useState<SessionRow[] | null>(null);
  const [mfaOverride, setMfaOverride] = useState<MfaStatus | null>(null);
  useEffect(() => {
    if (loaded) {
      setSessionsOverride(null);
      setMfaOverride(null);
    }
  }, [loaded]);

  const loading = data.loading && !loaded;
  const options = loaded?.options ?? null;
  const emailVerificationEnabled = options?.emailVerificationEnabled === true;
  const providers = options?.providerAuthBridge.enabled ? options.providers.filter((p) => p.loginEnabled) : [];
  const mfa = mfaOverride ?? loaded?.mfa ?? null;
  const sessions = sessionsOverride ?? loaded?.sessions ?? [];
  const identities = loaded?.identities ?? [];

  const twoFactorEnabled = mfa?.totpEnabled ?? user?.twoFactorEnabled === true;
  const emailVerified = user ? isEmailVerified(user) : false;
  const methods = useMemo(
    () => availableVerificationMethods({ twoFactorEnabled, emailVerificationEnabled, emailVerified }),
    [twoFactorEnabled, emailVerificationEnabled, emailVerified],
  );

  const bootstrap = user
    ? isEmailBootstrap({ email: user.email, emailSource: emailSource(user), emailVerified, bootstrapUsed: emailBootstrapUsed(user) })
    : false;

  const [passwordOpen, setPasswordOpen] = useState(false);
  const [emailMode, setEmailMode] = useState<EmailMode | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [tfMode, setTfMode] = useState<TwoFactorMode | null>(null);
  const [tfSetup, setTfSetup] = useState<TotpSetup | null>(null);
  const [startingTf, setStartingTf] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);

  // A forced password reset opens the dialog on its own.
  useEffect(() => {
    if (user?.mustResetPassword) setPasswordOpen(true);
  }, [user?.mustResetPassword]);

  const refreshMfa = useCallback(async () => {
    try {
      setMfaOverride(await fetchMfaStatus());
    } catch {
      /* the next full load corrects it */
    }
  }, []);

  const startTwoFactor = async (): Promise<TotpSetup | null> => {
    try {
      const setup = await mfaSetupStart();
      setTfSetup(setup);
      return setup;
    } catch (e) {
      toast.error(t("settings.account.toasts.startTwoFactorFailed"), { description: errorMessage(e) });
      return null;
    }
  };

  const openTwoFactor = async () => {
    if (twoFactorEnabled) {
      setTfMode("manage");
      return;
    }
    setStartingTf(true);
    const setup = await startTwoFactor();
    setStartingTf(false);
    if (setup) setTfMode("setup");
  };

  const logOutEverywhere = async () => {
    setLoggingOut(true);
    try {
      await logoutAll();
    } catch {
      /* the user always leaves, even when the call fails */
    }
    await endSession();
  };

  const updated = (next: Parameters<typeof publishUserUpdate>[0]) => publishUserUpdate(next);

  if (!user) {
    return (
      <SettingsPage title={t("settings.nav.account")} description={t("settings.account.description")}>
        <p className="text-sm text-muted-foreground">{t("common.states.loading")}</p>
      </SettingsPage>
    );
  }

  const emailTitle = user.email ? t("settings.account.rows.emailWith", { email: user.email }) : t("settings.account.rows.email");
  const emailStatus = emailVerificationEnabled && user.email ? (emailVerified ? t("settings.account.rows.verified") : t("settings.account.rows.unverified")) : "";

  return (
    <SettingsPage title={t("settings.nav.account")} description={t("settings.account.description")}>
      {loaded?.error ? <LoadErrorNotice title={t("settings.account.toasts.loadFailed")} error={loaded.error} onRetry={data.reload} retrying={data.loading} /> : null}
      <SettingsSection title={t("settings.account.overview")}>
        <SettingRow
          title={t("settings.account.rows.publicId")}
          description={<span className="font-mono">{user.publicID || "-"}</span>}
        >
          <CopyButton value={user.publicID} label={t("settings.account.rows.copyPublicId")} successMessage={t("settings.account.toasts.publicIdCopied")} variant="outline" size="icon" />
        </SettingRow>

        {security ? (
          <>
            <SettingRow
              title={emailTitle}
              description={
                emailStatus ? <Badge tone={emailVerified ? "success" : "warning"}>{emailStatus}</Badge> : t("settings.account.rows.emailBody")
              }
            >
              {emailVerificationEnabled && user.email && !emailVerified ? (
                <Button variant="outline" size="sm" disabled={loading || emailMode !== null} onClick={() => setEmailMode("verify")}>
                  {t("settings.account.rows.verify")}
                </Button>
              ) : null}
              <Button variant="outline" size="sm" disabled={loading || emailMode !== null} onClick={() => setEmailMode(bootstrap ? "set" : "change")}>
                {bootstrap ? t("settings.account.rows.set") : t("settings.account.rows.update")}
              </Button>
            </SettingRow>

            <SettingRow title={t("settings.account.rows.password")} description={t("settings.account.rows.passwordBody")}>
              <Button variant="outline" size="sm" disabled={loading} onClick={() => setPasswordOpen(true)}>
                {hasPassword(user) ? t("settings.account.rows.update") : t("settings.account.rows.set")}
              </Button>
            </SettingRow>

            {(mfa?.available ?? user.twoFactorAvailable) ? (
              <SettingRow
                title={t("settings.account.rows.twoFactor")}
                description={twoFactorEnabled ? t("settings.account.rows.twoFactorOn", { count: mfa?.recoveryCount ?? 0 }) : t("settings.account.rows.twoFactorOff")}
              >
                <Button variant="outline" size="sm" loading={startingTf} disabled={loading} onClick={() => void openTwoFactor()}>
                  {startingTf ? t("settings.account.rows.generating") : twoFactorEnabled ? t("settings.account.rows.manage") : t("settings.account.rows.set")}
                </Button>
              </SettingRow>
            ) : null}

            <SettingRow title={t("settings.account.rows.logoutAll")} description={t("settings.account.rows.logoutAllBody")}>
              <Button variant="outline" size="sm" loading={loggingOut} onClick={() => void logOutEverywhere()}>
                {loggingOut ? t("settings.account.rows.loggingOut") : t("settings.account.rows.logoutAllAction")}
              </Button>
            </SettingRow>

            <SettingRow title={t("settings.account.rows.delete")} description={t("settings.account.rows.deleteBody")}>
              <Button variant="destructive" size="sm" onClick={() => setDeleteOpen(true)}>
                {t("settings.account.rows.deleteAction")}
              </Button>
            </SettingRow>
          </>
        ) : (
          <SettingRow title={<span className="flex items-center gap-2"><Fingerprint className="size-4" />{t("settings.account.rows.securityOff")}</span>} />
        )}
      </SettingsSection>

      {identityFeature && providers.length > 0 ? (
        <IdentitiesSection identities={identities} providers={providers} loading={loading} hasPassword={hasPassword(user)} onChanged={data.reload} />
      ) : null}

      {security ? (
        <SessionsSection sessions={sessions} loading={loading} onRemoved={(id) => setSessionsOverride(sessions.filter((s) => s.sessionID !== id))} />
      ) : null}

      <PasswordDialog open={passwordOpen} onOpenChange={setPasswordOpen} user={user} methods={methods} />
      <EmailDialog
        mode={emailMode}
        onOpenChange={(open) => !open && setEmailMode(null)}
        user={user}
        currentMethods={methods}
        verificationEnabled={emailVerificationEnabled}
        onDone={updated}
      />
      <TwoFactorDialog
        mode={tfMode}
        setup={tfSetup}
        onOpenChange={(open) => {
          if (!open) setTfMode(null);
        }}
        onStartAgain={startTwoFactor}
        onChanged={() => void refreshMfa().then(() => data.reload())}
      />
      <DeleteAccountDialog open={deleteOpen} onOpenChange={setDeleteOpen} methods={methods} />
    </SettingsPage>
  );
}

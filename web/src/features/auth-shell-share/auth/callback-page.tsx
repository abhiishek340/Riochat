"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { AlertTriangle, ArrowLeft, MailWarning } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { resolveAccessToken } from "@/lib/api/client";
import { isDesktop } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { loginUrlFor } from "@/lib/utils/navigation";
import { exchangeBindGrant, exchangeProviderGrant } from "./api";
import { processCallback, type CallbackResult } from "./callback-logic";
import { settleAuth } from "./sign-in";

type View = { state: "loading" } | { state: "error"; message: string } | { state: "conflict"; providerSlug: string; email: string };

/** `/auth/callback`: finishes a provider sign-in (or an account link) exactly once per page load. */
export function AuthCallbackPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  const router = useRouter();
  const params = useSearchParams();
  const started = useRef(false);
  const [view, setView] = useState<View>({ state: "loading" });

  useEffect(() => {
    // React strict mode runs effects twice in development; a grant must not be used twice.
    if (started.current) return;
    started.current = true;
    const query = new URLSearchParams(params.toString());

    const show = (result: CallbackResult) => {
      switch (result.kind) {
        case "provider-error":
          return setView({ state: "error", message: t("authFlow.callback.providerError", { error: result.detail }) });
        case "expired":
          return setView({ state: "error", message: t("authFlow.callback.expired") });
        case "missing":
          return setView({ state: "error", message: t("authFlow.callback.missing") });
        case "failed":
          return setView({ state: "error", message: errorMessage(result.error, t("authFlow.login.failed")) });
        case "empty":
        case "challenge-lost":
          return setView({ state: "error", message: t("authFlow.login.failed") });
        case "bind-session":
          return setView({ state: "error", message: t("authFlow.callback.bindExpired") });
        case "bind-failed":
          return setView({ state: "error", message: errorMessage(result.error, t("authFlow.callback.bindFailed")) });
        case "conflict":
          return setView({ state: "conflict", providerSlug: result.providerSlug, email: result.email });
        case "challenge-stored":
          return router.replace(loginUrlFor(result.next));
        case "signed-in":
        case "bound":
          return router.replace(result.next);
      }
    };

    void processCallback(query, {
      desktop: isDesktop(),
      exchangeLogin: exchangeProviderGrant,
      exchangeBind: exchangeBindGrant,
      hasAccessToken: async () => (await resolveAccessToken()) !== "",
      settle: settleAuth,
    }).then(show, (error: unknown) => setView({ state: "error", message: errorMessage(error, t("authFlow.login.failed")) }));
  }, [params, router, t, errorMessage]);

  if (view.state === "loading") {
    return (
      <div role="status" className="grid justify-items-center gap-3 py-6 text-center">
        <Spinner className="size-6" />
        <h1 className="text-lg font-semibold tracking-tight">{t("authFlow.callback.loadingTitle")}</h1>
        <p className="text-sm text-muted-foreground">{t("authFlow.callback.loadingBody")}</p>
      </div>
    );
  }

  if (view.state === "conflict") {
    return (
      <div className="grid gap-5">
        <div className="grid justify-items-start gap-3">
          <span className="flex size-10 items-center justify-center rounded-full bg-warning/20 text-warning-foreground dark:text-warning">
            <MailWarning className="size-5" />
          </span>
          <h1 className="text-xl font-semibold tracking-tight">{t("authFlow.conflict.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("authFlow.conflict.body")}</p>
        </div>
        <dl className="grid gap-2 rounded-lg border bg-muted/40 p-3 text-sm">
          <div className="flex justify-between gap-4">
            <dt className="text-muted-foreground">{t("authFlow.conflict.provider")}</dt>
            <dd className="font-medium">{view.providerSlug || t("authFlow.conflict.unknownProvider")}</dd>
          </div>
          {view.email ? (
            <div className="flex justify-between gap-4">
              <dt className="text-muted-foreground">{t("authFlow.conflict.email")}</dt>
              <dd className="break-all font-medium">{view.email}</dd>
            </div>
          ) : null}
        </dl>
        <Button onClick={() => router.replace(loginUrlFor("/settings/account"))}>{t("authFlow.conflict.signInFirst")}</Button>
        <p className="text-center text-[13px] text-muted-foreground">
          {t("authFlow.conflict.note")}{" "}
          <button type="button" className="rounded-sm font-medium text-primary hover:underline" onClick={() => router.replace("/login")}>
            {t("authFlow.conflict.returnLink")}
          </button>
        </p>
      </div>
    );
  }

  return (
    <div className="grid gap-5">
      <div className="grid justify-items-start gap-3">
        <span className="flex size-10 items-center justify-center rounded-full bg-destructive/12 text-destructive">
          <AlertTriangle className="size-5" />
        </span>
        <h1 className="text-xl font-semibold tracking-tight">{t("authFlow.callback.errorTitle")}</h1>
        <p role="alert" className="text-sm text-muted-foreground">
          {view.message}
        </p>
      </div>
      <Button variant="outline" onClick={() => router.replace("/login")}>
        <ArrowLeft />
        {t("authFlow.callback.backToLogin")}
      </Button>
    </div>
  );
}

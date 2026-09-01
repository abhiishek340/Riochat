"use client";

import { AlertTriangle, Check, Loader2, ShieldAlert } from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AppLogo } from "@/components/brand/logo";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { ApiError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { AuthGuard } from "@/lib/session/auth-guard";
import { useLoad } from "../lib/use-load";
import { LoadErrorNotice } from "../parts";
import { createProvider, listProviders, probeProvider, type AvailableModel, type PersonalProvider } from "../models/api";
import { runBatch, type BatchDeps, type EntryStatus } from "../models/batch-import";
import { iconFor } from "../models/icons";
import { maskKey, parseImportFragment, type LinkEntry } from "../models/link-parser";
import { clearStash, readStash, saveStash } from "../models/link-stash";
import { AddProviderDialog } from "../models/provider-dialogs";
import { toRequestModels } from "../models/selection";
import { useProviderAccess } from "../models/use-provider-access";
import { recognizeProvider, resolveProtocol } from "../models/well-known";
import { linkErrorKey } from "./import-link-text";

/** Takes the link out of the address bar at once and keeps it in the tab's session storage. */
function captureFragment(): string {
  const hash = window.location.hash;
  if (hash.length > 1) {
    const fragment = hash.slice(1);
    saveStash(fragment);
    try {
      window.history.replaceState(window.history.state, "", `${window.location.pathname}${window.location.search}`);
    } catch {
      /* the stash still holds the link */
    }
    return fragment;
  }
  return readStash();
}

export function ProviderImportPage() {
  const [fragment, setFragment] = useState<string | null>(null);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    setFragment(captureFragment());
    // A fragment-only navigation loads a new link into this page.
    const onHash = () => {
      if (window.location.hash.length > 1) {
        setFragment(captureFragment());
        setGeneration((g) => g + 1);
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  if (fragment === null) return null;
  return (
    <AuthGuard>
      <ImportScreen key={generation} fragment={fragment} />
    </AuthGuard>
  );
}

function Frame({ children }: { children: ReactNode }) {
  return (
    <main className="flex min-h-dvh items-center justify-center bg-muted/40 p-4">
      <div className="grid w-full max-w-lg gap-5 rounded-2xl border bg-card p-6 shadow-lg sm:p-8">
        <AppLogo className="size-10" />
        {children}
      </div>
    </main>
  );
}

function Message({ title, body, actions }: { title: ReactNode; body?: ReactNode; actions?: ReactNode }) {
  return (
    <>
      <div className="grid gap-1.5">
        <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
        {body ? <p className="text-sm text-muted-foreground">{body}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap justify-end gap-2">{actions}</div> : null}
    </>
  );
}

function Warning({ children, tone = "warning" }: { children: ReactNode; tone?: "warning" | "danger" }) {
  return (
    <div
      role="note"
      className={
        tone === "danger"
          ? "flex gap-2.5 rounded-lg bg-destructive/10 p-3 text-[13px] text-destructive"
          : "flex gap-2.5 rounded-lg bg-warning/15 p-3 text-[13px] text-warning-foreground dark:text-warning"
      }
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

function leave(router: ReturnType<typeof useRouter>, to: string) {
  clearStash();
  router.replace(to);
}

function entryName(entry: LinkEntry): string {
  return entry.name || recognizeProvider(entry.baseURL)?.name || entry.host;
}

function ImportScreen({ fragment }: { fragment: string }) {
  const t = useT();
  const router = useRouter();
  const { access, loaded } = useProviderAccess();
  const enabled = loaded && access?.enabled === true;
  const providers = useLoad((signal) => listProviders(signal), "import-providers", enabled);
  const parsed = useMemo(() => parseImportFragment(fragment), [fragment]);

  const loading = !loaded || (enabled && providers.loading && !providers.data);
  const shown = !loading;

  // Once something other than the spinner shows, the stash has done its job.
  useEffect(() => {
    if (shown) clearStash();
  }, [shown]);

  if (loading) {
    return (
      <Frame>
        <div className="flex items-center justify-center gap-2 py-6 text-sm text-muted-foreground">
          <Spinner /> {t("settings.import.loading")}
        </div>
      </Frame>
    );
  }
  const back = (
    <Button variant="outline" onClick={() => leave(router, "/chat")}>
      {t("settings.import.backToChat")}
    </Button>
  );
  if (!enabled || !access) {
    return (
      <Frame>
        <Message title={t("settings.import.unavailableTitle")} body={t("settings.import.unavailableBody")} actions={back} />
      </Frame>
    );
  }
  if (!parsed.ok) {
    const { reason, entry } = parsed.error;
    const text = t(linkErrorKey(reason));
    return (
      <Frame>
        <Message
          title={t("settings.import.invalidTitle")}
          body={entry === undefined ? text : t("settings.import.entryError", { n: entry, reason: text })}
          actions={back}
        />
      </Frame>
    );
  }
  if (providers.error !== undefined && !providers.data) {
    // The limit and duplicate checks need the list: do not guess without it.
    return (
      <Frame>
        <LoadErrorNotice title={t("settings.import.loadFailed")} error={providers.error} onRetry={providers.reload} />
        <div className="flex flex-wrap justify-end gap-2">{back}</div>
      </Frame>
    );
  }
  const existing = providers.data ?? [];
  const max = access.maxPerUser;
  return parsed.entries.length === 1 ? (
    <SingleImport entry={parsed.entries[0] as LinkEntry} existing={existing} max={max} access={access} />
  ) : (
    <BatchImport entries={parsed.entries} existing={existing} max={max} access={access} />
  );
}

type Access = NonNullable<ReturnType<typeof useProviderAccess>["access"]>;

function EntryHeader({ entry }: { entry: LinkEntry }) {
  const t = useT();
  const Icon = iconFor("", recognizeProvider(entry.baseURL)?.icon);
  return (
    <div className="grid gap-3 rounded-xl border bg-background p-4">
      <div className="flex items-center gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-accent text-accent-foreground">
          <Icon className="size-5" />
        </span>
        <span className="truncate text-base font-semibold">{entryName(entry)}</span>
      </div>
      <dl className="grid gap-2 text-sm">
        <div className="grid gap-0.5">
          <dt className="text-xs text-muted-foreground">{t("settings.import.address")}</dt>
          <Tooltip label={entry.baseURL}>
            <dd className="break-all font-mono text-[13px]">{entry.baseURL}</dd>
          </Tooltip>
        </div>
        <div className="grid gap-0.5">
          <dt className="text-xs text-muted-foreground">{t("settings.import.key")}</dt>
          <dd className="font-mono text-[13px]">{maskKey(entry.key)}</dd>
        </div>
      </dl>
    </div>
  );
}

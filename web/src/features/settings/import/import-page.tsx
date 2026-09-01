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

function SingleImport({ entry, existing, max, access }: { entry: LinkEntry; existing: PersonalProvider[]; max: number; access: Access }) {
  const t = useT();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const atLimit = max > 0 && existing.length >= max;
  const duplicate = !atLimit && existing.some((p) => p.host === entry.host);

  return (
    <Frame>
      <Message title={t("settings.import.title")} body={t("settings.import.intro")} />
      <EntryHeader entry={entry} />
      <Warning tone="danger">
        <ShieldAlert className="mr-1 inline size-4 align-text-bottom" />
        {t("settings.import.securityWarning", { host: entry.host })}
      </Warning>
      {duplicate ? <Warning>{t("settings.import.duplicate", { host: entry.host })}</Warning> : null}
      {atLimit ? <Warning>{t("settings.import.limitReached", { max })}</Warning> : null}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="outline" onClick={() => leave(router, "/chat")}>
          {t("common.actions.cancel")}
        </Button>
        {atLimit ? (
          <Button onClick={() => leave(router, "/settings/models")}>{t("settings.import.manage")}</Button>
        ) : (
          <Button onClick={() => setOpen(true)}>{t("common.actions.continue")}</Button>
        )}
      </div>
      <AddProviderDialog
        open={open}
        onOpenChange={setOpen}
        access={access}
        source="link"
        locked
        initial={{ name: entry.name, baseURL: entry.baseURL, apiKey: entry.key, protocol: entry.protocol }}
        onCreated={() => {
          toast.success(t("settings.import.imported"));
          leave(router, "/settings/models");
        }}
      />
    </Frame>
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function BatchImport({ entries, existing, max, access }: { entries: LinkEntry[]; existing: PersonalProvider[]; max: number; access: Access }) {
  const t = useT();
  const router = useRouter();
  const errorMessage = useErrorMessage();
  const [statuses, setStatuses] = useState<EntryStatus[]>(() => entries.map(() => ({ kind: "pending" })));
  const statusRef = useRef(statuses);
  const [running, setRunning] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [created, setCreated] = useState(0);
  const [selecting, setSelecting] = useState<number | null>(null);
  const cancelled = useRef(false);
  const errorRef = useRef(errorMessage);
  errorRef.current = errorMessage;

  useEffect(() => {
    cancelled.current = false;
    return () => {
      cancelled.current = true;
    };
  }, []);

  const free = max > 0 ? max - existing.length - created : Number.POSITIVE_INFINITY;
  const noSlots = max > 0 && max - existing.length <= 0;
  const perModel = access.modelProtocols.length > 0;

  const report = useCallback((index: number, status: EntryStatus) => {
    const next = statusRef.current.map((s, i) => (i === index ? status : s));
    statusRef.current = next;
    if (!cancelled.current) setStatuses(next);
  }, []);

  const deps: BatchDeps = {
    probe: (entry) => probeProvider({ protocol: entry.protocol ?? resolveProtocol(entry.baseURL), baseURL: entry.baseURL, apiKey: entry.key }),
    create: (entry, models: AvailableModel[]) => {
      const protocol = entry.protocol ?? resolveProtocol(entry.baseURL);
      return createProvider({
        name: entry.name,
        protocol,
        baseURL: entry.baseURL,
        apiKey: entry.key,
        models: toRequestModels(
          models.map((m) => ({ name: m.name, protocols: m.suggestedProtocols.length ? m.suggestedProtocols : [protocol] })),
          perModel,
        ),
        source: "link",
      });
    },
    sleep,
    cancelled: () => cancelled.current,
    describe: (e) => errorRef.current(e),
    report,
    throttle: (e) => (e instanceof ApiError && e.status === 429 ? { retryAfter: e.retryAfterSeconds } : null),
  };

  const run = async (indices: number[]) => {
    if (running) return;
    setRunning(true);
    const outcome = await runBatch(entries, indices, free === Number.POSITIVE_INFINITY ? 1_000_000 : Math.max(0, free), deps);
    if (cancelled.current) return;
    setRunning(false);
    setAttempted(true);
    setCreated((c) => c + outcome.imported);
    if (outcome.imported > 0) toast.success(t("settings.import.batchDone", { count: outcome.imported }));
    if (statusRef.current.every((s) => s.kind === "imported")) leave(router, "/settings/models");
  };

  const pendingIndices = () => statusRef.current.flatMap((s, i) => (s.kind === "imported" ? [] : [i]));
  const slotsLeft = free === Number.POSITIVE_INFINITY ? Number.POSITIVE_INFINITY : Math.max(0, free);
  const pendingCount = statuses.filter((s) => s.kind !== "imported").length;

  if (noSlots) {
    return (
      <Frame>
        <Message
          title={t("settings.import.batchTitle", { count: entries.length })}
          body={t("settings.import.limitReached", { max })}
          actions={
            <>
              <Button variant="outline" onClick={() => leave(router, "/chat")}>
                {t("common.actions.cancel")}
              </Button>
              <Button onClick={() => leave(router, "/settings/models")}>{t("settings.import.manage")}</Button>
            </>
          }
        />
      </Frame>
    );
  }

  const label = (status: EntryStatus): ReactNode => {
    const effective: EntryStatus = status.kind !== "imported" && slotsLeft <= 0 ? { kind: "overLimit" } : status;
    switch (effective.kind) {
      case "pending":
        return <span className="text-muted-foreground">{t("settings.import.status.pending")}</span>;
      case "probing":
        return <Busy>{t("settings.import.status.probing")}</Busy>;
      case "saving":
        return <Busy>{t("settings.import.status.saving")}</Busy>;
      case "throttled":
        return <Busy>{t("settings.import.status.throttled", { seconds: effective.seconds })}</Busy>;
      case "imported":
        return (
          <span className="inline-flex items-center gap-1 text-success">
            <Check className="size-3.5" /> {t("settings.import.status.imported", { count: effective.count })}
          </span>
        );
      case "noModels":
        return <span className="text-warning-foreground dark:text-warning">{t("settings.import.status.noModels")}</span>;
      case "needsSelection":
        return <span className="text-warning-foreground dark:text-warning">{t("settings.import.status.needsSelection", { count: effective.count })}</span>;
      case "failed":
        return <span className="text-destructive">{t("settings.import.status.failed", { message: effective.message })}</span>;
      case "overLimit":
        return <span className="text-muted-foreground">{t("settings.import.status.overLimit")}</span>;
    }
  };

  const action = (status: EntryStatus, index: number): ReactNode => {
    if (status.kind === "needsSelection")
      return (
        <Button size="sm" variant="outline" disabled={running} onClick={() => setSelecting(index)}>
          {t("settings.import.selectModels")}
        </Button>
      );
    if (status.kind === "noModels" || status.kind === "failed")
      return (
        <Button size="sm" variant="outline" disabled={running} onClick={() => void run([index])}>
          {t("common.actions.retry")}
        </Button>
      );
    return null;
  };

  const done = attempted && !running;
  const selectedEntry = selecting === null ? null : (entries[selecting] ?? null);

  return (
    <Frame>
      <Message title={t("settings.import.batchTitle", { count: entries.length })} body={t("settings.import.batchIntro")} />
      <Warning tone="danger">{t("settings.import.batchSecurity")}</Warning>
      {Number.isFinite(slotsLeft) && slotsLeft < pendingCount ? <Warning>{t("settings.import.slotsLeft", { count: slotsLeft })}</Warning> : null}
      <ul className="grid max-h-[45vh] gap-2 overflow-y-auto pr-1">
        {entries.map((entry, index) => {
          const status = statuses[index] ?? ({ kind: "pending" } as EntryStatus);
          const dup = status.kind === "pending" ? existing.find((p) => p.host === entry.host) : undefined;
          const Icon = iconFor("", recognizeProvider(entry.baseURL)?.icon);
          return (
            <li key={`${entry.baseURL}-${index}`} className="grid gap-1.5 rounded-lg border bg-background p-3">
              <div className="flex items-center gap-2.5">
                <Icon className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate text-sm font-medium">{entryName(entry)}</span>
              </div>
              <span className="break-all font-mono text-xs text-muted-foreground">{entry.baseURL}</span>
              <span className="font-mono text-xs text-muted-foreground">{maskKey(entry.key)}</span>
              {dup ? <Badge tone="warning" className="w-fit">{t("settings.import.existing", { name: dup.name || dup.host })}</Badge> : null}
              <div className="flex items-center justify-between gap-2 text-[13px]">
                <span>{label(status)}</span>
                {action(status, index)}
              </div>
            </li>
          );
        })}
      </ul>
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="outline" onClick={() => leave(router, "/chat")}>
          {t("common.actions.cancel")}
        </Button>
        {done ? (
          <Button onClick={() => leave(router, "/settings/models")}>{t("settings.import.done")}</Button>
        ) : (
          <Button loading={running} disabled={running || pendingCount === 0} onClick={() => void run(pendingIndices())}>
            {t("settings.import.importAll")}
          </Button>
        )}
      </div>
      <AddProviderDialog
        open={selectedEntry !== null}
        onOpenChange={(open) => !open && setSelecting(null)}
        access={access}
        source="link"
        locked
        initial={selectedEntry ? { name: selectedEntry.name, baseURL: selectedEntry.baseURL, apiKey: selectedEntry.key, protocol: selectedEntry.protocol } : undefined}
        onCreated={(provider) => {
          if (selecting !== null) {
            report(selecting, { kind: "imported", count: provider.models.length });
            setCreated((c) => c + 1);
            if (statusRef.current.every((s) => s.kind === "imported")) {
              toast.success(t("settings.import.batchDone", { count: entries.length }));
              leave(router, "/settings/models");
            }
          }
        }}
      />
    </Frame>
  );
}

function Busy({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-muted-foreground">
      <Loader2 className="size-3.5 animate-spin" /> {children}
    </span>
  );
}

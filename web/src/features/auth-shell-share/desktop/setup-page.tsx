"use client";

import { useEffect, useState, type FormEvent } from "react";
import { ArrowLeft, Laptop, Server } from "lucide-react";
import { AppLogo } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { shell, ShellError } from "./bridge";
import { FALLBACK_DISTRIBUTION, loadDistribution, type Distribution } from "./distribution";
import { normalizeOrigin, probeServer, PROBE_TIMEOUT_MS, type ProbeResult } from "./server-url";

type Step = "pick" | "remote";

/**
 * First-run screen of the desktop app: use the bundled server on this
 * computer, or connect to a deployment. `onBound` runs once the shell has
 * pinned a server (and, for the local one, a session exists).
 */
export function ServerSetup({ onBound }: { onBound: (mode: "local" | "remote") => Promise<void> | void }) {
  const t = useT();
  const [distribution, setDistribution] = useState<Distribution | null>(null);
  const [step, setStep] = useState<Step>("pick");
  const [address, setAddress] = useState("");
  const [busy, setBusy] = useState<"local" | "remote" | null>(null);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    let live = true;
    void loadDistribution().then((d) => {
      if (!live) return;
      setDistribution(d);
      setAddress(d.policy.defaultServerUrl ?? "");
      // Without a local server there is nothing to pick between.
      if (!d.policy.localModeAllowed) setStep("remote");
    });
    return () => {
      live = false;
    };
  }, []);

  const policy = (distribution ?? FALLBACK_DISTRIBUTION).policy;

  const shellFailure = (error: unknown, fallback: "local" | "remote") => {
    const failure = error instanceof ShellError ? error : null;
    if (failure?.kind === "tabs") toast.error(t("desktopSetup.errors.alreadyOpen"));
    else if (failure?.kind === "policy" || failure?.message.startsWith("policy:"))
      toast.error(fallback === "local" ? t("desktopSetup.errors.localDisabled") : t("desktopSetup.errors.serverLocked"));
    else if (fallback === "local") toast.error(t("desktopSetup.errors.localFailed"), { description: failure?.message || undefined });
    else toast.error(t("desktopSetup.errors.invalidUrl"), { description: failure?.message || undefined });
  };

  const startLocal = async () => {
    if (busy) return;
    setBusy("local");
    try {
      await shell.setLocalServer();
      await onBound("local");
    } catch (error) {
      shellFailure(error, "local");
    } finally {
      setBusy(null);
    }
  };

  const connect = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    const origin = normalizeOrigin(address);
    if (!origin) {
      toast.error(t("desktopSetup.errors.invalidUrl"));
      return;
    }
    setBusy("remote");
    setChecking(true);
    let probe: ProbeResult;
    try {
      probe = await probeServer(origin);
    } finally {
      setChecking(false);
    }
    if (probe.kind !== "ok") {
      setBusy(null);
      if (probe.kind === "timeout") toast.error(t("desktopSetup.errors.timeout", { seconds: PROBE_TIMEOUT_MS / 1000 }));
      else if (probe.kind === "network") toast.error(t("desktopSetup.errors.network"));
      else toast.error(t("desktopSetup.errors.status", { status: probe.status }));
      return;
    }
    try {
      await shell.setRemoteServer(origin);
      await onBound("remote");
    } catch (error) {
      shellFailure(error, "remote");
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-background px-4 py-10">
      <div className="grid w-full max-w-sm gap-7 rounded-2xl border bg-card p-7 shadow-md">
        <div className="grid justify-items-center gap-3 text-center">
          <AppLogo className="size-12" />
          <h1 className="text-xl font-semibold tracking-tight">{t("desktopSetup.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("desktopSetup.subtitle")}</p>
        </div>

        {!distribution ? (
          <div aria-hidden className="h-24" />
        ) : step === "pick" ? (
          <div className="grid gap-2.5">
            <Button size="lg" onClick={() => void startLocal()} loading={busy === "local"} disabled={busy !== null}>
              <Laptop />
              {busy === "local" ? t("desktopSetup.local.starting") : t("desktopSetup.local.use")}
            </Button>
            <Button size="lg" variant="outline" onClick={() => setStep("remote")} disabled={busy !== null}>
              <Server />
              {t("desktopSetup.remote.connect")}
            </Button>
          </div>
        ) : (
          <form onSubmit={connect} className="grid gap-4">
            {!policy.localModeAllowed ? <p className="rounded-lg border bg-muted/40 p-3 text-[13px] text-muted-foreground">{t("desktopSetup.local.disabled")}</p> : null}
            <Field required>
              <FieldLabel>{t("desktopSetup.remote.address")}</FieldLabel>
              <Input
                name="server"
                type="url"
                inputMode="url"
                autoFocus
                autoCapitalize="none"
                spellCheck={false}
                placeholder="https://chat.example.com"
                value={address}
                readOnly={policy.serverUrlLocked}
                onChange={(e) => setAddress(e.target.value)}
              />
              {policy.serverUrlLocked ? <FieldHint>{t("desktopSetup.remote.managed")}</FieldHint> : null}
            </Field>
            <div className="flex gap-2">
              {policy.localModeAllowed ? (
                <Button type="button" variant="ghost" onClick={() => setStep("pick")} disabled={busy !== null}>
                  <ArrowLeft />
                  {t("common.actions.back")}
                </Button>
              ) : null}
              <Button type="submit" className="flex-1" loading={busy === "remote"} disabled={busy !== null || !address.trim()}>
                {checking ? t("desktopSetup.remote.checking") : t("desktopSetup.remote.submit")}
              </Button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}

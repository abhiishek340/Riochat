"use client";

import { Activity } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { Mono } from "../components/page";
import { toneFor } from "../components/status";
import { useLabel } from "../lib/use-label";
import { getRuntime, type RuntimeName, type RuntimeState } from "./api";

type State = { kind: "idle" } | { kind: "loading" } | { kind: "done"; result: RuntimeState } | { kind: "error"; message: string };

/**
 * A "test connection" panel for one external service. The answer always
 * describes the saved settings, so it says so while the draft differs, and it
 * is forgotten after any save (`resetKey` changes).
 */
export function RuntimeProbe({ name, dirty, resetKey }: { name: RuntimeName; dirty: boolean; resetKey: unknown }) {
  const t = useT();
  const message = useErrorMessage();
  const label = useLabel();
  const [state, setState] = useState<State>({ kind: "idle" });
  const run = useRef(0);

  useEffect(() => {
    run.current += 1;
    setState({ kind: "idle" });
  }, [resetKey]);

  const test = async () => {
    const mine = ++run.current;
    setState({ kind: "loading" });
    try {
      const result = await getRuntime(name);
      if (mine === run.current) setState({ kind: "done", result });
    } catch (error) {
      if (mine === run.current) setState({ kind: "error", message: message(error) });
    }
  };

  const status = state.kind === "done" ? state.result.status : "";
  return (
    <div className="mt-4 space-y-2 rounded-lg border bg-muted/30 p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{label("adminFiles.runtime.services", name)}</span>
        <Button size="sm" variant="outline" loading={state.kind === "loading"} onClick={test}>
          <Activity />
          {state.kind === "loading" ? t("adminFiles.runtime.testing") : t("adminFiles.runtime.test")}
        </Button>
        {state.kind === "done" ? (
          <>
            <Badge tone={toneFor(status)}>{label("adminFiles.runtime.status", status)}</Badge>
            <Badge tone={state.result.reachable ? "success" : "danger"}>{state.result.reachable ? t("adminFiles.runtime.reachable") : t("adminFiles.runtime.unreachable")}</Badge>
          </>
        ) : null}
      </div>
      {state.kind === "done" ? (
        <div className="space-y-1 text-[13px] text-muted-foreground">
          {state.result.baseURL ? <Mono className="break-all">{state.result.baseURL}</Mono> : null}
          {state.result.message ? <p>{state.result.message}</p> : null}
        </div>
      ) : null}
      {state.kind === "error" ? <p className="text-[13px] font-medium text-destructive">{t("adminFiles.runtime.failed")}: {state.message}</p> : null}
      {dirty && state.kind !== "idle" ? <p className="text-[13px] text-muted-foreground">{t("adminFiles.runtime.savedNote")}</p> : null}
    </div>
  );
}

"use client";

import { useEffect, useState } from "react";
import { isAbortError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { hasSession } from "../shared/session";
import { useLatest } from "../shared/use-latest";
import type { FileExtract, FileObject } from "../shared/types";
import { fetchExtract } from "./api";

export type ExtractState =
  | { phase: "idle" }
  | { phase: "blocked"; reason: "failed" | "notReady" }
  | { phase: "loading" }
  | { phase: "ready"; data: FileExtract }
  | { phase: "error"; message: string };

/** Loads the extracted text, but only while the Extract tab is showing. */
export function useExtract(file: FileObject | null, active: boolean): ExtractState {
  const errorMessage = useErrorMessage();
  const [state, setState] = useState<ExtractState>({ phase: "idle" });
  const errorRef = useLatest(errorMessage);
  const id = file?.fileID;
  const extractStatus = file?.extractStatus;
  const processingStatus = file?.processingStatus;
  const updatedAt = file?.updatedAt;

  useEffect(() => {
    if (!active || id === undefined) {
      setState({ phase: "idle" });
      return;
    }
    if ((extractStatus ?? "").trim().toLowerCase() !== "ready") {
      setState({ phase: "blocked", reason: (processingStatus ?? "").trim().toLowerCase() === "failed" ? "failed" : "notReady" });
      return;
    }
    const controller = new AbortController();
    setState({ phase: "loading" });
    void (async () => {
      try {
        if (!(await hasSession())) throw new Error("errors.auth.unauthorized");
        const data = await fetchExtract(id, controller.signal);
        if (!controller.signal.aborted) setState({ phase: "ready", data });
      } catch (error) {
        if (!isAbortError(error) && !controller.signal.aborted) setState({ phase: "error", message: errorRef.current(error) });
      }
    })();
    return () => controller.abort();
  }, [active, id, extractStatus, processingStatus, updatedAt, errorRef]);

  return state;
}

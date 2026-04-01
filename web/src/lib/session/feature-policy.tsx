"use client";

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "@/lib/api/client";
import type { FeaturePolicy } from "@/lib/api/types";
import { isRecord } from "@/lib/utils/guards";
import { useSession } from "./provider";

/** Defaults are "everything on": a missing field or a failed request never hides features. */
export const DEFAULT_FEATURE_POLICY: FeaturePolicy = {
  knowledgeBaseEnabled: true,
  processTraceEnabled: true,
  desktopDownloadEnabled: true,
  desktopDownloadURL: "",
};

export function readFeaturePolicy(payload: unknown): FeaturePolicy {
  const d = isRecord(payload) ? payload : {};
  const b = (key: keyof FeaturePolicy): boolean =>
    typeof d[key] === "boolean" ? (d[key] as boolean) : (DEFAULT_FEATURE_POLICY[key] as boolean);
  return {
    knowledgeBaseEnabled: b("knowledgeBaseEnabled"),
    processTraceEnabled: b("processTraceEnabled"),
    desktopDownloadEnabled: b("desktopDownloadEnabled"),
    desktopDownloadURL: typeof d.desktopDownloadURL === "string" ? d.desktopDownloadURL.trim() : "",
  };
}

interface PolicyValue {
  policy: FeaturePolicy;
  loaded: boolean;
  /** Called by admin screens after saving a toggle so the change shows at once. */
  patch: (next: Partial<FeaturePolicy>) => void;
}

const Ctx = createContext<PolicyValue>({ policy: DEFAULT_FEATURE_POLICY, loaded: false, patch: () => undefined });

export function FeaturePolicyProvider({ children }: { children: ReactNode }) {
  const { accessToken } = useSession();
  const [policy, setPolicy] = useState(DEFAULT_FEATURE_POLICY);
  const [loaded, setLoaded] = useState(false);
  const hasToken = accessToken !== "";

  useEffect(() => {
    if (!hasToken) return;
    let live = true;
    api.get<unknown>("/settings/feature-policy").then(
      (data) => {
        if (!live) return;
        setPolicy(readFeaturePolicy(data));
        setLoaded(true);
      },
      () => live && setLoaded(true), // fail open
    );
    return () => {
      live = false;
    };
  }, [hasToken]);

  const value = useMemo<PolicyValue>(
    () => ({ policy, loaded, patch: (next) => setPolicy((p) => ({ ...p, ...next })) }),
    [policy, loaded],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useFeaturePolicy(): PolicyValue {
  return useContext(Ctx);
}

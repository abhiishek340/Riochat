"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { absolutizeApiPath } from "@/lib/api/base-url";
import type { LoginProvider } from "@/lib/api/types";
import { useT } from "@/lib/i18n/provider";
import { isSilentProviderError, startProviderFlow } from "./provider-flow";

function ProviderLogo({ provider }: { provider: LoginProvider }) {
  const [failed, setFailed] = useState(false);
  const initial = Array.from(provider.name.trim())[0]?.toUpperCase() ?? "?";
  if (provider.logoURL && !failed) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- operator-supplied logo of unknown origin
      <img src={absolutizeApiPath(provider.logoURL)} alt="" className="size-5 rounded-sm object-contain" onError={() => setFailed(true)} />
    );
  }
  return (
    <span aria-hidden className="flex size-5 items-center justify-center rounded-sm bg-muted text-[11px] font-semibold text-muted-foreground">
      {initial}
    </span>
  );
}

export function ProviderButtons({
  providers,
  bridgeEnabled,
  intent,
  next,
}: {
  providers: LoginProvider[];
  bridgeEnabled: boolean;
  intent: "login" | "register";
  next: string;
}) {
  const t = useT();
  const router = useRouter();
  const [starting, setStarting] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);

  useEffect(() => () => abort.current?.abort(), []);

  const start = async (provider: LoginProvider) => {
    if (!bridgeEnabled) {
      toast.error(t("authFlow.providers.bridgeMissing"));
      return;
    }
    if (starting) return;
    setStarting(provider.slug);
    abort.current = new AbortController();
    try {
      await startProviderFlow({
        slug: provider.slug,
        intent,
        next,
        navigate: (path) => router.replace(path),
        signal: abort.current.signal,
      });
    } catch (error) {
      if (!isSilentProviderError(error)) toast.error(t("authFlow.providers.startFailed"));
    } finally {
      setStarting(null);
    }
  };

  return (
    <div className="grid gap-2">
      {providers.map((provider) => (
        <Button
          key={provider.publicID || provider.slug}
          type="button"
          variant="outline"
          className="w-full justify-center"
          loading={starting === provider.slug}
          disabled={starting !== null && starting !== provider.slug}
          onClick={() => void start(provider)}
        >
          {starting === provider.slug ? null : <ProviderLogo provider={provider} />}
          {t("authFlow.providers.continueWith", { provider: provider.name })}
        </Button>
      ))}
    </div>
  );
}

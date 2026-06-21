"use client";

import { useRef, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useT } from "@/lib/i18n/provider";
import { LoadErrorNotice, SettingsPage } from "../parts";
import { useLoad } from "../lib/use-load";
import { listMemories, MEMORY_SCOPES, type MemoryEntry, type MemoryScope } from "./api";
import { MemoryList } from "./memory-list";

/** Everything the assistant remembers about the user, by scope. */
export function MemorySettingsPage() {
  const t = useT();
  usePageTitle(t("settings.title"));
  const load = useLoad((signal) => listMemories(signal), "all");
  const [override, setOverride] = useState<MemoryEntry[] | null>(null);
  const [scope, setScope] = useState<MemoryScope>("profile");
  const all = override ?? load.data ?? [];
  const loading = load.loading && !override;
  // A failed read must not look like an empty list that can be added to.
  const failed = load.error !== undefined && !load.data && !override;

  const latest = useRef(all);
  latest.current = all;
  const replaceScope = (target: MemoryScope, next: MemoryEntry[]) =>
    setOverride([...next, ...latest.current.filter((e) => e.scope !== target && !next.some((n) => n.memoryKey === e.memoryKey))]);

  return (
    <SettingsPage title={t("settings.nav.memory")} description={t("settings.memory.pageDescription")}>
      {failed ? (
        <LoadErrorNotice title={t("settings.memory.toasts.loadFailed")} error={load.error} onRetry={load.reload} retrying={load.loading} />
      ) : (
        <Tabs value={scope} onValueChange={(v) => setScope(v as MemoryScope)}>
          <TabsList>
            {MEMORY_SCOPES.map((s) => (
              <TabsTrigger key={s} value={s}>
                {t(`settings.memory.scopes.${s}`)}
              </TabsTrigger>
            ))}
          </TabsList>
          {MEMORY_SCOPES.map((s) => (
            <TabsContent key={s} value={s}>
              <p className="mb-3 text-sm text-muted-foreground">{t(`settings.memory.scopeHints.${s}`)}</p>
              <MemoryList entries={all.filter((e) => e.scope === s)} loading={loading} scope={s} onChange={(next) => replaceScope(s, next)} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </SettingsPage>
  );
}

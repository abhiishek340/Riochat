"use client";

import { useState } from "react";
import { useT } from "@/lib/i18n/provider";
import { SettingsSection } from "../parts";
import { useLoad } from "../lib/use-load";
import { listMemories, PREFERENCE_LIMIT, type MemoryEntry } from "./api";
import { MemoryList } from "./memory-list";

/** The short list of personal preferences shown under the chat settings. */
export function PreferenceMemory() {
  const t = useT();
  const load = useLoad(async (signal) => (await listMemories(signal)).filter((e) => e.scope === "preference"), "preference");
  const [override, setOverride] = useState<MemoryEntry[] | null>(null);
  const entries = override ?? load.data ?? [];
  return (
    <SettingsSection title={t("settings.memory.preferencesTitle")} description={t("settings.memory.preferencesDescription")} bare>
      <MemoryList entries={entries} loading={load.loading && !override} scope="preference" limit={PREFERENCE_LIMIT} onChange={setOverride} />
    </SettingsSection>
  );
}

"use client";

import { useState } from "react";
import { Plus, Search } from "lucide-react";
import { Button, Input, Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { LibrarySection } from "./library-section";
import type { LibraryKind } from "./model";

export function LibraryPage() {
  const t = useT();
  const [kind, setKind] = useState<LibraryKind>("skills");
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState<LibraryKind | null>(null);

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-5 px-4 py-6 sm:px-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-tight">{t("libraryWs.title")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t("libraryWs.description")}</p>
        </div>
        <Button onClick={() => setCreating(kind)}>
          <Plus />
          {t("libraryWs.new")}
        </Button>
      </header>

      <Tabs value={kind} onValueChange={(v) => setKind(v === "prompts" ? "prompts" : "skills")} className="flex flex-col gap-5">
        <div className="flex flex-wrap items-center gap-3">
          <TabsList>
            <TabsTrigger value="skills">{t("libraryWs.tabs.skills")}</TabsTrigger>
            <TabsTrigger value="prompts">{t("libraryWs.tabs.prompts")}</TabsTrigger>
          </TabsList>
          <div className="relative min-w-48 flex-1">
            <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={kind === "skills" ? t("libraryWs.search.skills") : t("libraryWs.search.prompts")}
              aria-label={kind === "skills" ? t("libraryWs.search.skills") : t("libraryWs.search.prompts")}
              className="pl-9"
            />
          </div>
        </div>

        {/* One list is mounted at a time; the active tab controls this panel. */}
        <TabsContent value={kind} className="mt-0">
          <LibrarySection
            key={kind}
            kind={kind}
            search={search}
            creating={creating === kind}
            onCreatingChange={(open) => setCreating(open ? kind : null)}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}

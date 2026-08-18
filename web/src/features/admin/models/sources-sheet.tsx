"use client";

import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { ProbeDialog } from "../components/probe-dialog";
import { Sheet } from "../components/sheet";
import { parseKinds } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useSnapshot } from "../lib/use-snapshot";
import type { PlatformModel } from "../lib/wire";
import { bindSource, type ProbeResult } from "../llm-api";
import { blankSource, sourceBody, validateSources, type SourceDraft } from "./rules";
import { SourcePicker, useUpstreamChoices } from "./source-picker";
import { SourcesTable } from "./sources-table";
import { useSources } from "./use-sources";

/** All sources of one model with the same inline editing as the list, plus a form that binds another. */
export function SourcesSheet({ model, breakerOn, onClose, onModel, onChanged }: { model: PlatformModel | null; breakerOn: boolean; onClose: () => void; onModel: (id: number, change: (m: PlatformModel) => PlatformModel) => void; onChanged: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(model);
  const ctl = useSources({ model: shown, enabled: model !== null, breakerOn, pageSize: 25, onModel: (change) => shown && onModel(shown.id, change), onChanged });
  const upstreams = useUpstreamChoices(model !== null);
  const [draft, setDraft] = useState<SourceDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [probes, setProbes] = useState<ProbeResult[] | null>(null);

  useEffect(() => {
    if (model === null) setDraft(null);
  }, [model]);

  const bind = async () => {
    if (!shown || !draft) return;
    const issue = validateSources([draft]);
    if (issue || draft.upstreamID <= 0) {
      toast.error(t("adminModels.sources.bindFailed"), { description: t(`adminModels.sources.problems.${issue?.issue ?? "upstream"}`) });
      return;
    }
    setBusy(true);
    try {
      await bindSource(shown.id, sourceBody(draft));
      invalidateModels();
      toast.success(t("adminModels.sources.bound"));
      setDraft(null);
      onModel(shown.id, (m) => ({ ...m, sourceCount: m.sourceCount + 1 }));
      ctl.list.reload();
      onChanged();
    } catch (error) {
      toast.error(t("adminModels.sources.bindFailed"), { description: message(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Sheet open={model !== null} onOpenChange={(open) => !open && onClose()} size="xl" title={t("adminModels.sources.title", { name: shown?.platformModelName ?? "" })} description={t("adminModels.sources.description")}>
        {shown ? (
          <div className="space-y-6">
            <SourcesTable model={shown} ctl={ctl} breakerOn={breakerOn} onProbe={setProbes} />
            <section className="space-y-3 rounded-lg border bg-muted/30 p-4">
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold">{t("adminModels.sources.bindTitle")}</h3>
                {draft === null ? (
                  <Button size="sm" variant="outline" onClick={() => setDraft(blankSource("bind"))}>
                    <Plus />
                    {t("adminModels.sources.bindNew")}
                  </Button>
                ) : null}
              </div>
              {draft ? (
                <>
                  <SourcePicker row={draft} onChange={(patch) => setDraft((d) => (d ? { ...d, ...patch } : d))} kinds={parseKinds(shown.kindsJSON)} upstreams={upstreams} disabled={busy} />
                  <div className="flex justify-end gap-2">
                    <Button variant="outline" disabled={busy} onClick={() => setDraft(null)}>
                      {t("common.actions.cancel")}
                    </Button>
                    <Button loading={busy} onClick={bind}>
                      {t("adminModels.sources.bind")}
                    </Button>
                  </div>
                </>
              ) : (
                <p className="text-[13px] text-muted-foreground">{t("adminModels.sources.bindHint")}</p>
              )}
            </section>
          </div>
        ) : null}
      </Sheet>
      <ProbeDialog results={probes} onClose={() => setProbes(null)} />
    </>
  );
}

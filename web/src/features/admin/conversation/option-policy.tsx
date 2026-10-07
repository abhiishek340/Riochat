"use client";

import { BookOpen, Check, X } from "lucide-react";
import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { SettingsGroup } from "../components/settings-group";
import { OptionSelect } from "../components/toolbar";
import { problem, type Problem } from "../lib/problem";
import { fieldId, type Draft, type FieldDef } from "../lib/settings-form";
import type { SettingsForm } from "../lib/use-settings-form";
import { DEFAULT_ALLOWED, DEFAULT_DENIED, evaluatePaths, OPTION_PROTOCOLS, parseDoc, prettyDoc, SYSTEM_DENIED, type PathDoc } from "./rules";

const NS = "chat";
const MODE = `${NS}.model_option_policy_mode`;
const ALLOWED = `${NS}.model_option_allowed_paths`;
const DENIED = `${NS}.model_option_denied_paths`;

export const OPTION_FIELDS: FieldDef[] = [
  {
    ns: NS,
    key: "model_option_policy_mode",
    type: "select",
    label: "adminConversation.options.mode",
    hint: "adminConversation.options.modeHint",
    def: "allowlist",
    blankDefault: true,
    options: [
      { value: "allowlist", labelKey: "adminConversation.options.modes.allowlist" },
      { value: "denylist", labelKey: "adminConversation.options.modes.denylist" },
      { value: "disabled", labelKey: "adminConversation.options.modes.disabled" },
    ],
  },
  { ns: NS, key: "model_option_allowed_paths", type: "json", label: "adminConversation.options.allowed", hint: "adminConversation.options.allowedHint", def: prettyDoc(DEFAULT_ALLOWED), blankDefault: true, visible: (d) => (d[MODE] ?? "allowlist") === "allowlist" },
  { ns: NS, key: "model_option_denied_paths", type: "json", label: "adminConversation.options.denied", hint: "adminConversation.options.deniedHint", def: prettyDoc(DEFAULT_DENIED), blankDefault: true, visible: (d) => d[MODE] === "denylist" },
];

/** The documents that are about to be saved must parse. */
export function validateOptionDocs(draft: Draft): Problem | null {
  const visible = (draft[MODE] ?? "allowlist") === "allowlist" ? [ALLOWED] : draft[MODE] === "denylist" ? [DENIED] : [];
  for (const id of visible) {
    const parsed = parseDoc(draft[id] ?? "");
    if (!parsed.ok) return problem(`adminConversation.problems.doc.${parsed.reason}`);
  }
  return null;
}

function docOf(text: string | undefined, fallback: PathDoc): PathDoc {
  const parsed = parseDoc(text ?? "");
  return parsed.ok && Object.keys(parsed.doc).length > 0 ? parsed.doc : fallback;
}

const SAMPLE = "temperature\nreasoning.effort\nmax_tokens\nmodel\ncustom_flag";

export function OptionPolicy({ form }: { form: SettingsForm }) {
  const t = useT();
  const [guide, setGuide] = useState(false);
  const [protocol, setProtocol] = useState("default");
  const [sample, setSample] = useState(SAMPLE);
  const mode = form.draft[MODE] || "allowlist";
  const allowed = useMemo(() => docOf(form.draft[ALLOWED], DEFAULT_ALLOWED), [form.draft]);
  const denied = useMemo(() => docOf(form.draft[DENIED], DEFAULT_DENIED), [form.draft]);
  const results = useMemo(() => evaluatePaths(sample, protocol, mode, allowed, denied), [sample, protocol, mode, allowed, denied]);

  return (
    <>
      <SettingsGroup
        form={form}
        fields={OPTION_FIELDS}
        title={t("adminConversation.options.title")}
        description={t("adminConversation.options.description")}
        save={{ validate: validateOptionDocs }}
        actions={
          <Button variant="outline" size="sm" onClick={() => setGuide(true)}>
            <BookOpen />
            {t("adminConversation.options.guide")}
          </Button>
        }
      >
        <div className="mt-4 space-y-3 rounded-lg border bg-muted/30 p-3">
          <div className="text-sm font-medium">{t("adminConversation.options.previewTitle")}</div>
          <div className="grid gap-3 md:grid-cols-[14rem_1fr]">
            <div className="space-y-2">
              <OptionSelect value={protocol} onChange={setProtocol} label={t("adminConversation.options.protocol")} options={OPTION_PROTOCOLS.map((p) => ({ value: p, label: p }))} className="w-full" />
              <Textarea rows={6} value={sample} onChange={(e) => setSample(e.target.value)} spellCheck={false} aria-label={t("adminConversation.options.samplePaths")} className="font-mono text-[12.5px]" />
            </div>
            <ul className="grid content-start gap-1.5" aria-label={t("adminConversation.options.previewTitle")}>
              {results.length === 0 ? <li className="text-sm text-muted-foreground">{t("adminConversation.options.previewEmpty")}</li> : null}
              {results.map((r) => (
                <li key={r.path} className="flex flex-wrap items-center gap-2 text-sm">
                  {r.pass ? <Check aria-hidden className="size-4 text-success" /> : <X aria-hidden className="size-4 text-destructive" />}
                  <code className="font-mono text-[12.5px]">{r.path}</code>
                  <Badge tone={r.pass ? "success" : "danger"}>{r.pass ? t("adminConversation.options.passes") : t("adminConversation.options.filtered")}</Badge>
                  <span className="text-[13px] text-muted-foreground">{t(`adminConversation.options.verdicts.${r.verdict}`, { rule: r.rule ?? "" })}</span>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </SettingsGroup>
      <Dialog open={guide} onOpenChange={setGuide}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t("adminConversation.options.guide")}</DialogTitle>
            <DialogDescription>{t("adminConversation.options.guideIntro")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 text-sm">
            <section className="space-y-1">
              <h3 className="font-semibold">{t("adminConversation.options.guideSyntax")}</h3>
              <p className="text-muted-foreground">{t("adminConversation.options.guideSyntaxBody")}</p>
              <pre className="overflow-auto rounded-lg border bg-muted/50 p-3 font-mono text-[12.5px]">{`{\n  "default": ["temperature", "response_format.type"],\n  "openai_responses": ["reasoning.effort"]\n}`}</pre>
            </section>
            <section className="space-y-1">
              <h3 className="font-semibold">{t("adminConversation.options.guideStrategies")}</h3>
              <p className="text-muted-foreground">{t("adminConversation.options.guideStrategiesBody")}</p>
            </section>
            <section className="space-y-1">
              <h3 className="font-semibold">{t("adminConversation.options.guideProtocols")}</h3>
              <p className="flex flex-wrap gap-1">
                {OPTION_PROTOCOLS.map((p) => (
                  <code key={p} className="rounded bg-muted px-1.5 py-0.5 font-mono text-[12px]">
                    {p}
                  </code>
                ))}
              </p>
            </section>
            <section className="space-y-1">
              <h3 className="font-semibold">{t("adminConversation.options.guideSystem")}</h3>
              <p className="text-muted-foreground">{t("adminConversation.options.guideSystemBody")}</p>
              <p className="flex flex-wrap gap-1">
                {SYSTEM_DENIED.map((p) => (
                  <code key={p} className="rounded bg-destructive/10 px-1.5 py-0.5 font-mono text-[12px] text-destructive">
                    {p}
                  </code>
                ))}
              </p>
            </section>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

export const OPTION_IDS = { mode: fieldId(OPTION_FIELDS[0] as FieldDef) };

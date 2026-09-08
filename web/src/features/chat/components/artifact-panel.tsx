"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy, Download, ShieldCheck, X } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger, Tooltip, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useCopy } from "../hooks/use-copy";
import { artifactLabel, downloadName, type Artifact } from "../lib/artifacts";
import { downloadText } from "../lib/clipboard";
import { captureTheme, PERMISSIONS_POLICY, previewDocument, SANDBOX_ATTR, svgDataUrl, validateSvg, type PreviewTheme } from "../lib/sandbox-doc";
import { highlight } from "../lib/markdown/highlight";

/** Pinned-to-newest live source of a block that is still being written. */
function LiveSource({ code, lang }: { code: string; lang: string }) {
  const ref = useRef<HTMLPreElement>(null);
  const pinned = useRef(true);
  const tokens = useMemo(() => highlight(code, lang), [code, lang]);
  useEffect(() => {
    const el = ref.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [code]);
  return (
    <pre
      ref={ref}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight <= 24;
      }}
      className="rio-pre !max-h-none h-full overflow-auto"
    >
      <code>{tokens.map((tok, i) => (tok.type === "plain" ? tok.text : <span key={i} className={`tok-${tok.type}`}>{tok.text}</span>))}</code>
    </pre>
  );
}

export function PreviewFrame({ artifact, theme, title }: { artifact: Artifact; theme: PreviewTheme; title: string }) {
  const t = useT();
  const srcDoc = useMemo(() => (artifact.kind === "svg" ? "" : previewDocument(artifact.kind, artifact.code, theme)), [artifact.kind, artifact.code, theme]);
  const svg = useMemo(() => (artifact.kind === "svg" ? validateSvg(artifact.code) : null), [artifact.kind, artifact.code]);
  if (artifact.kind === "svg") {
    if (svg && !svg.ok) return <p role="alert" className="p-4 text-sm text-destructive">{t("chatRich.panel.invalidSvg")}</p>;
    return (
      <div className="flex size-full items-center justify-center overflow-auto bg-[repeating-conic-gradient(var(--muted)_0%_25%,transparent_0%_50%)] [background-size:16px_16px] p-4">
        {/* eslint-disable-next-line @next/next/no-img-element -- an <img> never runs scripts in the SVG */}
        <img src={svgDataUrl(artifact.code)} alt={title} className="max-h-full max-w-full" referrerPolicy="no-referrer" />
      </div>
    );
  }
  return <iframe title={title} sandbox={SANDBOX_ATTR} referrerPolicy="no-referrer" allow={PERMISSIONS_POLICY} srcDoc={srcDoc} className="size-full border-0 bg-background" />;
}

export function ArtifactPanel({
  artifacts,
  selected,
  onSelect,
  onClose,
}: {
  artifacts: readonly Artifact[];
  selected: number;
  onSelect: (index: number) => void;
  onClose: () => void;
}) {
  const t = useT();
  const { copied, copy } = useCopy();
  const artifact = artifacts[selected] ?? artifacts[artifacts.length - 1];
  const [tab, setTab] = useState("preview");
  const [theme, setTheme] = useState<PreviewTheme>(() => captureTheme());

  // The theme snapshot follows light/dark changes without rebuilding on every render.
  useEffect(() => {
    const el = document.documentElement;
    const mo = new MutationObserver(() => setTheme(captureTheme()));
    mo.observe(el, { attributes: true, attributeFilter: ["class", "data-accent"] });
    return () => mo.disconnect();
  }, []);

  if (!artifact) return null;
  const download = () => {
    if (artifact.kind === "svg") downloadText(artifact.code, downloadName("svg"), "image/svg+xml");
    else downloadText(previewDocument(artifact.kind, artifact.code, theme), downloadName(artifact.kind), "text/html;charset=utf-8");
  };
  const lang = artifact.kind === "javascript" ? "js" : artifact.kind;
  const building = !artifact.complete;

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-artifact-panel>
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <h2 className="text-sm font-semibold">{t("chatRich.panel.title")}</h2>
        {artifacts.length > 1 ? (
          <select
            value={selected}
            onChange={(e) => onSelect(Number(e.target.value))}
            aria-label={t("chatRich.panel.select")}
            className="h-7 rounded-md border bg-background px-1.5 text-xs"
          >
            {artifacts.map((a, i) => (
              <option key={a.id} value={i}>
                {artifactLabel(a.kind, a.index + 1)}
              </option>
            ))}
          </select>
        ) : null}
        <span className={cn("rounded-full px-2 py-0.5 text-[11px]", building ? "bg-primary/12 text-primary" : "bg-muted text-muted-foreground")}>
          {building ? t("chatRich.panel.live") : t("chatRich.panel.complete")}
        </span>
        <div className="ml-auto flex items-center gap-0.5">
          <Tooltip label={copied ? t("chatRich.md.copied") : t("chatRich.panel.copySource")}>
            <button
              type="button"
              aria-label={t("chatRich.panel.copySource")}
              onClick={async () => {
                if (!(await copy(artifact.code))) toast.error(t("chatRich.md.copyFailed"));
              }}
              className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            >
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </button>
          </Tooltip>
          <Tooltip label={artifact.kind === "svg" ? t("chatRich.panel.downloadSvg") : t("chatRich.panel.downloadHtml")}>
            <button type="button" aria-label={artifact.kind === "svg" ? t("chatRich.panel.downloadSvg") : t("chatRich.panel.downloadHtml")} onClick={download} disabled={building} className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-40">
              <Download className="size-4" />
            </button>
          </Tooltip>
          <Tooltip label={t("chatRich.panel.close")}>
            <button type="button" aria-label={t("chatRich.panel.close")} onClick={onClose} className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground">
              <X className="size-4" />
            </button>
          </Tooltip>
        </div>
      </div>
      <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
        <div className="px-3 pt-2">
          <TabsList>
            <TabsTrigger value="preview">{t("chatRich.panel.preview")}</TabsTrigger>
            <TabsTrigger value="source">{t("chatRich.panel.source")}</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="preview" className="m-0 mt-2 min-h-0 flex-1 overflow-hidden">
          {artifact.code.trim() === "" ? (
            <p className="p-4 text-sm text-muted-foreground">{t("chatRich.panel.empty")}</p>
          ) : building ? (
            <div className="flex h-full flex-col">
              <p className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
                <span className="rio-typing"><span /><span /><span /></span>
                {t("chatRich.panel.generating")}
              </p>
              <div className="min-h-0 flex-1">
                <LiveSource code={artifact.code} lang={lang} />
              </div>
            </div>
          ) : (
            <PreviewFrame artifact={artifact} theme={theme} title={t("chatRich.panel.previewTitle")} />
          )}
        </TabsContent>
        <TabsContent value="source" className="m-0 mt-2 min-h-0 flex-1 overflow-hidden">
          <LiveSource code={artifact.code} lang={lang} />
        </TabsContent>
      </Tabs>
      <p className="flex items-start gap-1.5 border-t px-3 py-2 text-[11px] text-muted-foreground">
        <ShieldCheck className="mt-0.5 size-3.5 shrink-0" />
        {t("chatRich.panel.securityNote")}
      </p>
    </div>
  );
}

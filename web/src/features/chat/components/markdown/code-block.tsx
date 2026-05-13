"use client";

import { memo, useMemo } from "react";
import { Check, Copy, Eye } from "lucide-react";
import { Tooltip } from "@/components/ui";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useCopy } from "../../hooks/use-copy";
import { detectKind } from "../../lib/artifacts";
import { highlight } from "../../lib/markdown/highlight";
import { useMarkdownContext } from "./context";

const LABELS: Record<string, string> = { js: "JavaScript", javascript: "JavaScript", ts: "TypeScript", typescript: "TypeScript", tsx: "TSX", jsx: "JSX", py: "Python", python: "Python", sh: "Shell", bash: "Bash", zsh: "Zsh", html: "HTML", css: "CSS", json: "JSON", yaml: "YAML", yml: "YAML", sql: "SQL", go: "Go", rs: "Rust", rust: "Rust", cpp: "C++", c: "C", cs: "C#", java: "Java", kt: "Kotlin", rb: "Ruby", php: "PHP", md: "Markdown", markdown: "Markdown", svg: "SVG", xml: "XML", diff: "Diff", text: "Text" };

export function languageLabel(lang: string): string {
  return LABELS[lang] ?? (lang ? lang : "Text");
}

export const CodeBlock = memo(function CodeBlock({ code, lang, closed }: { code: string; lang: string; closed: boolean }) {
  const t = useT();
  const ctx = useMarkdownContext();
  const { copied, copy } = useCopy();
  const tokens = useMemo(() => highlight(code, lang), [code, lang]);
  const kind = detectKind(lang || "text", code);
  const canPreview = !!ctx.openCode && !!kind && code.trim() !== "";

  return (
    <div className="group/code my-3 overflow-hidden rounded-xl border bg-muted/50" data-code-block data-lang={lang || "text"}>
      <div className="flex items-center justify-between gap-2 border-b bg-muted/60 px-3 py-1.5 text-xs text-muted-foreground" data-capture-hide>
        <span className="font-medium">{languageLabel(lang)}</span>
        <span className="flex items-center gap-1">
          {canPreview ? (
            <Tooltip label={t("chatRich.md.preview")}>
              <button
                type="button"
                onClick={() => ctx.openCode?.({ kind: kind!, code, lang })}
                aria-label={t("chatRich.md.preview")}
                className="inline-flex items-center gap-1 rounded-md px-1.5 py-1 hover:bg-accent hover:text-accent-foreground"
              >
                <Eye className="size-3.5" />
                <span>{t("chatRich.md.preview")}</span>
              </button>
            </Tooltip>
          ) : null}
          <Tooltip label={copied ? t("chatRich.md.copied") : t("chatRich.md.copy")}>
            <button
              type="button"
              onClick={async () => {
                const ok = await copy(code.replace(/\n$/, ""));
                if (!ok) toast.error(t("chatRich.md.copyFailed"));
              }}
              aria-label={t("chatRich.md.copy")}
              className="inline-flex items-center gap-1 rounded-md px-1.5 py-1 hover:bg-accent hover:text-accent-foreground"
            >
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            </button>
          </Tooltip>
        </span>
      </div>
      <pre className="rio-pre" tabIndex={0} aria-label={languageLabel(lang)}>
        <code>
          {tokens.map((tok, i) => (tok.type === "plain" ? tok.text : <span key={i} className={`tok-${tok.type}`}>{tok.text}</span>))}
        </code>
      </pre>
      {!closed ? <div className="h-0.5 w-full animate-pulse bg-primary/40" aria-hidden /> : null}
    </div>
  );
});

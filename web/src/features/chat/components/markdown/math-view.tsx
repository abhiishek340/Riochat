"use client";

import { memo, useMemo } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { copyText } from "../../lib/clipboard";
import { replaceArrowsInMath } from "../../lib/markdown/normalize";
import { texToMathML } from "../../lib/markdown/math";

export const MathView = memo(function MathView({ tex, display }: { tex: string; display: boolean }) {
  const t = useT();
  const result = useMemo(() => texToMathML(replaceArrowsInMath(tex), display), [tex, display]);
  const onCopy = async () => {
    const selection = window.getSelection();
    if (selection && !selection.isCollapsed) return;
    const source = display ? `$$\n${tex}\n$$` : `$${tex}$`;
    if (await copyText(source)) toast.success(t("chatRich.md.latexCopied"));
    else toast.error(t("chatRich.md.latexCopyFailed"));
  };
  const Tag = display ? "div" : "span";
  if (result.error) {
    return (
      <Tag className="math-error" role="button" tabIndex={0} title={t("chatRich.md.latexCopy")} onClick={onCopy} onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onCopy()}>
        {display ? `$$${tex}$$` : `$${tex}$`}
      </Tag>
    );
  }
  return (
    <Tag
      className={display ? "math-block" : "math-inline"}
      role="button"
      tabIndex={0}
      aria-label={t("chatRich.md.latexCopy")}
      title={t("chatRich.md.latexCopy")}
      onClick={onCopy}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          void onCopy();
        }
      }}
      dangerouslySetInnerHTML={{ __html: result.html }}
    />
  );
});

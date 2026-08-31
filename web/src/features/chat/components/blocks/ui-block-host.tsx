"use client";

import { memo, useCallback, useMemo, useState, useSyncExternalStore } from "react";
import { AlertTriangle, Loader2, RotateCcw } from "lucide-react";
import { Button, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { parseBlock, sniffComponent, type Block } from "../../lib/blocks/block";
import type { Value } from "../../lib/blocks/expression";
import { declaredState, diffState, mergeState, resolveLive, type ButtonAction } from "../../lib/blocks/live";
import { copyText } from "../../lib/clipboard";
import { CodeBlock } from "../markdown/code-block";
import { useMarkdownContext } from "../markdown/context";
import { NodeView, type RenderCtx } from "./nodes";

export { WidgetHost } from "./widget-host";

function Skeleton({ component }: { component: string }) {
  const t = useT();
  return (
    <div className="my-3 flex items-center gap-2 rounded-xl border bg-muted/30 px-3 py-4 text-sm text-muted-foreground" role="status">
      <Loader2 className="size-4 animate-spin" />
      {component ? t("chatBlocks.loadingNamed", { component }) : t("chatBlocks.loading")}
    </div>
  );
}

function Fallback({ message, code }: { message: string; code: string }) {
  const t = useT();
  return (
    <div className="my-3 rounded-xl border border-dashed">
      <p className="flex items-start gap-2 px-3 py-2 text-xs text-muted-foreground">
        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
        {t("chatBlocks.invalidBlock", { message })}
      </p>
      <CodeBlock code={code} lang="json" closed />
    </div>
  );
}

export const UiBlockHost = memo(function UiBlockHost({ code, closed }: { code: string; closed: boolean }) {
  const parsed = useMemo(() => parseBlock(code, !closed), [code, closed]);
  if (parsed.kind === "invalid") return closed ? <Fallback message={parsed.message} code={code} /> : <Skeleton component={sniffComponent(code)} />;
  if (parsed.kind === "incomplete") return <Skeleton component={parsed.component || sniffComponent(code)} />;
  if (!parsed.block.complete || !closed) return <Skeleton component={parsed.block.component} />;
  return <LiveBlock block={parsed.block} />;
});

const EMPTY: Record<string, unknown> = {};

function LiveBlock({ block }: { block: Block }) {
  const t = useT();
  const { blocks: api } = useMarkdownContext();
  const persistent = !!api && block.key !== "" && !api.duplicateKeys.has(block.key);
  const declared = useMemo(() => declaredState(block), [block]);

  // Persisted state is read through the store; without persistence changes stay in this component.
  const [localOverride, setLocalOverride] = useState<Record<string, Value>>({});
  const subscribe = useCallback((cb: () => void) => (api ? api.subscribe(cb) : () => undefined), [api]);
  const storedJson = useSyncExternalStore(
    subscribe,
    () => (persistent && api ? JSON.stringify(api.readState(block.key) ?? null) : "null"),
    () => "null",
  );
  const stored = useMemo<Record<string, unknown>>(() => {
    try {
      return (JSON.parse(storedJson) as Record<string, unknown> | null) ?? EMPTY;
    } catch {
      return EMPTY;
    }
  }, [storedJson]);

  const source = persistent ? stored : localOverride;
  const current = useMemo(() => mergeState(declared.state, source), [declared.state, source]);
  const resolution = useMemo(() => resolveLive(block, current), [block, current]);
  const changed = useMemo(() => diffState(declared.state, current), [declared.state, current]);
  const dirty = Object.keys(changed).length > 0;

  const commit = useCallback(
    (next: Record<string, Value>) => {
      const diff = diffState(declared.state, next);
      if (persistent && api) api.writeState(block.key, Object.keys(diff).length > 0 ? diff : null, block.title || undefined);
      else setLocalOverride(diff);
    },
    [api, block.key, block.title, declared.state, persistent],
  );

  const set = useCallback((name: string, value: Value) => commit({ ...current, [name]: value }), [commit, current]);

  const act = useCallback(
    (action: ButtonAction) => {
      if (!action) return;
      if (action.kind === "set") commit({ ...current, ...action.patch });
      else if (action.kind === "copy") {
        void copyText(action.text).then((ok) => (ok ? toast.success(t("chatBlocks.copied")) : toast.error(t("chatBlocks.copyFailed"))));
      } else if (api?.ask && !api.busy) api.ask(action.text);
    },
    [api, commit, current, t],
  );

  const ctx: RenderCtx = useMemo(
    () => ({ values: resolution.values, set, act, declared: declared.state, busy: api?.busy ?? false, canAsk: !!api?.ask }),
    [resolution.values, set, act, declared.state, api?.busy, api?.ask],
  );

  const issues = resolution.issues.length + declared.issues.length;
  if (!resolution.root) return null;
  return (
    <div className="my-3 min-w-0 rounded-xl" data-ui-block={block.key} data-component={block.component}>
      {block.title && typeof resolution.root.props.title !== "string" ? <p className="mb-1.5 text-sm font-medium">{block.title}</p> : null}
      <NodeView node={resolution.root} ctx={ctx} />
      {dirty || issues > 0 ? (
        <div className="mt-1.5 flex items-center justify-between gap-2 text-xs text-muted-foreground" data-capture-hide>
          {issues > 0 ? (
            <span title={[...declared.issues, ...resolution.issues].map((i) => `${i.path}: ${i.message}`).join("\n")} className="flex items-center gap-1">
              <AlertTriangle className="size-3" />
              {t("chatBlocks.formulaIssues", { count: issues })}
            </span>
          ) : (
            <span />
          )}
          {dirty ? (
            <Button variant="ghost" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => commit(declared.state)}>
              <RotateCcw className="size-3" />
              {t("chatBlocks.ask.restore")}
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}


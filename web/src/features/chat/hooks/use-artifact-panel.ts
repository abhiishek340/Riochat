"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMediaQuery } from "@/hooks/use-media-query";
import { extractArtifacts, pickArtifact, replacementFor, type Artifact, type ArtifactKind } from "../lib/artifacts";
import type { ChatMessage } from "../types";

interface Selection {
  messageKey: string;
  id: string;
  owner: string;
  kind: ArtifactKind;
  index: number;
  code: string;
  streaming: boolean;
  messageId: string;
}

export interface ArtifactPanelState {
  open: boolean;
  artifacts: Artifact[];
  selected: number;
  messageKey: string;
  select: (index: number) => void;
  openCode: (m: ChatMessage, args: { kind: ArtifactKind; code: string }) => void;
  close: () => void;
}

function ownerOf(m: ChatMessage): string {
  return m.runId || m.id;
}

/**
 * The code-preview panel: which message's artifacts are shown, which one is
 * selected, automatic opening while one streams, and keeping a closed one closed.
 */
export function useArtifactPanel(path: readonly ChatMessage[], chatKey: string): ArtifactPanelState {
  const wide = useMediaQuery("(min-width: 768px)");
  const [sel, setSel] = useState<Selection | null>(null);
  const closed = useRef(new Set<string>());
  const prevKey = useRef(chatKey);

  const byMessage = useMemo(() => {
    const out = new Map<string, Artifact[]>();
    for (const m of path) {
      if (m.role !== "assistant") continue;
      const list = extractArtifacts(m.content, { owner: ownerOf(m), streaming: m.live?.streaming === true || m.status === "pending" });
      if (list.length > 0) out.set(m.key, list);
    }
    return out;
  }, [path]);

  const slot = (messageKey: string, kind: ArtifactKind, index: number) => `${messageKey}|${kind}|${index}`;

  // Switching conversation closes the panel unless a streaming artifact moves from a draft to its conversation.
  useEffect(() => {
    if (prevKey.current === chatKey) return;
    const from = prevKey.current;
    prevKey.current = chatKey;
    closed.current.clear();
    setSel((cur) => (cur && cur.streaming && from === "draft" && byMessage.has(cur.messageKey) ? cur : null));
  }, [chatKey, byMessage]);

  // Keep the selection pointing at the same logical artifact when ids change.
  useEffect(() => {
    setSel((cur) => {
      if (!cur) return cur;
      const list = byMessage.get(cur.messageKey);
      if (!list) return null;
      const next = replacementFor({ id: cur.id, owner: cur.owner, kind: cur.kind, index: cur.index, code: cur.code, streaming: cur.streaming, messageId: cur.messageId }, list.map((a) => ({ ...a, messageId: cur.messageId })));
      if (!next) return null;
      if (next.id === cur.id && next.code === cur.code && next.streaming === cur.streaming) return cur;
      return { ...cur, id: next.id, owner: next.owner, code: next.code, streaming: next.streaming, index: next.index, kind: next.kind };
    });
  }, [byMessage]);

  // Auto-open the newest artifact while it streams (inline viewports only).
  useEffect(() => {
    if (!wide) return;
    for (let i = path.length - 1; i >= 0; i--) {
      const m = path[i] as ChatMessage;
      if (m.role !== "assistant") continue;
      const list = byMessage.get(m.key);
      if (!list || m.live?.streaming !== true) return;
      const last = list[list.length - 1] as Artifact;
      if (!last.streaming) return;
      if (closed.current.has(slot(m.key, last.kind, last.index))) return;
      setSel((cur) => (cur && cur.messageKey === m.key && cur.index === last.index && cur.kind === last.kind ? cur : { messageKey: m.key, id: last.id, owner: last.owner, kind: last.kind, index: last.index, code: last.code, streaming: true, messageId: m.id }));
      return;
    }
  }, [byMessage, path, wide]);

  const artifacts = useMemo(() => (sel ? (byMessage.get(sel.messageKey) ?? []) : []), [sel, byMessage]);
  const selected = sel ? Math.max(0, artifacts.findIndex((a) => a.id === sel.id)) : 0;

  const select = useCallback(
    (index: number) => {
      const a = artifacts[index];
      if (!a || !sel) return;
      setSel({ ...sel, id: a.id, owner: a.owner, kind: a.kind, index: a.index, code: a.code, streaming: a.streaming });
    },
    [artifacts, sel],
  );

  const openCode = useCallback(
    (m: ChatMessage, args: { kind: ArtifactKind; code: string }) => {
      const list = byMessage.get(m.key) ?? extractArtifacts(m.content, { owner: ownerOf(m), streaming: false });
      const at = pickArtifact(list, args.kind, args.code);
      const a = list[at];
      if (!a) return;
      closed.current.delete(slot(m.key, a.kind, a.index));
      setSel({ messageKey: m.key, id: a.id, owner: a.owner, kind: a.kind, index: a.index, code: a.code, streaming: a.streaming, messageId: m.id });
    },
    [byMessage],
  );

  const close = useCallback(() => {
    if (sel) closed.current.add(slot(sel.messageKey, sel.kind, sel.index));
    setSel(null);
  }, [sel]);

  return { open: !!sel && artifacts.length > 0, artifacts, selected, messageKey: sel?.messageKey ?? "", select, openCode, close };
}

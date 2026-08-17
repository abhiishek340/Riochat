"use client";

import { useEffect, useMemo, useRef } from "react";
import { scanMessageBlocks } from "../lib/blocks/block";
import { tempEngine } from "../state/temp-engine";
import { installUiState, readBlockState, subscribeUiState, syncWriteTarget, writeBlockState, type WriteContext } from "../state/ui-state";
import type { ChatMessage } from "../types";
import type { BlockHostApi } from "../components/markdown/context";

interface Args {
  message: ChatMessage;
  conversationKey: string;
  temporary: boolean;
  readOnly: boolean;
  busy: boolean;
  /** The reply is still being written. */
  live: boolean;
  ask: (text: string) => void;
  prefill: (text: string) => void;
  openInPanel?: (blockKey: string) => void;
}

const NO_KEYS: ReadonlySet<string> = new Set();

/** The interactive-block services a message hands to its markdown. Stable while the message stays the same. */
export function useBlockHostApi({ message, conversationKey, temporary, readOnly, busy, live, ask, prefill, openInPanel }: Args): BlockHostApi | undefined {
  const latest = useRef({ message, ask, prefill, openInPanel });
  useEffect(() => {
    latest.current = { message, ask, prefill, openInPanel };
  });
  const hasBlocks = message.role === "assistant" && message.content.includes("riochat-ui");

  useEffect(() => {
    installUiState();
  }, []);

  useEffect(() => {
    if (hasBlocks && !live) syncWriteTarget({ conversationId: conversationKey, message, temporary });
  }, [hasBlocks, live, conversationKey, message, temporary]);

  return useMemo<BlockHostApi | undefined>(() => {
    if (!hasBlocks || readOnly) return undefined;
    const duplicates = live ? NO_KEYS : scanMessageBlocks(latest.current.message.content).duplicates;
    const ctx = (): WriteContext => ({
      conversationId: conversationKey,
      message: latest.current.message,
      temporary,
      sink: temporary
        ? (messageId, blockId, state) => {
            const cur = { ...(tempEngine.uiStates.get(messageId) ?? {}) };
            if (state && Object.keys(state).length > 0) cur[blockId] = state;
            else delete cur[blockId];
            tempEngine.uiStates.set(messageId, cur);
          }
        : undefined,
    });
    return {
      messageId: message.id,
      readState: (blockId) => readBlockState(ctx(), blockId),
      writeState: (blockId, state, title) => writeBlockState(ctx(), blockId, state, title),
      subscribe: subscribeUiState,
      ask: (text) => latest.current.ask(text),
      prefill: (text) => latest.current.prefill(text),
      busy,
      openInPanel: openInPanel ? (key) => latest.current.openInPanel?.(key) : undefined,
      duplicateKeys: duplicates,
    };
  }, [hasBlocks, readOnly, live, conversationKey, temporary, message.id, busy, openInPanel]);
}

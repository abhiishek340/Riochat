"use client";

import { createContext, useContext } from "react";
import type { ArtifactKind } from "../../lib/artifacts";

export interface MarkdownActions {
  /** Opens the side-panel preview for a code block. */
  openCode?: (args: { kind: ArtifactKind; code: string; lang: string }) => void;
  /** Image actions: can this image go back to the composer, and do it. */
  imageEditable?: (src: string) => boolean;
  editImage?: (src: string, alt: string) => void;
  /** Interactive block host support (set by the message). */
  blocks?: BlockHostApi;
}

export interface BlockHostApi {
  messageId: string;
  /** Persisted block state of this message. */
  readState: (blockId: string) => Record<string, unknown> | undefined;
  writeState: (blockId: string, state: Record<string, unknown> | null, title?: string) => void;
  subscribe: (cb: () => void) => () => void;
  ask?: (text: string) => void;
  prefill?: (text: string) => void;
  busy: boolean;
  openInPanel?: (blockKey: string) => void;
  /** Keys that occur more than once in the message do not persist. */
  duplicateKeys: ReadonlySet<string>;
}

export interface MarkdownContextValue extends MarkdownActions {
  /** Prefix for heading ids so several messages on a page do not collide. */
  scope: string;
  streaming: boolean;
  readOnly: boolean;
}

export const MarkdownContext = createContext<MarkdownContextValue>({ scope: "md", streaming: false, readOnly: false });

export function useMarkdownContext(): MarkdownContextValue {
  return useContext(MarkdownContext);
}

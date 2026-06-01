"use client";

import { createContext, useContext } from "react";
import type { Currency } from "../../lib/billing";
import type { ChatPrefs } from "../../lib/settings";
import type { ChatAttachment, ChatMessage, ModelDTO } from "../../types";
import type { ArtifactKind } from "../../lib/artifacts";

export interface MessageActions {
  retry: (m: ChatMessage) => void;
  continueReply: (m: ChatMessage) => void;
  /** Saves edited text of this message only. */
  saveEdit: (m: ChatMessage, text: string) => Promise<boolean>;
  /** Sends the edited text as a new branch. */
  regenerate: (m: ChatMessage, text: string) => void;
  remove: (m: ChatMessage) => void;
  fork: (m: ChatMessage) => void;
  feedback: (m: ChatMessage, value: "up" | "down") => void;
  cycle: (m: ChatMessage, delta: -1 | 1) => void;
  openCode: (m: ChatMessage, args: { kind: ArtifactKind; code: string; lang: string }) => void;
  openBlock: (m: ChatMessage, blockKey: string) => void;
  editImage: (src: string, alt: string) => void;
  imageEditable: (src: string) => boolean;
  extendVideo: (a: ChatAttachment, m: ChatMessage) => void;
  canExtendVideo: (m: ChatMessage) => boolean;
  rememberPreference: (name: string, value: string) => Promise<boolean>;
  loadFullTrace: (m: ChatMessage) => void;
  ask: (text: string) => void;
  prefill: (text: string) => void;
}

export interface SelectionMode {
  active: boolean;
  selected: ReadonlySet<string>;
  toggle: (id: string) => void;
}

export interface MessageEnv {
  conversationKey: string;
  temporary: boolean;
  readOnly: boolean;
  prefs: ChatPrefs;
  currency: Currency;
  billingMode: string;
  busy: boolean;
  locale: string;
  actions: MessageActions;
  selection: SelectionMode;
  /** Whether a model that can extend videos exists / can edit images. */
  hasRememberApi: boolean;
  /** What the `@` menu of the message editor needs (models only; prompts come from the API). */
  mention?: { models: ModelDTO[]; selectedModel: string; selectModel: (name: string) => void };
}

export const MessageEnvContext = createContext<MessageEnv | null>(null);

export function useMessageEnv(): MessageEnv {
  const env = useContext(MessageEnvContext);
  if (!env) throw new Error("MessageEnvContext is missing");
  return env;
}

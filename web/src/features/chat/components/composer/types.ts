import type { UseDraft } from "../../hooks/use-draft";
import type { ModelState } from "../../hooks/use-model-state";
import type { UseSelections } from "../../hooks/use-selections";
import type { Currency } from "../../lib/billing";
import type { QueuedMessage } from "../../lib/queue";
import type { ChatPrefs } from "../../lib/settings";
import type { TaskDecision } from "../../lib/task-decision";
import type { FilePolicyDTO, McpToolDTO, ModelDTO, ModelOptionPolicyDTO, UiComponentDTO } from "../../types";

/** Everything the composer needs from the page, in one object. */
export interface ComposerController {
  /** Conversation key: "draft", a conversation id, or "temp". */
  chatKey: string;
  temporary: boolean;
  /** The conversation's first page is still loading. */
  conversationLoading: boolean;
  draft: UseDraft;
  model: ModelState;
  models: ModelDTO[];
  modelsLoading: boolean;
  policy: ModelOptionPolicyDTO | null;
  selections: UseSelections;
  tools: McpToolDTO[];
  toolsLoaded: boolean;
  uiComponents: UiComponentDTO[];
  uiChoice: { effective: number[]; explicit: boolean; set: (ids: number[]) => void; reset: () => void };
  visualPrompt: [boolean, (on: boolean) => void];
  prefs: ChatPrefs;
  filePolicy: FilePolicyDTO | null;
  kbEnabled: boolean;
  isAdmin: boolean;
  currency: Currency;
  locale: string;
  /** What the current model, attachments and options amount to. */
  decision: TaskDecision;
  /** A reply is running on the visible branch. */
  busy: boolean;
  queued: QueuedMessage[];
  /** Layout: centred on the landing view, docked otherwise. */
  landing: boolean;
  dragging: boolean;
  onSubmit: () => void;
  onStop: () => void;
  onEditQueued: (id: string, text: string) => void;
  onRemoveQueued: (id: string) => void;
  onGuideQueued: (id: string) => void;
  /** Menu placement hint for the model picker. */
  pickerPlacement: "top" | "bottom";
  /** Registered by the composer so the page can focus it or insert text. */
  registerApi: (api: ComposerApi | null) => void;
}

export interface ComposerApi {
  focus: () => void;
  /** Inserts text at the caret. */
  insert: (text: string) => void;
}

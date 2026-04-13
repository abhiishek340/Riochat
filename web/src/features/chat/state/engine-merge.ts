/**
 * Combines what the server returned with the optimistic turns this browser has
 * in flight, so that a reply never shows twice and nothing flickers when the
 * real ids arrive.
 */
import type { ChatMessage } from "../types";
import { inheritUsage, navigatorsFor, reconcileSelection, treeSignature, visiblePath, type Navigator, type Selection } from "../lib/tree";
import type { AttachState, ConvState, Turn } from "./engine-types";

function terminal(status: ChatMessage["status"]): boolean {
  return status !== "pending";
}

/** The server message of a run being re-attached, overlaid with the replayed text. */
function overlayAttach(m: ChatMessage, attach: AttachState | null): ChatMessage {
  if (!attach || m.role !== "assistant" || m.runId !== attach.runId || m.status !== "pending") return m;
  const blocked = attach.blocked;
  return {
    ...m,
    content: blocked ? "" : attach.text ?? m.content,
    contentType: attach.contentType ?? m.contentType,
    usage: {
      input: attach.usage.input || m.usage.input,
      output: attach.usage.output || m.usage.output,
      cacheRead: attach.usage.cacheRead || m.usage.cacheRead,
      cacheWrite: attach.usage.cacheWrite || m.usage.cacheWrite,
      reasoning: attach.usage.reasoning || m.usage.reasoning,
    },
    trace: attach.trace ?? m.trace,
    status: blocked ? "blocked" : "pending",
    live: { streaming: true, label: attach.label, startedAt: Date.parse(m.createdAt) || 0, preview: attach.preview },
  };
}

export function mergeMessages(server: readonly ChatMessage[], turns: readonly Turn[], attach: AttachState | null): ChatMessage[] {
  const list = server.map((m) => overlayAttach(m, attach));
  if (turns.length === 0) return list;
  const indexByKey = new Map<string, number>();
  list.forEach((m, i) => indexByKey.set(m.key, i));
  const rewrite = new Map<string, string>();
  const appended: ChatMessage[] = [];

  const fix = (m: ChatMessage): ChatMessage => {
    const parent = m.parentId ? rewrite.get(m.parentId) : undefined;
    const source = m.sourceId ? rewrite.get(m.sourceId) : undefined;
    return parent || source ? { ...m, parentId: parent ?? m.parentId, sourceId: source ?? m.sourceId } : m;
  };

  for (const turn of [...turns].sort((a, b) => a.order - b.order)) {
    const pieces: (ChatMessage | null)[] = [turn.result ? turn.result.user ?? turn.user : turn.user, turn.result ? turn.result.assistant ?? turn.assistant : turn.assistant];
    for (const piece of pieces) {
      if (!piece) continue;
      const at = indexByKey.get(piece.key);
      if (at === undefined) {
        appended.push(fix(piece));
        continue;
      }
      const serverMsg = list[at] as ChatMessage;
      if (piece.id !== serverMsg.id) rewrite.set(piece.id, serverMsg.id);
      if (piece.role === "assistant" && turn.stage === "running") {
        // Still streaming here: the local text and live flags win over a stale server row.
        list[at] = {
          ...piece,
          id: serverMsg.id,
          numericId: serverMsg.numericId,
          persisted: serverMsg.persisted,
          parentId: serverMsg.parentId,
          sourceId: serverMsg.sourceId,
          createdAt: serverMsg.createdAt,
          optimistic: false,
        };
      } else if (piece.role === "assistant" && !terminal(serverMsg.status) && turn.result?.assistant) {
        // The final result is known but the reload has not caught up yet.
        list[at] = { ...turn.result.assistant };
      }
    }
  }
  return appended.length > 0 ? [...list, ...appended] : list;
}

export interface Derived {
  messages: ChatMessage[];
  path: ChatMessage[];
  navigators: Map<string, Navigator>;
  selection: Selection;
}

export function derive(st: ConvState): Derived {
  const messages = mergeMessages(st.server, st.turns, st.attach);
  const signature = treeSignature(messages);
  if (signature !== st.signature) {
    st.selection = reconcileSelection(messages, st.selection);
    st.signature = signature;
  }
  const path = inheritUsage(visiblePath(messages, st.selection));
  return { messages, path, navigators: navigatorsFor(path, messages), selection: st.selection };
}

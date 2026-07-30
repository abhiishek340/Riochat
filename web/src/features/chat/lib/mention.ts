/**
 * Finding an `@` (resources) or `/` (context) trigger at the caret.
 */

export type TriggerChar = "@" | "/";

export interface Trigger {
  trigger: TriggerChar;
  /** Index of the trigger character. */
  start: number;
  /** Lower-cased text typed after it. */
  query: string;
}

const CJK = /[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef\u3000-\u303f\u3040-\u30ff\uac00-\ud7af]/;
const PUNCT = /[()[\]{}<>"'“”‘’\u300c\u300d\u300e\u300f\u3010\u3011\uff08\uff09\u300a\u300b,.;:!?\uff0c\u3002\uff1b\uff1a\uff01\uff1f\u3001…—–-]/;
const IDENT = /[A-Za-z0-9._-]/;
const URL_PATH = /[A-Za-z0-9._~%:/?#&=+@!$*'()-]/;

/** May the trigger character stand where it does? */
export function triggerAllowed(text: string, index: number): boolean {
  const ch = text[index] as TriggerChar;
  if (index === 0) return true;
  const prev = text[index - 1] as string;
  if (/\s/.test(prev) || CJK.test(prev)) return true;
  // Sentence punctuation and brackets open a trigger, but not the characters that make a URL or path.
  if (PUNCT.test(prev) && !(ch === "/" && URL_PATH.test(prev))) return !(ch === "@" && IDENT.test(prev));
  if (ch === "@") return !IDENT.test(prev);
  return !URL_PATH.test(prev);
}

/** The trigger the caret is inside of, if any. Only for a collapsed selection. */
export function detectTrigger(text: string, selectionStart: number, selectionEnd: number = selectionStart): Trigger | null {
  if (selectionStart !== selectionEnd) return null;
  const caret = selectionStart;
  for (let i = caret - 1; i >= 0; i--) {
    const ch = text[i] as string;
    if (/\s/.test(ch)) return null;
    if (ch === "@" || ch === "/") {
      if (!triggerAllowed(text, i)) return null;
      return { trigger: ch, start: i, query: text.slice(i + 1, caret).toLowerCase() };
    }
  }
  return null;
}

/** Removes the trigger and its query from the text, returning the new text and caret. */
export function removeTrigger(text: string, trigger: Trigger, caret: number): { text: string; caret: number } {
  const next = text.slice(0, trigger.start) + text.slice(caret);
  return { text: next, caret: trigger.start };
}

/** Replaces the trigger text with new content (a prompt preset). */
export function replaceTrigger(text: string, trigger: Trigger, caret: number, insert: string): { text: string; caret: number } {
  const next = text.slice(0, trigger.start) + insert + text.slice(caret);
  return { text: next, caret: trigger.start + insert.length };
}

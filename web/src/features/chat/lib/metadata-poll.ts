/**
 * After a reply, the server may still be generating a title and labels. The
 * client polls on a growing schedule until they show up or time runs out.
 */
import { isPlaceholderTitle } from "./text";

export const FIRST_DELAY_MS = 800;
export const GROWTH = 1.5;
export const MAX_DELAY_MS = 5000;
export const GIVE_UP_MS = 45_000;

export function nextDelay(previous: number | null): number {
  if (previous === null) return FIRST_DELAY_MS;
  return Math.min(MAX_DELAY_MS, Math.round(previous * GROWTH));
}

/** Delays of the whole schedule until the 45 s budget is spent. */
export function schedule(): number[] {
  const out: number[] = [];
  let total = 0;
  let d: number | null = null;
  for (;;) {
    d = nextDelay(d);
    if (total + d > GIVE_UP_MS) break;
    total += d;
    out.push(d);
  }
  return out;
}

export interface PollInput {
  title: string;
  optimisticTitle: string;
  labels: readonly string[];
  autoGenerateLabels: boolean;
  /** The refresh hint from the send result. */
  hint: string | undefined;
}

/** Metadata is still pending: placeholder or fallback title, or no labels while they are wanted. */
export function titlePending(title: string, optimisticTitle: string): boolean {
  if (isPlaceholderTitle(title)) return true;
  return optimisticTitle !== "" && title.trim() === optimisticTitle.trim();
}

export function shouldPoll(input: PollInput): boolean {
  if (input.hint && input.hint !== "pending") return false;
  const needsTitle = titlePending(input.title, input.optimisticTitle);
  const needsLabels = input.labels.length === 0 && input.autoGenerateLabels;
  return needsTitle || needsLabels;
}

/** Did the fetched conversation bring something new? */
export function metadataChanged(
  prev: { title: string; labels: readonly string[] },
  next: { title: string; labels: readonly string[] },
  optimisticTitle: string,
): boolean {
  const titleNew = !titlePending(next.title, optimisticTitle) && next.title.trim() !== prev.title.trim();
  const labelsNew = next.labels.join("\u0000") !== prev.labels.join("\u0000");
  return titleNew || labelsNew;
}

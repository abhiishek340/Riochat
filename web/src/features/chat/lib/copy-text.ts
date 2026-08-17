/**
 * Text that goes to the clipboard when a reply is copied: the markdown as
 * written, except that every interactive block is replaced by a plain-text
 * account of what it currently holds (its defaults overlaid with whatever the
 * reader changed), so the copy is readable outside the app.
 */
import { replaceFences } from "./fences";
import { parseBlock } from "./blocks/block";

type StateReader = (blockKey: string) => Record<string, unknown> | undefined;

function show(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "";
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return "";
  }
}

/** A block as plain text: a heading line, then one `name: value` line per state entry. */
export function blockAsText(title: string, component: string, state: Record<string, unknown>): string {
  const lines = [title || component];
  for (const [name, value] of Object.entries(state)) lines.push(`${name}: ${show(value)}`);
  return lines.join("\n");
}

export function readableMessageText(content: string, readState: StateReader): string {
  if (!content.includes("riochat-ui")) return content;
  return replaceFences(content, (fence) => {
    if (fence.lang !== "riochat-ui" || !fence.closed) return null;
    const parsed = parseBlock(fence.body, false);
    if (parsed.kind !== "ok") return null;
    const { block } = parsed;
    const changed = block.key ? readState(block.key) : undefined;
    return blockAsText(block.title, block.component, { ...(block.state ?? {}), ...(changed ?? {}) });
  });
}

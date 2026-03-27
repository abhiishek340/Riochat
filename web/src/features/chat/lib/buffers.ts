/**
 * Pacing for streamed text. Answer text is flushed to the screen at most every
 * 50 ms; thinking text trickles out 48 characters every 40 ms (256 when more
 * than 1024 are waiting) so long reasoning does not stall the page.
 */

export const TEXT_FLUSH_MS = 50;
export const THINK_FLUSH_MS = 40;
export const THINK_CHUNK = 48;
export const THINK_BACKLOG = 1024;
export const THINK_FAST_CHUNK = 256;

/** Accumulates deltas; `take()` hands over what is waiting. */
export class TextBuffer {
  private pending = "";
  private replacement: string | null = null;

  push(text: string): void {
    if (text) this.pending += text;
  }

  /** An authoritative snapshot discards anything not yet flushed. */
  replace(text: string): void {
    this.pending = "";
    this.replacement = text;
  }

  get hasPending(): boolean {
    return this.pending !== "" || this.replacement !== null;
  }

  take(): { replace: string | null; append: string } {
    const out = { replace: this.replacement, append: this.pending };
    this.replacement = null;
    this.pending = "";
    return out;
  }
}

export interface ThinkChunk {
  /** Key of the thinking entry the text belongs to (round or event id). */
  key: string;
  text: string;
}

/** Thinking deltas, paced per key; a new key flushes the previous one at once. */
export class ThinkBuffer {
  private queue: { key: string; text: string }[] = [];

  push(key: string, text: string): ThinkChunk[] {
    if (!text) return [];
    const flushed: ThinkChunk[] = [];
    const last = this.queue[this.queue.length - 1];
    if (last && last.key !== key) {
      // A different round or event: what is waiting for the earlier one goes out now.
      for (const item of this.queue) flushed.push({ key: item.key, text: item.text });
      this.queue = [];
    }
    const tail = this.queue[this.queue.length - 1];
    if (tail && tail.key === key) tail.text += text;
    else this.queue.push({ key, text });
    return flushed;
  }

  get size(): number {
    return this.queue.reduce((n, q) => n + q.text.length, 0);
  }

  get hasPending(): boolean {
    return this.queue.length > 0;
  }

  /** One timer tick: a slice of the oldest waiting text. */
  tick(): ThinkChunk | null {
    const head = this.queue[0];
    if (!head) return null;
    const size = this.size > THINK_BACKLOG ? THINK_FAST_CHUNK : THINK_CHUNK;
    const text = Array.from(head.text).slice(0, size).join("");
    head.text = head.text.slice(text.length);
    if (head.text === "") this.queue.shift();
    return { key: head.key, text };
  }

  /** Terminal status of the entry (or end of stream): everything out immediately. */
  flushAll(): ThinkChunk[] {
    const out = this.queue.map((q) => ({ key: q.key, text: q.text }));
    this.queue = [];
    return out;
  }

  flushKey(key: string): ThinkChunk[] {
    const out = this.queue.filter((q) => q.key === key).map((q) => ({ key: q.key, text: q.text }));
    this.queue = this.queue.filter((q) => q.key !== key);
    return out;
  }

  clear(): void {
    this.queue = [];
  }
}

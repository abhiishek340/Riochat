/**
 * Re-attaching to a run asks for a snapshot (the whole answer so far) and the
 * server then replays every buffered event after the cursor. The first of those
 * replayed deltas are text the snapshot already holds. This tells them apart:
 * while the replayed text is a prefix of what the snapshot added to the text at
 * the cursor, it is a repeat; the part beyond is new.
 *
 * It only acts when the snapshot did not move the cursor (its sequence number is
 * the one that was asked for), which is when a replay can overlap it; a server
 * that puts the snapshot at the end of the replayed range gets every delta
 * through untouched.
 */
export class ReplayDedupe {
  private expected: string | null = null;
  private got = "";

  /** Call when a snapshot arrives. `textAtCursor` is null when it is not known. */
  begin(snapshotText: string, snapshotSeq: number, requestedAfter: number, textAtCursor: string | null): void {
    this.got = "";
    this.expected = null;
    if (snapshotSeq !== requestedAfter || textAtCursor === null) return;
    if (!snapshotText.startsWith(textAtCursor)) return;
    this.expected = snapshotText.slice(textAtCursor.length);
  }

  /** Stops filtering (a replace event, or anything else that breaks the pattern). */
  stop(): void {
    this.expected = null;
  }

  /** The part of a replayed delta that is new ("" when the snapshot already has it). */
  push(delta: string): string {
    const expected = this.expected;
    if (expected === null) return delta;
    const next = this.got + delta;
    if (expected.startsWith(next)) {
      this.got = next;
      return "";
    }
    this.expected = null;
    if (next.startsWith(expected)) return next.slice(expected.length);
    return delta;
  }
}

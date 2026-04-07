/**
 * Tracks which generations are known to be running, to drive the sidebar's
 * "running" dot and the resume decisions. One registry per browser tab; the
 * server's active-run feed and other tabs feed it.
 *
 *  started_here  this tab began the run
 *  orphaned      this tab's stream ended without a terminal result
 *  confirmed     the server feed (or another tab) reported it
 *  finished      done; kept until an authoritative snapshot forgets it
 */
export type RunState = "started_here" | "orphaned" | "confirmed" | "finished";

export interface RunRecord {
  runId: string;
  conversationId: string;
  state: RunState;
  /** When a snapshot first lacked this started_here run. */
  missingSince: number | null;
}

export interface SnapshotRun {
  runID: string;
  conversationPublicID: string;
}

export const STARTED_HERE_GRACE_MS = 15_000;

export type RunningListener = (conversationId: string, running: boolean) => void;

export class RunRegistry {
  private records = new Map<string, RunRecord>();
  private listeners = new Set<RunningListener>();
  private lastRunning = new Map<string, boolean>();

  subscribe(listener: RunningListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private runningNow(conversationId: string): boolean {
    for (const r of this.records.values()) if (r.conversationId === conversationId && r.state !== "finished") return true;
    return false;
  }

  /** Notifies observers about conversations whose running flag flipped. */
  private settle(touched: Iterable<string>): void {
    for (const id of new Set(touched)) {
      if (!id) continue;
      const now = this.runningNow(id);
      const before = this.lastRunning.get(id) ?? false;
      if (now !== before) {
        if (now) this.lastRunning.set(id, true);
        else this.lastRunning.delete(id);
        for (const l of [...this.listeners]) l(id, now);
      }
    }
  }

  get(runId: string): RunRecord | undefined {
    return this.records.get(runId);
  }

  isRunning(conversationId: string): boolean {
    return this.runningNow(conversationId);
  }

  runningConversations(): string[] {
    const out = new Set<string>();
    for (const r of this.records.values()) if (r.state !== "finished" && r.conversationId) out.add(r.conversationId);
    return [...out];
  }

  runsOf(conversationId: string): RunRecord[] {
    return [...this.records.values()].filter((r) => r.conversationId === conversationId);
  }

  /** This tab starts a run. Ignored for finished runs or runs bound to another conversation. */
  register(runId: string, conversationId: string): void {
    const cur = this.records.get(runId);
    if (cur) {
      if (cur.state === "finished") return;
      if (cur.conversationId && conversationId && cur.conversationId !== conversationId) return;
    }
    const touched = [cur?.conversationId ?? "", conversationId];
    this.records.set(runId, { runId, conversationId: conversationId || cur?.conversationId || "", state: "started_here", missingSince: null });
    this.settle(touched);
  }

  /** A draft chat got its conversation: move the run over. */
  rebind(runId: string, conversationId: string): void {
    const cur = this.records.get(runId);
    if (!cur) {
      this.register(runId, conversationId);
      return;
    }
    const before = cur.conversationId;
    cur.conversationId = conversationId;
    this.settle([before, conversationId]);
  }

  /** The stream ended without a terminal result: the server may still run it. */
  orphan(runId: string): void {
    const cur = this.records.get(runId);
    if (!cur || cur.state === "finished") return;
    cur.state = "orphaned";
    cur.missingSince = null;
    this.settle([cur.conversationId]);
  }

  /** Completed locally. */
  finish(runId: string): void {
    const cur = this.records.get(runId);
    if (!cur) return;
    cur.state = "finished";
    cur.missingSince = null;
    this.settle([cur.conversationId]);
  }

  /** A "started" notice (feed or another tab). */
  noticeStarted(runId: string, conversationId: string | undefined): void {
    const cur = this.records.get(runId);
    if (cur) {
      if (cur.state === "finished") return;
      cur.state = "confirmed";
      cur.missingSince = null;
      if (conversationId && !cur.conversationId) cur.conversationId = conversationId;
      this.settle([cur.conversationId]);
      return;
    }
    if (!conversationId) return;
    this.records.set(runId, { runId, conversationId, state: "confirmed", missingSince: null });
    this.settle([conversationId]);
  }

  /** A "finished" notice. Only the server feed is authoritative. */
  noticeFinished(runId: string, authoritative: boolean): void {
    const cur = this.records.get(runId);
    if (!cur) return;
    if (authoritative) this.records.delete(runId);
    else cur.state = "finished";
    this.settle([cur.conversationId]);
  }

  /** A full feed snapshot. */
  applySnapshot(runs: readonly SnapshotRun[], now: number): void {
    const touched: string[] = [];
    const present = new Map(runs.map((r) => [r.runID, r] as const));
    for (const [id, rec] of [...this.records]) {
      const seen = present.get(id);
      touched.push(rec.conversationId);
      if (!seen) {
        if (rec.state === "finished" || rec.state === "orphaned" || rec.state === "confirmed") {
          this.records.delete(id);
        } else {
          if (rec.missingSince === null) rec.missingSince = now;
          if (now - rec.missingSince >= STARTED_HERE_GRACE_MS) this.records.delete(id);
        }
        continue;
      }
      rec.missingSince = null;
      if (rec.state === "orphaned" || rec.state === "started_here") rec.state = "confirmed";
      if (seen.conversationPublicID && seen.conversationPublicID !== rec.conversationId) {
        touched.push(seen.conversationPublicID);
        rec.conversationId = seen.conversationPublicID;
      }
    }
    for (const r of runs) {
      if (this.records.has(r.runID)) continue;
      this.records.set(r.runID, { runId: r.runID, conversationId: r.conversationPublicID, state: "confirmed", missingSince: null });
      touched.push(r.conversationPublicID);
    }
    this.settle(touched);
  }

  /** Re-evaluates grace periods without a new snapshot (called on a timer). */
  sweep(now: number): void {
    const touched: string[] = [];
    for (const [id, rec] of [...this.records]) {
      if (rec.state === "started_here" && rec.missingSince !== null && now - rec.missingSince >= STARTED_HERE_GRACE_MS) {
        this.records.delete(id);
        touched.push(rec.conversationId);
      }
    }
    this.settle(touched);
  }

  clear(): void {
    const touched = [...this.records.values()].map((r) => r.conversationId);
    this.records.clear();
    this.settle(touched);
  }
}

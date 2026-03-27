import { describe, expect, it } from "vitest";
import type { ProcessTraceDTO, TraceEventDTO } from "../types";
import {
  analyzeToolPayload, applyThinkText, buildRounds, dedupeToolCalls, distinctSourceCount, eventKind, isNoiseStage, mergeTrace, parseStages, parseToolCalls,
  roundTotals, stageCitations, toolCategory, toolStatusOf,
} from "./trace";

function ev(p: Partial<TraceEventDTO> & { eventID: string }): TraceEventDTO {
  return { eventType: "", phase: "", stage: "", ...p };
}

describe("stages", () => {
  it("reads trace_stages from the payload", () => {
    const stages = parseStages({
      title: "p",
      payloadJSON: JSON.stringify({
        trace_stages: [
          { kind: "file_context", groups: { direct_images: [{ file_name: "a.png" }], full_context: ["b.txt"], skipped: [{ file_name: "c.bin" }] } },
          { kind: "content_retrieval", status: "completed", file_count: 2, chunk_count: 7, citations: [{ file_name: "x", file_id: "f1", score: 0.9, preview: "p" }] },
          { kind: "context_compaction", status: "done", from_turn: 1, to_turn: 4, source_tokens: 900, summary_tokens: 120 },
        ],
      }),
    });
    expect(stages.map((x) => x.kind)).toEqual(["file_context", "content_retrieval", "context_compaction"]);
    expect(stages[0]).toMatchObject({ includedCount: 2, skippedCount: 1 });
    expect(stages[1]?.retrieval).toMatchObject({ fileCount: 2, chunkCount: 7 });
    expect(stages[2]?.compaction).toMatchObject({ fromTurn: 1, toTurn: 4, sourceTokens: 900 });
    expect(stageCitations({ process: { payloadJSON: JSON.stringify({ trace_stages: [{ kind: "content_retrieval", citations: [{ file_id: "f1" }, { file_id: "f1" }, { file_id: "f2" }] }] }) } })).toHaveLength(3);
  });

  it("falls back to legacy markdown lines", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    const stages = parseStages({ contentMarkdown: "**Context**: 3 messages\nplain\n**Files**\uff1aok" });
    expect(stages.map((x) => [x.label, x.detail])).toEqual([["Context", "3 messages"], ["Files", "ok"]]);
  });

  it("flags noisy stages", () => {
    expect(isNoiseStage({ kind: "context_planning", status: "", label: "", detail: "Protocol does not support stateful continuation", files: [], includedCount: 0, skippedCount: 0, citations: [] })).toBe(true);
    expect(isNoiseStage({ kind: "other", status: "", label: "Result", detail: "Upstream request failed", files: [], includedCount: 0, skippedCount: 0, citations: [] })).toBe(true);
    expect(isNoiseStage({ kind: "file_context", status: "", label: "", detail: "", files: [], includedCount: 0, skippedCount: 0, citations: [] })).toBe(false);
  });

  it("counts distinct sources", () => {
    expect(distinctSourceCount([{ fileId: "a" }, { fileId: "a" }, { fileId: "", fileName: "n" }, { fileId: "", fileName: "n" }] as never)).toBe(2);
  });
});

describe("tool calls", () => {
  it("analyses a search result for http sources only", () => {
    const a = analyzeToolPayload({ results: [{ title: "A", url: "https://a.example/x", snippet: "s" }, { title: "B", url: "javascript:alert(1)" }, { title: "C", url: "ftp://c" }] });
    expect(a.sources).toEqual([{ title: "A", url: "https://a.example/x", snippet: "s" }]);
  });

  it("parses embedded JSON strings and caps the walk", () => {
    const a = analyzeToolPayload(JSON.stringify({ items: [{ url: "https://e.example" }] }));
    expect(a.sources).toHaveLength(1);
    const many = analyzeToolPayload({ list: Array.from({ length: 600 }, (_, i) => ({ url: `https://x.example/${i}` })) });
    expect(many.sources.length).toBeLessThanOrEqual(64);
  });

  it("finds code results and images", () => {
    const a = analyzeToolPayload({ exit_code: 2, stdout: "hi", image: "data:image/png;base64,AAAA" });
    expect(a).toMatchObject({ exitCode: 2, text: "hi", images: ["data:image/png;base64,AAAA"] });
  });

  it("maps statuses and categories", () => {
    expect(toolStatusOf("in_progress")).toBe("calling");
    expect(toolStatusOf("success")).toBe("completed");
    expect(toolStatusOf("error")).toBe("failed");
    expect(toolStatusOf("reused")).toBe("reused");
    expect(toolCategory("web_search", "", false)).toBe("web_search");
    expect(toolCategory("run_python", "", false)).toBe("code");
    expect(toolCategory("bash", "", false)).toBe("shell");
    expect(toolCategory("lookup", "", true)).toBe("web_search");
    expect(toolCategory("lookup", "", false)).toBe("generic");
  });

  it("keeps the more advanced status when the same call repeats", () => {
    const [a] = parseToolCalls(ev({ eventID: "e1", payloadJSON: JSON.stringify({ tool_calls: [{ id: "t1", name: "search", status: "requested", input: { q: "x" } }] }) }));
    const [b] = parseToolCalls(ev({ eventID: "e2", payloadJSON: JSON.stringify({ tool_calls: [{ id: "t1", name: "search", status: "success", output: { url: "https://z.example" } }] }) }));
    const merged = dedupeToolCalls([a as never, b as never]);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({ status: "completed" });
    expect(merged[0]?.input).toContain("x");
    expect(merged[0]?.sources).toHaveLength(1);
    const failed = dedupeToolCalls([{ ...(b as object), rawStatus: "success" } as never, { ...(b as object), rawStatus: "error", status: "failed" } as never]);
    expect(failed[0]?.status).toBe("failed");
  });
});

describe("rounds", () => {
  const trace: ProcessTraceDTO = {
    events: [
      ev({ eventID: "t2", stage: "tool", seq: 2, roundID: "r1", payloadJSON: JSON.stringify({ tool_calls: [{ id: "c1", name: "search", status: "success" }] }) }),
      ev({ eventID: "k1", stage: "upstream_think", seq: 1, roundID: "r1", contentMarkdown: "think a", startedAt: "2026-01-01T00:00:00Z" }),
      ev({ eventID: "k2", stage: "upstream_think", seq: 3, contentMarkdown: "think b" }),
      ev({ eventID: "t3", stage: "tool", seq: 4, parentEventID: "k2", payloadJSON: JSON.stringify({ tool_calls: [{ id: "c2", name: "code", status: "success" }] }) }),
      ev({ eventID: "rag", stage: "retrieval", seq: 5 }),
    ],
  };
  it("groups by round id or parent event id and ignores other events", () => {
    const rounds = buildRounds(trace);
    expect(rounds.map((r) => r.id)).toEqual(["r1", "k2"]);
    expect(rounds[0]?.tools.map((t) => t.id)).toEqual(["c1"]);
    expect(rounds[1]?.tools.map((t) => t.id)).toEqual(["c2"]);
    expect(rounds[0]?.thinking?.text).toBe("think a");
    expect(roundTotals(rounds)).toEqual({ toolCalls: 2, thinkRounds: 2 });
  });

  it("live thinking enriches a round and keeps the longer text once", () => {
    const rounds = buildRounds(trace, [{ id: "r1", roundId: "r1", title: "", summary: "", text: "think a and more", status: "running", startedAt: "", endedAt: "" }]);
    expect(rounds[0]?.thinking?.text).toBe("think a and more");
  });

  it("classifies events", () => {
    expect(eventKind({ stage: "tools", phase: "", eventType: "" })).toBe("tool");
    expect(eventKind({ stage: "", phase: "", eventType: "reasoning" })).toBe("think");
    expect(eventKind({ stage: "retrieval", phase: "", eventType: "" })).toBe("other");
  });
});

describe("merging", () => {
  it("keeps longer thinking text and the end time when the snapshot lacks them", () => {
    const prev: ProcessTraceDTO = { events: [ev({ eventID: "k", stage: "think", contentMarkdown: "long text", endedAt: "2026-01-01T00:00:09Z", summary: "sum" })] };
    const next: ProcessTraceDTO = { events: [ev({ eventID: "k", stage: "think", contentMarkdown: "long" })] };
    const merged = mergeTrace(prev, next);
    expect(merged?.events?.[0]).toMatchObject({ contentMarkdown: "long text", endedAt: "2026-01-01T00:00:09Z", summary: "sum" });
  });

  it("applies think text per key", () => {
    let entries = applyThinkText([], "r1", "ab", "append");
    entries = applyThinkText(entries, "r1", "cd", "append");
    expect(entries[0]?.text).toBe("abcd");
    entries = applyThinkText(entries, "r1", "x", "replace");
    expect(entries[0]?.text).toBe("abcd");
    entries = applyThinkText(entries, "r1", "abcdef", "replace", { status: "completed" });
    expect(entries[0]).toMatchObject({ text: "abcdef", status: "completed" });
  });
});

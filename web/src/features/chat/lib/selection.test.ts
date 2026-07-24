import { describe, expect, it } from "vitest";
import type { McpToolDTO, UiComponentDTO } from "../types";
import {
  defaultUiComponents, effectiveUiComponents, normalizeTools, parseStoredIds, storableUiChoice, toggleKnowledgeBase, toggleServer, toggleTool, toolCap,
} from "./selection";

const tool = (id: number, over: Partial<McpToolDTO> = {}): McpToolDTO => ({
  id, serverID: 1, serverName: "S", name: `t${id}`, displayName: `T${id}`, description: "", attachmentInputMode: "", priceNanousd: 0, status: "active", ...over,
});
const comp = (id: number, over: Partial<UiComponentDTO> = {}): UiComponentDTO => ({ id, scope: "builtin", name: `c${id}`, version: 1, description: "", rendererKind: "builtin", enabled: true, sortOrder: id, ...over });

describe("tool selection", () => {
  const all = [tool(1), tool(2), tool(3), tool(4), tool(5, { attachmentInputMode: "image" }), tool(6, { attachmentInputMode: "image" })];

  it("derives the cap from policy", () => {
    expect(toolCap(3)).toBe(3);
    expect(toolCap(0)).toBe(32);
    expect(toolCap(-4)).toBe(32);
    expect(toolCap(undefined)).toBe(32);
    expect(toolCap(500)).toBe(128);
  });

  it("refuses a fourth tool at cap 3 and leaves the selection unchanged", () => {
    expect(toggleTool([1, 2, 3], tool(4), all, 3)).toEqual({ ok: false, reason: "limit" });
    expect(toggleTool([1, 2], tool(4), all, 3)).toEqual({ ok: true, ids: [1, 2, 4] });
    expect(toggleTool([1, 2], tool(2), all, 3)).toEqual({ ok: true, ids: [1] });
  });

  it("allows only one image processor", () => {
    expect(toggleTool([5], tool(6, { attachmentInputMode: "image" }), all, 10)).toEqual({ ok: false, reason: "processor" });
    expect(toggleTool([], tool(5, { attachmentInputMode: "image" }), all, 10)).toEqual({ ok: true, ids: [5] });
  });

  it("normalises silently: unavailable ids, duplicates, extra processors, cap", () => {
    expect(normalizeTools([1, 1, 99, 5, 6, 2], all, 10)).toEqual([1, 5, 2]);
    expect(normalizeTools([1, 2, 3, 4], all, 2)).toEqual([1, 2]);
  });

  it("selects and clears a whole server, stopping at the cap", () => {
    const server = [tool(1), tool(2), tool(3)];
    expect(toggleServer([], server, all, 10).ids).toEqual([1, 2, 3]);
    expect(toggleServer([1, 2, 3], server, all, 10).ids).toEqual([]);
    const capped = toggleServer([4], server, all, 2);
    expect(capped).toMatchObject({ ids: [4, 1], hitLimit: true });
  });

  it("caps knowledge bases at eight", () => {
    const eight = Array.from({ length: 8 }, (_, i) => `kb${i}`);
    expect(toggleKnowledgeBase(eight, "kb9")).toEqual({ ok: false });
    expect(toggleKnowledgeBase(eight, "kb0")).toMatchObject({ ok: true });
  });
});

describe("interactive components", () => {
  const list = [comp(1), comp(2), comp(3, { enabled: false }), comp(4, { rendererKind: "sandbox", scope: "user" })];

  it("checks every enabled built-in by default", () => {
    expect(defaultUiComponents(list)).toEqual([1, 2]);
    expect(effectiveUiComponents(list, null)).toEqual([1, 2]);
  });

  it("prunes an explicit choice to visible components", () => {
    expect(effectiveUiComponents(list, [4, 3, 9, 1, 1])).toEqual([4, 1]);
    expect(effectiveUiComponents(list, [])).toEqual([]);
  });

  it("stores a choice equal to the defaults as 'use defaults'", () => {
    expect(storableUiChoice(list, [2, 1])).toBeNull();
    expect(storableUiChoice(list, [1])).toEqual([1]);
    expect(parseStoredIds("[1,2,\"x\"]")).toEqual([1, 2]);
    expect(parseStoredIds(null)).toBeNull();
    expect(parseStoredIds("nope")).toBeNull();
  });
});

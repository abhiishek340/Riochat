import { describe, expect, it } from "vitest";
import type { ControlDTO } from "../types";
import { normalizeControls, pruneChoices, requestControls, resolvePlacements, setPlacement, stepOf, stepValue, validateNumber, type PlaceableItem } from "./controls";

function control(p: Partial<ControlDTO> & { id: string }): ControlDTO {
  return {
    kind: "generic",
    type: "select",
    label: p.id,
    description: "",
    icon: "",
    placement: "toolbar",
    default: null,
    locked: false,
    options: [{ value: "a", label: "A" }],
    min: null,
    max: null,
    step: null,
    integer: false,
    protocols: null,
    ...p,
  };
}

describe("normalizeControls", () => {
  it("drops option-less select controls and duplicates but keeps numeric and toggle ones", () => {
    const list = normalizeControls([
      control({ id: "a" }),
      control({ id: "a" }),
      control({ id: "b", options: [] }),
      control({ id: "c", type: "number", options: null }),
      control({ id: "d", type: "toggle", options: null }),
    ]);
    expect(list.map((c) => c.id)).toEqual(["a", "c", "d"]);
  });
});

describe("number controls", () => {
  const c = control({ id: "temperature", type: "number", min: 0, max: 2, step: 0.5, options: null });
  it("validates range and step", () => {
    expect(validateNumber(c, 0.75)).toBeNull();
    expect(validateNumber(c, 1.5)).toBe(1.5);
    expect(validateNumber(c, 3)).toBeNull();
    expect(validateNumber(c, "x")).toBeNull();
    expect(validateNumber(control({ id: "n", type: "number", integer: true, options: null }), 1.2)).toBeNull();
  });

  it("derives stepper increments", () => {
    expect(stepOf(c)).toBe(0.5);
    expect(stepOf(control({ id: "temperature", type: "number", options: null }))).toBe(0.1);
    expect(stepOf(control({ id: "x", type: "number", integer: true, options: null }))).toBe(1);
    expect(stepOf(control({ id: "x", type: "number", min: 0, max: 2, options: null }))).toBe(0.1);
    expect(stepOf(control({ id: "x", type: "number", min: 0, max: 100, options: null }))).toBe(1);
    expect(stepOf(control({ id: "x", type: "number", default: "0.7", options: null }))).toBe(0.1);
  });

  it("steps without float drift and stops at bounds", () => {
    const t = control({ id: "temperature", type: "number", min: 0, max: 1, options: null });
    expect(stepValue(t, 0.7, 1)).toBe(0.8);
    expect(stepValue(t, 1, 1)).toBeNull();
    expect(stepValue(t, 0, -1)).toBeNull();
  });
});

describe("choices", () => {
  const controls = [
    control({ id: "sel", options: [{ value: "x", label: "X" }, { value: "y", label: "Y" }] }),
    control({ id: "lock", locked: true, default: "x", options: [{ value: "x", label: "X" }] }),
    control({ id: "tog", type: "toggle", options: null }),
  ];
  it("keeps valid unlocked choices only", () => {
    expect(pruneChoices(controls, { sel: "y", lock: "x", tog: "on", other: 1 })).toEqual({ sel: "y", tog: "on" });
    expect(pruneChoices(controls, { sel: "nope", tog: "maybe" })).toEqual({});
    expect(requestControls(controls, { sel: "x" })).toEqual({ sel: "x" });
    expect(requestControls(controls, {})).toBeUndefined();
  });
});

describe("placement", () => {
  const items: PlaceableItem[] = Array.from({ length: 7 }, (_, i) => ({ id: `c${i}`, defaultPlacement: "toolbar" as const }));
  it("puts six on the toolbar and the seventh in the menu", () => {
    const placed = resolvePlacements(items, {});
    expect(items.filter((i) => placed[i.id] === "toolbar")).toHaveLength(6);
    expect(placed.c6).toBe("menu");
  });

  it("refuses to pin beyond six and stores only differences", () => {
    expect(setPlacement(items, {}, "c6", "toolbar")).toEqual({ ok: false, reason: "limit" });
    const moved = setPlacement(items, {}, "c0", "menu");
    expect(moved).toEqual({ ok: true, overrides: { c0: "menu" } });
    if (!moved.ok) throw new Error("unreachable");
    // freeing a slot lets the seventh default item onto the toolbar; pinning it changes nothing stored
    expect(resolvePlacements(items, moved.overrides).c6).toBe("toolbar");
    expect(setPlacement(items, moved.overrides, "c6", "toolbar")).toEqual({ ok: true, overrides: { c0: "menu" } });
    expect(setPlacement(items, moved.overrides, "c1", "toolbar")).toEqual({ ok: true, overrides: { c0: "menu" } });
    // moving back to the default removes the entry
    expect(setPlacement(items, moved.overrides, "c0", "toolbar")).toEqual({ ok: false, reason: "limit" });
    expect(setPlacement(items, {}, "c0", "toolbar")).toEqual({ ok: true, overrides: {} });
  });

  it("trims stale pins from other models first", () => {
    const few: PlaceableItem[] = [
      { id: "a", defaultPlacement: "menu" },
      { id: "b", defaultPlacement: "menu" },
    ];
    const overrides = { s1: "toolbar", s2: "toolbar", s3: "toolbar", s4: "toolbar", s5: "toolbar", s6: "toolbar" } as const;
    const result = setPlacement(few, { ...overrides }, "a", "toolbar");
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(Object.values(result.overrides).filter((v) => v === "toolbar")).toHaveLength(6);
      expect(result.overrides.a).toBe("toolbar");
    }
  });
});

import { describe, expect, it } from "vitest";
import { backoffDelay } from "./use-polling";

describe("backoffDelay", () => {
  it("doubles per failure up to four times the interval", () => {
    expect([0, 1, 2, 3, 9].map((n) => backoffDelay(2000, n))).toEqual([2000, 4000, 8000, 8000, 8000]);
  });
});

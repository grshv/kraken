import { describe, it, expect } from "vitest";
import { PANE_LIMITS, clampPane } from "../../src/shell/layout";

describe("clampPane", () => {
  it("clamps within list range", () => {
    expect(clampPane("list", 300)).toBe(300);
    expect(clampPane("list", 100)).toBe(PANE_LIMITS.list.min);
    expect(clampPane("list", 9999)).toBe(PANE_LIMITS.list.max);
  });

  it("clamps within detail range", () => {
    expect(clampPane("detail", 400)).toBe(400);
    expect(clampPane("detail", 10)).toBe(PANE_LIMITS.detail.min);
    expect(clampPane("detail", 9999)).toBe(PANE_LIMITS.detail.max);
  });

  it("returns min for NaN", () => {
    expect(clampPane("list", NaN)).toBe(PANE_LIMITS.list.min);
    expect(clampPane("detail", NaN)).toBe(PANE_LIMITS.detail.min);
  });

  it("returns min for non-finite values", () => {
    expect(clampPane("list", Infinity)).toBe(PANE_LIMITS.list.max);
    expect(clampPane("list", -Infinity)).toBe(PANE_LIMITS.list.min);
  });

  it("rounds fractional pixels", () => {
    expect(clampPane("list", 250.7)).toBe(251);
    expect(clampPane("detail", 350.4)).toBe(350);
  });
});

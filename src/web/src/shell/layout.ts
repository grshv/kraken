// Pane width limits for the inspector shell, shared by the resizer and tests.

export const PANE_LIMITS = {
  list: { min: 220, max: 440 },
  detail: { min: 280, max: 520 },
} as const;

export type PaneKind = "list" | "detail";

// Clamp a pane width to its allowed range. NaN falls back to the minimum so a
// corrupt localStorage value never collapses a pane; other non-finite values
// clamp naturally (Infinity -> max, -Infinity -> min).
export function clampPane(kind: PaneKind, px: number): number {
  const { min, max } = PANE_LIMITS[kind];
  if (Number.isNaN(px)) return min;
  return Math.min(max, Math.max(min, Math.round(px)));
}

import { describe, expect, it } from "vitest";
import type { Draft, Snapshot } from "../../src/model";
import { applyBlockedReason } from "../../src/draft/applyBlock";

const draft = (foreign?: boolean): Draft => ({
  version: 3,
  snapshot: {} as Snapshot,
  updated_at: "2026-11-01T10:00:00Z",
  ...(foreign === undefined ? {} : { foreign_changes: foreign }),
});

describe("applyBlockedReason", () => {
  it("explains a manager draft with foreign changes", () => {
    expect(applyBlockedReason(draft(true))).toMatch(/вне вашей области/);
  });
  it("does not block an own or admin draft", () => {
    expect(applyBlockedReason(draft(false))).toBeUndefined();
    expect(applyBlockedReason(draft())).toBeUndefined();
    expect(applyBlockedReason(null)).toBeUndefined();
  });
});

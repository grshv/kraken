import { describe, it, expect } from "vitest";
import { pickExpectedRevision } from "../../src/draft/expectedRevision";

describe("pickExpectedRevision", () => {
  it("uses the preview value when present", () => {
    expect(
      pickExpectedRevision(
        { expected_applied_revision: "rev-preview" },
        "rev-status",
      ),
    ).toBe("rev-preview");
  });

  it("falls back to status.applied_revision when preview is null", () => {
    expect(pickExpectedRevision(null, "rev-status")).toBe("rev-status");
  });

  it("falls back to status when preview.expected_applied_revision is null", () => {
    expect(
      pickExpectedRevision({ expected_applied_revision: null }, "rev-status"),
    ).toBe("rev-status");
  });

  it("falls back to status when preview.expected_applied_revision is undefined", () => {
    expect(
      pickExpectedRevision(
        { expected_applied_revision: undefined },
        "rev-status",
      ),
    ).toBe("rev-status");
  });

  it("returns null only when status.applied_revision is null", () => {
    expect(pickExpectedRevision(null, null)).toBeNull();
    expect(
      pickExpectedRevision({ expected_applied_revision: null }, null),
    ).toBeNull();
  });

  it("never returns null when status has a value, even if preview is null", () => {
    expect(
      pickExpectedRevision({ expected_applied_revision: null }, "rev-xyz"),
    ).toBe("rev-xyz");
  });

  it("preview value takes precedence over status", () => {
    expect(
      pickExpectedRevision(
        { expected_applied_revision: "from-preview" },
        "from-status",
      ),
    ).toBe("from-preview");
  });

  it("handles empty-string preview as falsy and falls back to status", () => {
    expect(
      pickExpectedRevision({ expected_applied_revision: "" }, "rev-status"),
    ).toBe("rev-status");
  });
});

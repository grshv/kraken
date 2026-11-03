import { describe, expect, it } from "vitest";
import { navItems } from "../../src/shell/nav";
import { applyReducer, initialApplyState } from "../../src/draft/applyFlow";

describe("navigation by role", () => {
  it("admin sees all sections in agreed order", () => {
    const items = navItems("admin");
    expect(items.filter((i) => i.group === "main").map((i) => i.id)).toEqual([
      "clients",
      "policies",
      "log",
      "audit",
      "accounts",
    ]);
    expect(items.filter((i) => i.group === "bottom").map((i) => i.id)).toEqual([
      "help",
      "settings",
    ]);
    expect(items.find((i) => i.id === "log")?.label).toBe("Журнал");
  });
  it("manager has no settings but keeps accounts and help", () => {
    const ids = navItems("manager").map((i) => i.id);
    expect(ids).not.toContain("settings");
    expect(ids).toContain("accounts");
    expect(ids).toContain("help");
  });
});

describe("apply flow", () => {
  it("walks the happy path", () => {
    let s = initialApplyState;
    expect(s.phase).toBe("idle");
    s = applyReducer(s, { type: "start" });
    expect(s).toMatchObject({ phase: "running", step: "revision" });
    s = applyReducer(s, { type: "step", step: "validate" });
    expect(s).toMatchObject({ phase: "running", step: "validate" });
    s = applyReducer(s, { type: "operation", status: "validated" });
    expect(s).toMatchObject({ phase: "running", step: "apply" });
    s = applyReducer(s, { type: "operation", status: "succeeded" });
    expect(s.phase).toBe("done");
  });
  it("maps failures", () => {
    const running = applyReducer(initialApplyState, { type: "start" });
    expect(
      applyReducer(running, {
        type: "error",
        status: 409,
        code: "revision_conflict",
      }).phase,
    ).toBe("conflict");
    expect(
      applyReducer(running, {
        type: "error",
        status: 403,
        code: "draft_out_of_scope",
      }),
    ).toMatchObject({
      phase: "failed",
      code: "draft_out_of_scope",
      step: "revision",
    });
    expect(
      applyReducer(running, { type: "operation", status: "rolled_back" }).phase,
    ).toBe("rolled_back");
    expect(
      applyReducer(running, { type: "operation", status: "rejected" }),
    ).toMatchObject({ phase: "failed", code: "rejected" });
  });
  it("ignores operation updates when idle", () => {
    expect(
      applyReducer(initialApplyState, {
        type: "operation",
        status: "succeeded",
      }).phase,
    ).toBe("idle");
  });
});

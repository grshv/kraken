import { describe, it, expect } from "vitest";
import {
  applyReducer,
  initialApplyState,
  type ApplyEvent,
} from "../../src/draft/applyFlow";

const r = (s: ReturnType<typeof applyReducer>, e: ApplyEvent) =>
  applyReducer(s, e);

describe("applyReducer", () => {
  it("starts in idle", () => {
    expect(initialApplyState).toEqual({ phase: "idle" });
  });

  it("start moves to running/revision", () => {
    expect(r(initialApplyState, { type: "start" })).toEqual({
      phase: "running",
      step: "revision",
    });
  });

  it("step updates the current step while running", () => {
    const running = r(initialApplyState, { type: "start" });
    expect(r(running, { type: "step", step: "validate" })).toEqual({
      phase: "running",
      step: "validate",
    });
  });

  it("step is ignored outside running", () => {
    expect(r(initialApplyState, { type: "step", step: "validate" })).toEqual({
      phase: "idle",
    });
    const done = r(r(initialApplyState, { type: "start" }), {
      type: "operation",
      status: "succeeded",
    });
    expect(r(done, { type: "step", step: "apply" })).toEqual(done);
  });

  it("error 409 becomes conflict", () => {
    const running = r(initialApplyState, { type: "start" });
    const validating = r(running, { type: "step", step: "validate" });
    expect(
      r(validating, { type: "error", status: 409, code: "conflict" }),
    ).toEqual({ phase: "conflict", step: "validate" });
  });

  it("non-409 error becomes failed with code and current step", () => {
    const running = r(initialApplyState, { type: "start" });
    const applying = r(running, { type: "step", step: "apply" });
    expect(
      r(applying, { type: "error", status: 500, code: "internal" }),
    ).toEqual({ phase: "failed", step: "apply", code: "internal" });
  });

  it("error is ignored outside running", () => {
    expect(
      r(initialApplyState, { type: "error", status: 500, code: "x" }),
    ).toEqual({ phase: "idle" });
  });

  it("operation validated moves to running/apply", () => {
    const running = r(initialApplyState, { type: "start" });
    expect(r(running, { type: "operation", status: "validated" })).toEqual({
      phase: "running",
      step: "apply",
    });
  });

  it("operation succeeded becomes done", () => {
    const running = r(initialApplyState, { type: "start" });
    const applying = r(running, { type: "operation", status: "validated" });
    expect(r(applying, { type: "operation", status: "succeeded" })).toEqual({
      phase: "done",
      step: "apply",
    });
  });

  it("operation rolled_back becomes rolled_back", () => {
    const running = r(initialApplyState, { type: "start" });
    const applying = r(running, { type: "operation", status: "validated" });
    expect(r(applying, { type: "operation", status: "rolled_back" })).toEqual({
      phase: "rolled_back",
      step: "apply",
    });
  });

  it("operation rejected becomes failed with code rejected", () => {
    const running = r(initialApplyState, { type: "start" });
    const applying = r(running, { type: "operation", status: "validated" });
    expect(r(applying, { type: "operation", status: "rejected" })).toEqual({
      phase: "failed",
      step: "apply",
      code: "rejected",
    });
  });

  it("pending and intermediate statuses keep running", () => {
    const running = r(initialApplyState, { type: "start" });
    for (const status of ["pending", "validating", "reloading", "degraded"]) {
      expect(r(running, { type: "operation", status })).toEqual(running);
    }
  });

  it("operation is ignored outside running", () => {
    expect(
      r(initialApplyState, { type: "operation", status: "succeeded" }),
    ).toEqual({ phase: "idle" });
  });

  it("reset returns to idle from any state", () => {
    const failed = {
      phase: "failed" as const,
      step: "apply" as const,
      code: "boom",
    };
    expect(r(failed, { type: "reset" })).toEqual({ phase: "idle" });
    const conflict = { phase: "conflict" as const, step: "validate" as const };
    expect(r(conflict, { type: "reset" })).toEqual({ phase: "idle" });
  });
});

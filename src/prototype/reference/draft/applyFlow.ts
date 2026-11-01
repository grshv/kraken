// Apply flow state machine for the review sheet. Pure reducer: the React layer
// owns HTTP calls and feeds server outcomes as ApplyEvent values. Statuses
// follow the Operation schema (pending/validating/validated/reloading/
// succeeded/rejected/rolled_back/degraded) plus a 409 draft conflict.

export type ApplyStep = "revision" | "validate" | "apply";

export type ApplyState = {
  phase: "idle" | "running" | "done" | "failed" | "conflict" | "rolled_back";
  step?: ApplyStep;
  code?: string;
};

export const initialApplyState: ApplyState = { phase: "idle" };

export type ApplyEvent =
  | { type: "start" }
  | { type: "step"; step: ApplyStep }
  | { type: "error"; status: number; code: string }
  | { type: "operation"; status: string }
  | { type: "reset" };

// Rules from the task:
// - start -> running/revision
// - step -> running/<step>
// - error 409 -> conflict
// - other error -> failed with code and current step
// - operation validated -> running/apply
// - operation succeeded -> done
// - operation rolled_back -> rolled_back
// - operation rejected -> failed code "rejected"
// - reset -> idle
// - operation/step/error outside running: state unchanged
export function applyReducer(s: ApplyState, e: ApplyEvent): ApplyState {
  switch (e.type) {
    case "start":
      return { phase: "running", step: "revision" };
    case "step":
      if (s.phase !== "running") return s;
      return { phase: "running", step: e.step };
    case "error":
      if (s.phase !== "running") return s;
      if (e.status === 409) return { phase: "conflict", step: s.step };
      return { phase: "failed", step: s.step, code: e.code };
    case "operation":
      if (s.phase !== "running") return s;
      if (e.status === "validated") return { phase: "running", step: "apply" };
      if (e.status === "succeeded")
        // Preserve the apply step for the done badge.
        return { phase: "done", step: s.step ?? "apply" };
      if (e.status === "rolled_back")
        return { phase: "rolled_back", step: s.step };
      if (e.status === "rejected")
        return { phase: "failed", step: s.step, code: "rejected" };
      // pending/validating/reloading/degraded and any unknown status leave the
      // current running step in place; degraded is terminal server-side and
      // arrives via a separate path, but if it shows up here we stay running
      // until the recovery poll resolves it.
      return s;
    case "reset":
      return { phase: "idle" };
  }
}

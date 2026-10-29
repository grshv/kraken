import { useCallback, useEffect, useRef, useState } from "react";
import type { Request } from "../api";
import type { Draft, Operation, Revision, Status } from "../model";
import { useOperationRecovery } from "../useOperationRecovery";
import {
  applyReducer,
  initialApplyState,
  type ApplyEvent,
  type ApplyState,
} from "./applyFlow";
import { pickExpectedRevision } from "./expectedRevision";

// Coordinates the revision -> validate -> apply flow against the real API.
// The reducer owns the visible state machine; this hook owns HTTP, polling,
// idempotency keys, the optional preview fetch (admin only) and transport
// recovery via useOperationRecovery.
export type ApplyRun = {
  state: ApplyState;
  operation: Operation | null;
  error: string;
  run: () => Promise<void>;
  reset: () => void;
};

export function useApplyFlow(
  request: Request,
  draft: Draft | null,
  role: "admin" | "manager",
  enabled: boolean,
  onApplied: () => Promise<void>,
): ApplyRun {
  const [state, dispatch] = useState<ApplyState>(initialApplyState);
  const [operation, setOperation] = useState<Operation | null>(null);
  const [error, setError] = useState("");
  const inflight = useRef(false);
  const reqRef = useRef(request);
  const roleRef = useRef(role);
  const draftRef = useRef(draft);
  const appliedRef = useRef(onApplied);
  reqRef.current = request;
  roleRef.current = role;
  draftRef.current = draft;
  appliedRef.current = onApplied;

  const emit = useCallback(
    (e: ApplyEvent) => dispatch((p) => applyReducer(p, e)),
    [],
  );

  const reset = useCallback(() => {
    emit({ type: "reset" });
    setOperation(null);
    setError("");
  }, [emit]);

  // After a transport failure mid-poll, resume GET /operations/{id} until
  // terminal. Never resubmit a mutation; the Idempotency-Key already persists
  // the intent server-side.
  useOperationRecovery(
    reqRef.current,
    operation,
    inflight.current,
    enabled,
    async (next) => {
      setOperation(next);
      emit({ type: "operation", status: next.status });
      if (next.status === "succeeded") {
        try {
          await appliedRef.current();
        } catch {
          // surfaced by the shell state poll
        }
        setOperation(null);
      } else if (next.status === "degraded") {
        emit({
          type: "error",
          status: 0,
          code: next.findings.join(" ") || "degraded",
        });
      }
    },
  );

  const run = useCallback(async () => {
    if (inflight.current) return;
    const d = draftRef.current;
    if (!d) return;
    inflight.current = true;
    setError("");
    try {
      await runApply(reqRef.current, d, roleRef.current, emit, setOperation);
      // Read the final state synchronously to trigger onApplied on success.
      let succeeded = false;
      dispatch((final) => {
        if (final.phase === "done") succeeded = true;
        return final;
      });
      if (succeeded) {
        try {
          await appliedRef.current();
        } catch {
          // surfaced by the shell state poll
        }
        setOperation(null);
      }
    } catch (e) {
      const status = (e as { status?: number }).status;
      const code = (e as { code?: string }).code;
      if (status === 409) {
        emit({ type: "error", status: 409, code: code || "conflict" });
      } else if (code) {
        emit({ type: "error", status: status ?? 0, code });
      } else {
        setError((e as Error).message);
      }
    } finally {
      inflight.current = false;
    }
  }, [emit]);

  return { state, operation, error, run, reset };
}

async function runApply(
  request: Request,
  draft: Draft,
  role: "admin" | "manager",
  emit: (e: ApplyEvent) => void,
  setOperation: (op: Operation | null) => void,
) {
  emit({ type: "reset" });
  emit({ type: "start" });
  emit({ type: "step", step: "revision" });

  const revision = await request<Revision>(
    "/revisions",
    "POST",
    { expected_draft_version: draft.version },
    idempotencyKey(),
  );

  // expected_applied_revision: admin reads it from /revisions/{id}/preview;
  // manager cannot (403 admin_required). Fallback for both roles: a fresh
  // GET /status carries applied_revision, which is exactly the value the
  // server compares against during validate/apply. A null result is only
  // valid when there is no applied revision yet (first-ever apply).
  let previewResult: { expected_applied_revision: string | null } | null = null;
  if (role === "admin") {
    try {
      previewResult = await request<{
        expected_applied_revision: string | null;
      }>(`/revisions/${revision.id}/preview`);
    } catch {
      // preview unreachable; fall back to status below
    }
  }
  const currentStatus = await request<Status>("/status");
  const expected = pickExpectedRevision(
    previewResult,
    currentStatus.applied_revision,
  );

  emit({ type: "step", step: "validate" });
  let op = await request<Operation>(
    "/operations",
    "POST",
    {
      action: "validate",
      revision_id: revision.id,
      expected_applied_revision: expected,
    },
    idempotencyKey(),
  );
  setOperation(op);
  op = await pollOperation(request, op.id);
  emit({ type: "operation", status: op.status });
  if (op.status !== "validated") {
    if (op.status === "degraded") {
      emit({
        type: "error",
        status: 0,
        code: op.findings.join(" ") || "degraded",
      });
    }
    return;
  }

  emit({ type: "step", step: "apply" });
  op = await request<Operation>(
    "/operations",
    "POST",
    {
      action: "apply",
      revision_id: revision.id,
      validation_operation_id: op.id,
      expected_applied_revision: expected,
    },
    idempotencyKey(),
  );
  setOperation(op);
  op = await pollOperation(request, op.id);
  emit({ type: "operation", status: op.status });
  if (op.status === "degraded") {
    emit({
      type: "error",
      status: 0,
      code: op.findings.join(" ") || "degraded",
    });
  }
}

// Poll GET /operations/{id} with backoff until a terminal status. Terminal
// statuses: succeeded, rejected, rolled_back, degraded.
async function pollOperation(request: Request, id: string): Promise<Operation> {
  for (let i = 0; i < 360; i++) {
    const op = await request<Operation>(`/operations/${id}`, "GET");
    if (!["pending", "validating", "reloading"].includes(op.status)) {
      return op;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("Операция выполняется слишком долго");
}

function idempotencyKey(): string {
  // 16+ chars per OpenAPI requirement; crypto.randomUUID when available.
  try {
    if (typeof crypto !== "undefined" && crypto.randomUUID) {
      return crypto.randomUUID();
    }
  } catch {
    // fall through
  }
  return "k-" + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

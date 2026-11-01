import { useEffect, useRef } from "react";
import type { Request } from "./api";
import type { Operation } from "./model";
export const operationPending = (operation: Operation | null) =>
  !!operation &&
  ["pending", "validating", "reloading"].includes(operation.status);
// Resume GET polling after a transport failure. Never resubmit a mutation.
export function useOperationRecovery(
  request: Request,
  operation: Operation | null,
  busy: boolean,
  enabled: boolean,
  onResult: (operation: Operation) => Promise<void>,
) {
  const api = useRef(request),
    result = useRef(onResult);
  api.current = request;
  result.current = onResult;
  useEffect(() => {
    if (!enabled || busy || !operationPending(operation)) return;
    const controller = new AbortController();
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const next = await api.current<Operation>(
          `/operations/${operation!.id}`,
          "GET",
          undefined,
          undefined,
          controller.signal,
        );
        if (!active) return;
        await result.current(next);
        if (!operationPending(next)) return;
      } catch {
        if (!active) return;
      }
      if (active) timer = setTimeout(poll, 2000);
    }
    void poll();
    return () => {
      active = false;
      controller.abort();
      clearTimeout(timer);
    };
  }, [operation?.id, operation?.status, busy, enabled]);
}

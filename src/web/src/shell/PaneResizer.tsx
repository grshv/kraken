import { useCallback, useEffect, useRef } from "react";
import type { PaneKind } from "./layout";
import { PANE_LIMITS } from "./layout";

// Vertical splitter between the list and inspector panes. Drag persists to
// localStorage via onCommit; arrow keys nudge by 16px; double-click resets.
// Direction is "list" (width grows to the right) or "detail" (grows to the
// left, since the inspector detail pane sits on the right edge of the log).
export function PaneResizer({
  kind,
  direction = "right",
  onDrag,
  onCommit,
  onReset,
}: {
  kind: PaneKind;
  direction?: "right" | "left";
  onDrag: (deltaFromStart: number, startWidth: number) => void;
  onCommit: (finalWidth: number) => void;
  onReset: () => void;
}) {
  const start = useRef<{ x: number; w: number } | null>(null);
  const onDragRef = useRef(onDrag);
  const onCommitRef = useRef(onCommit);
  onDragRef.current = onDrag;
  onCommitRef.current = onCommit;

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const handle = e.currentTarget as HTMLDivElement;
      const pane = handle.parentElement;
      if (!pane) return;
      const rect = pane.getBoundingClientRect();
      const startWidth = Math.round(rect.width);
      start.current = { x: e.clientX, w: startWidth };
      handle.classList.add("drag");
      document.body.classList.add("resizing");

      const move = (ev: MouseEvent) => {
        if (!start.current) return;
        const raw = ev.clientX - start.current.x;
        const delta = direction === "right" ? raw : -raw;
        onDragRef.current(delta, start.current.w);
      };
      const up = () => {
        document.removeEventListener("mousemove", move);
        document.removeEventListener("mouseup", up);
        handle.classList.remove("drag");
        document.body.classList.remove("resizing");
        if (start.current) {
          const rawDelta =
            (direction === "right" ? 1 : -1) * (start.current.w === 0 ? 0 : 0);
          void rawDelta;
          // commit the latest width from the parent rect
          const finalRect = pane.getBoundingClientRect();
          onCommitRef.current(Math.round(finalRect.width));
        }
        start.current = null;
      };
      document.addEventListener("mousemove", move);
      document.addEventListener("mouseup", up);
    },
    [direction],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const P = PANE_LIMITS[kind];
      const pane = (e.currentTarget as HTMLDivElement).parentElement;
      if (!pane) return;
      const cur = Math.round(pane.getBoundingClientRect().width);
      const step = 16;
      const sign =
        (e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0) *
        (direction === "right" ? 1 : -1);
      if (!sign) return;
      e.preventDefault();
      onCommitRef.current(Math.min(P.max, Math.max(P.min, cur + sign * step)));
    },
    [kind, direction],
  );

  // Cleanup is defensive: handlers remove themselves on mouseup. Keeping the
  // effect empty means we never leak a global listener after unmount.
  useEffect(() => {
    return () => {
      document.body.classList.remove("resizing");
    };
  }, []);

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={kind === "list" ? "Ширина списка" : "Ширина панели"}
      tabIndex={0}
      className="pane-resizer"
      onMouseDown={onMouseDown}
      onKeyDown={onKeyDown}
      onDoubleClick={onReset}
    />
  );
}

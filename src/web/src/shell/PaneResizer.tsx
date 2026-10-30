import { useCallback, useEffect, useRef } from "react";
import type { PaneKind } from "./layout";
import { PANE_LIMITS } from "./layout";

// paneOf finds the element whose width the handle controls: the list pane
// next to the handle in the section frame, or the detail pane that contains it.
function paneOf(handle: HTMLElement, kind: PaneKind): HTMLElement | null {
  if (kind === "detail") return handle.parentElement;
  return (
    handle.parentElement?.querySelector<HTMLElement>("[data-slot=list-pane]") ??
    null
  );
}

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
      const pane = paneOf(handle, kind);
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
          // commit the width the pane ended up with
          const finalRect = pane.getBoundingClientRect();
          onCommitRef.current(Math.round(finalRect.width));
        }
        start.current = null;
      };
      document.addEventListener("mousemove", move);
      document.addEventListener("mouseup", up);
    },
    [direction, kind],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const P = PANE_LIMITS[kind];
      const pane = paneOf(e.currentTarget as HTMLDivElement, kind);
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

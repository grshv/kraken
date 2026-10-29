import { useCallback, useEffect, useRef, useState } from "react";
import { clampPane, type PaneKind } from "./layout";

const storageKey = (kind: PaneKind) => `kraken-pane-${kind}`;

// Persisted pane width with try/catch around localStorage so private mode,
// quota errors and disabled storage never break the resizer. Defaults sit in
// the middle of each range, matching the prototype.
function defaultWidth(kind: PaneKind): number {
  const { min, max } = {
    list: { min: 220, max: 440 },
    detail: { min: 280, max: 520 },
  }[kind];
  return Math.round((min + max) / 2);
}

function readWidth(kind: PaneKind): number {
  try {
    const raw = localStorage.getItem(storageKey(kind));
    if (raw === null) return defaultWidth(kind);
    return clampPane(kind, Number(raw));
  } catch {
    return defaultWidth(kind);
  }
}

function writeWidth(kind: PaneKind, px: number) {
  try {
    localStorage.setItem(storageKey(kind), String(px));
  } catch {
    // ignore: storage is best-effort, the in-memory width still works
  }
}

export function usePaneWidth(kind: PaneKind) {
  const [width, setWidth] = useState(() => readWidth(kind));
  // useRef mirror so the drag handler reads the latest without re-binding.
  const widthRef = useRef(width);
  widthRef.current = width;
  useEffect(() => {
    widthRef.current = width;
  }, [width]);
  const onDrag = useCallback(
    (deltaFromStart: number, startWidth: number) => {
      const next = clampPane(kind, startWidth + deltaFromStart);
      setWidth(next);
      widthRef.current = next;
    },
    [kind],
  );
  const onCommit = useCallback(
    (finalWidth: number) => {
      const next = clampPane(kind, finalWidth);
      setWidth(next);
      writeWidth(kind, next);
    },
    [kind],
  );
  const reset = useCallback(() => {
    const d = defaultWidth(kind);
    setWidth(d);
    writeWidth(kind, d);
  }, [kind]);
  return { width, widthRef, onDrag, onCommit, reset };
}

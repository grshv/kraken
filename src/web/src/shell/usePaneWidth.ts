import { useCallback, useEffect, useRef, useState } from "react";
import { clampPane, type PaneKind } from "./layout";

// v2: widths saved before the resizer measured the right pane are dropped.
const storageKey = (kind: PaneKind) => `kraken-pane-v2-${kind}`;

// Persisted pane width with try/catch around localStorage so private mode,
// quota errors and disabled storage never break the resizer. Defaults are
// the prototype's values.
function defaultWidth(kind: PaneKind): number {
  return { list: 300, detail: 340 }[kind];
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

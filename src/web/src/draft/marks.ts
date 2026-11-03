// Pure draft-vs-active marking. No DOM, no fetch.
//
// entityMarks returns the per-id change kind for objects present in the draft
// or active snapshot. Unchanged objects are omitted. Comparison is deep and
// object-key-order independent; array order is significant (destinations and
// source networks are ordered lists).

export type Mark = "new" | "changed" | "deleted";

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a === null || b === null || typeof a !== "object" || typeof b !== "object") {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(a[i], b[i])) return false;
    }
    return true;
  }
  const ka = Object.keys(a as Record<string, unknown>);
  const kb = Object.keys(b as Record<string, unknown>);
  if (ka.length !== kb.length) return false;
  for (const k of ka) {
    if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
    if (
      !deepEqual(
        (a as Record<string, unknown>)[k],
        (b as Record<string, unknown>)[k],
      )
    ) {
      return false;
    }
  }
  return true;
}

export function entityMarks<T extends { id: string }>(
  active: T[],
  draft: T[],
): Record<string, Mark> {
  const activeMap = new Map<string, T>();
  for (const x of active) activeMap.set(x.id, x);
  const draftMap = new Map<string, T>();
  for (const x of draft) draftMap.set(x.id, x);
  const out: Record<string, Mark> = {};
  for (const [id, d] of draftMap) {
    if (!activeMap.has(id)) {
      out[id] = "new";
      continue;
    }
    if (!deepEqual(d, activeMap.get(id))) out[id] = "changed";
  }
  for (const id of activeMap.keys()) {
    if (!draftMap.has(id)) out[id] = "deleted";
  }
  return out;
}

import type { Snapshot } from "../model";
import { deepEqual, entityMarks } from "./marks";

// draftChangeCount counts changed objects of the draft against the active
// snapshot, the way the review sheet lists them: each client, each policy,
// Squid settings once and the configuration mode with the expert section
// once. A manager receives both snapshots filtered to his scope, so foreign
// changes are not counted. Null when either snapshot is unknown.
export function draftChangeCount(
  active: Snapshot | null | undefined,
  draft: Snapshot | null | undefined,
): number | null {
  if (!active || !draft) return null;
  let n =
    Object.keys(entityMarks(active.clients, draft.clients)).length +
    Object.keys(entityMarks(active.policies, draft.policies)).length;
  if (!deepEqual(active.settings ?? {}, draft.settings ?? {})) n++;
  if (
    (active.configuration_mode ?? "structured") !==
      (draft.configuration_mode ?? "structured") ||
    (active.expert_config ?? "") !== (draft.expert_config ?? "")
  )
    n++;
  return n;
}

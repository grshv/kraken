import type { Client, Snapshot } from "../model";
import { clientAllows } from "../draft/edits";

export type HostCount = { host: string; requests: number; denied: number };

// missingDomains: denied destinations the client's access does not allow —
// the ones "Разрешить" can fix by adding them to the client's own domains.
// Denials of allowed hosts come from built-in protection or the global
// blocklist and are not offered; an all-web client has nothing to add.
export function missingDomains(
  top: HostCount[] | undefined,
  snap: Snapshot | null,
  client: Client | null,
): HostCount[] {
  if (!snap || !client || client.access === "all_web") return [];
  return (top ?? []).filter(
    (d) => d.denied > 0 && !clientAllows(snap, client, d.host),
  );
}

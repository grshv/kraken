import type { ApiRequest, Client, Snapshot } from "../model";
import type { Notice } from "../clients/notifications";
import { clientAllows } from "../draft/edits";

export async function loadPolicySuggestions(
  request: ApiRequest,
  snap: Snapshot,
  clients: Client[],
) {
  const result = await request<{ items: Notice[] }>(
    "/notifications?status=all&kind=denied_domain",
  );
  if (!Array.isArray(result.items))
    throw Error("Не удалось прочитать уведомления");
  return policySuggestions(result.items, snap, clients);
}

// The source must be /notifications?kind=denied_domain: generic traffic totals
// cannot distinguish a missing domain from a protected destination or port.
export function policySuggestions(
  notices: Notice[],
  snap: Snapshot,
  clients: Client[],
) {
  if (snap.configuration_mode === "expert") return [];
  const eligible = new Map(
    clients
      .filter((c) => c.enabled && c.access === "list")
      .map((c) => [c.id, c]),
  );
  return notices
    .flatMap((n) => {
      const c = n.client_id ? eligible.get(n.client_id) : undefined;
      if (
        !c ||
        n.kind !== "denied_domain" ||
        n.status === "ignored" ||
        !n.host ||
        n.denied <= 0
      )
        return [];
      const host = n.host.toLowerCase().replace(/\.$/, "");
      const blocked = snap.settings?.blocked_domains?.some(
        (d) =>
          host === d.domain ||
          (d.include_subdomains && host.endsWith("." + d.domain)),
      );
      if (blocked || clientAllows(snap, c, host)) return [];
      return [{ host, denied: n.denied, client: c.name, clientID: c.id }];
    })
    .sort((a, b) => b.denied - a.denied);
}

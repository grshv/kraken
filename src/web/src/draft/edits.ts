// Pure draft-edit helpers. Each returns a new snapshot; inputs are not mutated.
// Used by the Clients/Policies screens to compute the next draft before PUT,
// and by vitest to test the edit logic without React.
//
// A client reaches the union of its own domains and the attached policies
// (shared domain sets) when access is "list"; "all_web" opens the whole web
// (protections still apply) and "none" closes everything. See
// policy.EffectiveDestinations in Go.

import type { Access, Client, Destination, Policy, Snapshot } from "../model";
import type { DomainEntry } from "../policies/domains";
import { mergeDestinations, parseDomainInput } from "../policies/domains";
import { parseNetwork } from "../clients/networks";

function withPolicy(draft: Snapshot, id: string, next: Policy): Snapshot {
  return {
    ...draft,
    policies: draft.policies.map((p) => (p.id === id ? next : p)),
  };
}

function withClient(
  draft: Snapshot,
  id: string,
  fn: (c: Client) => Client,
): Snapshot {
  if (!draft.clients.some((c) => c.id === id)) return draft;
  return {
    ...draft,
    clients: draft.clients.map((c) => (c.id === id ? fn(c) : c)),
  };
}

function parseAll(inputs: string[]): DomainEntry[] {
  return inputs
    .map((s) => parseDomainInput(s))
    .filter((d): d is DomainEntry => d !== null);
}

// newEntityID returns the smallest "<prefix><n>" not already present.
export function newEntityID(draft: Snapshot, prefix: string): string {
  const ids = new Set<string>([
    ...draft.clients.map((c) => c.id),
    ...draft.policies.map((p) => p.id),
    ...draft.tls_profiles.map((p) => p.id),
  ]);
  let n = 1;
  while (ids.has(prefix + n)) n++;
  return prefix + n;
}

// EffectiveEntry is one line of a client's final list with where it comes
// from: "own" or the ids of the policies that list it.
export type EffectiveEntry = {
  destination: Destination;
  own: boolean;
  policyIDs: string[];
};

// effectiveDestinations mirrors Go policy.EffectiveDestinations: the union of
// own domains and attached policies, without entries covered by a "*.parent"
// entry, sorted by domain; each line keeps its sources.
export function effectiveDestinations(
  snap: Snapshot,
  client: Client,
): EffectiveEntry[] {
  const all: { d: Destination; from: string }[] = client.destinations.map(
    (d) => ({ d, from: "" }),
  );
  for (const id of client.policy_ids) {
    const p = snap.policies.find((x) => x.id === id);
    for (const d of p?.destinations ?? []) all.push({ d, from: id });
  }
  const wild = new Set(
    all.filter((x) => x.d.include_subdomains).map((x) => x.d.domain),
  );
  const covered = (d: Destination) =>
    [...wild].some(
      (parent) => d.domain !== parent && d.domain.endsWith("." + parent),
    ) ||
    (!d.include_subdomains && wild.has(d.domain));
  const byKey = new Map<string, EffectiveEntry>();
  for (const { d, from } of all) {
    if (covered(d)) continue;
    const key = `${d.domain}|${d.include_subdomains}`;
    const e = byKey.get(key) ?? { destination: d, own: false, policyIDs: [] };
    if (from === "") e.own = true;
    else if (!e.policyIDs.includes(from)) e.policyIDs.push(from);
    byKey.set(key, e);
  }
  return [...byKey.values()].sort(
    (a, b) =>
      a.destination.domain.localeCompare(b.destination.domain) ||
      Number(a.destination.include_subdomains) -
        Number(b.destination.include_subdomains),
  );
}

function matches(host: string, d: Destination): boolean {
  const h = host.toLowerCase().replace(/\.$/, "");
  return h === d.domain || (d.include_subdomains && h.endsWith("." + d.domain));
}

// clientAllows reports whether the client's access (not protections or
// blocked-for-all) allows host.
export function clientAllows(
  snap: Snapshot,
  client: Client,
  host: string,
): boolean {
  if (client.access === "all_web") return true;
  if (client.access !== "list") return false;
  return effectiveDestinations(snap, client).some((e) =>
    matches(host, e.destination),
  );
}

// addDomainsToClient merges domain inputs into the client's own list; a client
// with access "none" switches to "list". No-op for "all_web", a missing client
// or when no input parses.
export function addDomainsToClient(
  draft: Snapshot,
  clientID: string,
  inputs: string[],
): Snapshot {
  const client = draft.clients.find((c) => c.id === clientID);
  if (!client || client.access === "all_web") return draft;
  const parsed = parseAll(inputs);
  if (!parsed.length) return draft;
  return withClient(draft, clientID, (c) => ({
    ...c,
    access: "list",
    destinations: mergeDestinations(c.destinations, parsed),
  }));
}

// addDomainToClient is addDomainsToClient for one input.
export function addDomainToClient(
  draft: Snapshot,
  clientID: string,
  input: string,
): Snapshot {
  return addDomainsToClient(draft, clientID, [input]);
}

// removeClientDomain drops one of the client's own domains.
export function removeClientDomain(
  draft: Snapshot,
  clientID: string,
  domain: string,
): Snapshot {
  return withClient(draft, clientID, (c) => ({
    ...c,
    destinations: c.destinations.filter((d) => d.domain !== domain),
  }));
}

// toggleClientDomainSubdomains flips include_subdomains for one own domain.
export function toggleClientDomainSubdomains(
  draft: Snapshot,
  clientID: string,
  domain: string,
): Snapshot {
  return withClient(draft, clientID, (c) => ({
    ...c,
    destinations: c.destinations.map((d) =>
      d.domain === domain
        ? { ...d, include_subdomains: !d.include_subdomains }
        : d,
    ),
  }));
}

// setClientAccess sets the access mode; lists are kept so switching back
// restores them.
export function setClientAccess(
  draft: Snapshot,
  clientID: string,
  access: Access,
): Snapshot {
  return withClient(draft, clientID, (c) => ({ ...c, access }));
}

// setClientTransport picks the TLS profile (HTTP or TLS listener).
export function setClientTransport(
  draft: Snapshot,
  clientID: string,
  tlsProfileID: string,
): Snapshot {
  if (!draft.tls_profiles.some((p) => p.id === tlsProfileID)) return draft;
  return withClient(draft, clientID, (c) => ({
    ...c,
    tls_profile_id: tlsProfileID,
  }));
}

// clientTransport is the listener the client uses: "tls" when its TLS
// profile has proxy_transport tls, otherwise "http".
export function clientTransport(
  snap: Snapshot,
  client: Client,
): "http" | "tls" {
  const p = snap.tls_profiles.find((x) => x.id === client.tls_profile_id);
  return p?.proxy_transport === "tls" ? "tls" : "http";
}

// withTransportProfile returns the snapshot with a profile of the transport
// and its id: the first existing one, or a new tunnel profile (a TLS one uses
// the installed proxy-server certificate; the server rejects it without one).
export function withTransportProfile(
  draft: Snapshot,
  transport: "http" | "tls",
): { snapshot: Snapshot; id: string } {
  const found = draft.tls_profiles.find((p) => p.proxy_transport === transport);
  if (found) return { snapshot: draft, id: found.id };
  const id = newEntityID(draft, "t-");
  const profile = {
    id,
    name: transport === "tls" ? "TLS proxy / CONNECT" : "HTTP proxy / CONNECT",
    mode: "tunnel",
    proxy_transport: transport,
    cert_ref: transport === "tls" ? "proxy-server" : null,
  };
  return {
    snapshot: { ...draft, tls_profiles: [...draft.tls_profiles, profile] },
    id,
  };
}

// setClientConnection sets how the client connects: HTTP without a login, or
// TLS with a proxy login (a login requires the TLS listener).
export function setClientConnection(
  draft: Snapshot,
  clientID: string,
  proxyUsername: string | undefined,
): Snapshot {
  if (!draft.clients.some((c) => c.id === clientID)) return draft;
  const { snapshot, id } = withTransportProfile(
    draft,
    proxyUsername ? "tls" : "http",
  );
  return withClient(snapshot, clientID, (c) => ({
    ...c,
    tls_profile_id: id,
    proxy_username: proxyUsername || undefined,
  }));
}

// attachPolicy adds a shared policy to the client (once); a client with access
// "none" switches to "list". detachPolicy removes it.
export function attachPolicy(
  draft: Snapshot,
  clientID: string,
  policyID: string,
): Snapshot {
  if (!draft.policies.some((p) => p.id === policyID)) return draft;
  return withClient(draft, clientID, (c) =>
    c.policy_ids.includes(policyID)
      ? c
      : {
          ...c,
          access: c.access === "none" ? "list" : c.access,
          policy_ids: [...c.policy_ids, policyID],
        },
  );
}

export function detachPolicy(
  draft: Snapshot,
  clientID: string,
  policyID: string,
): Snapshot {
  return withClient(draft, clientID, (c) => ({
    ...c,
    policy_ids: c.policy_ids.filter((id) => id !== policyID),
  }));
}

// shareClientDomains moves the client's own domains into a new shared policy
// attached to the client, so other clients can attach the same set. Returns
// the snapshot and the new policy id; no-op without own domains or a name.
export function shareClientDomains(
  draft: Snapshot,
  clientID: string,
  name: string,
): { snapshot: Snapshot; policyID: string } | null {
  const client = draft.clients.find((c) => c.id === clientID);
  if (!client || !client.destinations.length || !name.trim()) return null;
  const policyID = newEntityID(draft, "p-");
  const policy: Policy = {
    id: policyID,
    name: name.trim(),
    destinations: client.destinations,
  };
  const next = withClient(
    { ...draft, policies: [...draft.policies, policy] },
    clientID,
    (c) => ({
      ...c,
      destinations: [],
      policy_ids: [...c.policy_ids, policyID],
    }),
  );
  return { snapshot: next, policyID };
}

// blockDomainsForAll adds exact hosts to settings.blocked_domains (denied for
// every client before policies). Hosts already blocked are kept once.
export function blockDomainsForAll(draft: Snapshot, hosts: string[]): Snapshot {
  const parsed = parseAll(hosts).map((d) => ({
    domain: d.domain,
    include_subdomains: false,
  }));
  if (!parsed.length || !draft.settings) return draft;
  const current = draft.settings.blocked_domains ?? [];
  return {
    ...draft,
    settings: {
      ...draft.settings,
      blocked_domains: mergeDestinations(current, parsed),
    },
  };
}

// setClientEnabled toggles a client's enabled flag. No-op if client missing.
export function setClientEnabled(
  draft: Snapshot,
  clientID: string,
  enabled: boolean,
): Snapshot {
  return withClient(draft, clientID, (c) => ({ ...c, enabled }));
}

// deleteClient removes a client from the draft. Policies are left in place.
export function deleteClient(draft: Snapshot, clientID: string): Snapshot {
  if (!draft.clients.some((c) => c.id === clientID)) return draft;
  return {
    ...draft,
    clients: draft.clients.filter((c) => c.id !== clientID),
  };
}

// clientsOfPolicy returns the IDs of clients attaching a policy.
export function clientsOfPolicy(draft: Snapshot, policyID: string): string[] {
  return draft.clients
    .filter((c) => c.policy_ids.includes(policyID))
    .map((c) => c.id);
}

// deletePolicy removes a policy that no client attaches.
export function deletePolicy(draft: Snapshot, policyID: string): Snapshot {
  if (draft.clients.some((c) => c.policy_ids.includes(policyID))) return draft;
  return {
    ...draft,
    policies: draft.policies.filter((p) => p.id !== policyID),
  };
}

// renamePolicy changes a policy name. No-op when the policy is missing.
export function renamePolicy(
  draft: Snapshot,
  policyID: string,
  name: string,
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  if (!policy) return draft;
  return withPolicy(draft, policyID, { ...policy, name });
}

// addDomainsToPolicy merges parsed domain inputs into a policy's destination
// list. Invalid inputs are skipped.
export function addDomainsToPolicy(
  draft: Snapshot,
  policyID: string,
  inputs: string[],
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  const add = parseAll(inputs);
  if (!policy || add.length === 0) return draft;
  return withPolicy(draft, policyID, {
    ...policy,
    destinations: mergeDestinations(policy.destinations, add),
  });
}

// removeDomainFromPolicy drops a domain from a policy.
export function removeDomainFromPolicy(
  draft: Snapshot,
  policyID: string,
  domain: string,
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  if (!policy) return draft;
  return withPolicy(draft, policyID, {
    ...policy,
    destinations: policy.destinations.filter((d) => d.domain !== domain),
  });
}

// toggleDomainSubdomains flips include_subdomains for one policy domain.
export function toggleDomainSubdomains(
  draft: Snapshot,
  policyID: string,
  domain: string,
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  if (!policy) return draft;
  return withPolicy(draft, policyID, {
    ...policy,
    destinations: policy.destinations.map((d) =>
      d.domain === domain
        ? { ...d, include_subdomains: !d.include_subdomains }
        : d,
    ),
  });
}

// setClientNetworks replaces a client's source networks. Inputs that fail
// parseNetwork are dropped; if none parse, the snapshot is unchanged.
export function setClientNetworks(
  draft: Snapshot,
  clientID: string,
  inputs: string[],
): Snapshot {
  const nets: string[] = [];
  for (const raw of inputs) {
    const n = parseNetwork(raw);
    if (n) nets.push(n);
  }
  if (nets.length === 0) return draft;
  return withClient(draft, clientID, (c) => ({ ...c, source_networks: nets }));
}

// newClient is a list-access host client on the HTTP profile.
export function newClient(
  draft: Snapshot,
  fields: Pick<Client, "name" | "source_networks"> & Partial<Client>,
): Client {
  return {
    id: newEntityID(draft, "c-"),
    kind: "host",
    enabled: true,
    access: "list",
    tls_profile_id:
      draft.tls_profiles.find((p) => p.proxy_transport === "http")?.id ??
      draft.tls_profiles[0]?.id ??
      "",
    policy_ids: [],
    destinations: [],
    ...fields,
  };
}

// CreateClientResult is returned by createClientFromIP.
export type CreateClientResult = { snapshot: Snapshot; clientID: string };

// createClientFromIP builds a new host client from an unknown source IP with
// its own domains seeded from the given inputs. Returns null when the IP is
// not a valid network or the name is empty.
export function createClientFromIP(
  draft: Snapshot,
  ip: string,
  name: string,
  seedInputs: string[],
): CreateClientResult | null {
  const net = parseNetwork(ip);
  if (!net || !name.trim()) return null;
  const client = newClient(draft, {
    name,
    source_networks: [net],
    destinations: parseAll(seedInputs),
  });
  return {
    snapshot: { ...draft, clients: [...draft.clients, client] },
    clientID: client.id,
  };
}

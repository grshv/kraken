// Pure draft-edit helpers. Each returns a new snapshot; inputs are not mutated.
// Used by the Clients/Policies screens to compute the next draft before PUT,
// and by vitest to test the edit logic without React.

import type { Client, Policy, Snapshot } from "../model";
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
  return {
    ...draft,
    clients: draft.clients.map((c) => (c.id === id ? fn(c) : c)),
  };
}

// newEntityID returns the smallest "<prefix><n>" not already present.
export function newEntityID(draft: Snapshot, prefix: string): string {
  const ids = new Set<string>([
    ...draft.clients.map((c) => c.id),
    ...draft.policies.map((p) => p.id),
  ]);
  let n = 1;
  while (ids.has(prefix + n)) n++;
  return prefix + n;
}

// addDomainToClientPolicy merges a domain into the policy referenced by the
// client. No-op (returns input) when the client is missing, its policy is
// missing or not an allowlist, or the domain input is invalid.
export function addDomainToClientPolicy(
  draft: Snapshot,
  clientID: string,
  input: string,
): Snapshot {
  const client = draft.clients.find((c) => c.id === clientID);
  if (!client) return draft;
  const policy = draft.policies.find((p) => p.id === client.policy_id);
  if (!policy || policy.mode !== "allowlist") return draft;
  const parsed = parseDomainInput(input);
  if (!parsed) return draft;
  return withPolicy(draft, policy.id, {
    ...policy,
    destinations: mergeDestinations(policy.destinations, [parsed]),
  });
}

// changeClientPolicy reassigns a client to an existing policy. No-op when the
// client or the target policy is missing.
export function changeClientPolicy(
  draft: Snapshot,
  clientID: string,
  policyID: string,
): Snapshot {
  if (!draft.clients.some((c) => c.id === clientID)) return draft;
  if (!draft.policies.some((p) => p.id === policyID)) return draft;
  return withClient(draft, clientID, (c) => ({ ...c, policy_id: policyID }));
}

// setClientEnabled toggles a client's enabled flag. No-op if client missing.
export function setClientEnabled(
  draft: Snapshot,
  clientID: string,
  enabled: boolean,
): Snapshot {
  if (!draft.clients.some((c) => c.id === clientID)) return draft;
  return withClient(draft, clientID, (c) => ({ ...c, enabled }));
}

// deleteClient removes a client from the draft. Policies are left in place;
// the Policies screen deletes a policy only when it has no clients.
export function deleteClient(draft: Snapshot, clientID: string): Snapshot {
  if (!draft.clients.some((c) => c.id === clientID)) return draft;
  return {
    ...draft,
    clients: draft.clients.filter((c) => c.id !== clientID),
  };
}

// clientsOfPolicy returns the IDs of clients referencing a policy.
export function clientsOfPolicy(draft: Snapshot, policyID: string): string[] {
  return draft.clients.filter((c) => c.policy_id === policyID).map((c) => c.id);
}

// deletePolicy removes a policy. Caller must guard: only allowed when no client
// references it (enforced by the Policies screen).
export function deletePolicy(draft: Snapshot, policyID: string): Snapshot {
  if (draft.clients.some((c) => c.policy_id === policyID)) return draft;
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

// setPolicyMode switches a policy between allowlist and all_web. all_web clears
// destinations (the server rejects destinations on all_web policies).
export function setPolicyMode(
  draft: Snapshot,
  policyID: string,
  mode: Policy["mode"],
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  if (!policy) return draft;
  return withPolicy(draft, policyID, {
    ...policy,
    mode,
    destinations: mode === "all_web" ? [] : policy.destinations,
  });
}

// addDomainsToPolicy merges parsed domain inputs into a policy's destination
// list. Invalid inputs are skipped; a canonical allowlist results.
export function addDomainsToPolicy(
  draft: Snapshot,
  policyID: string,
  inputs: string[],
): Snapshot {
  const policy = draft.policies.find((p) => p.id === policyID);
  if (!policy || policy.mode !== "allowlist") return draft;
  const add: DomainEntry[] = [];
  for (const raw of inputs) {
    const d = parseDomainInput(raw);
    if (d) add.push(d);
  }
  if (add.length === 0) return draft;
  return withPolicy(draft, policyID, {
    ...policy,
    destinations: mergeDestinations(policy.destinations, add),
  });
}

// removeDomainFromPolicy drops a domain from a policy's allowlist.
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

// toggleDomainSubdomains flips include_subdomains for one domain.
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

// CreateClientResult is returned by createClientFromIP.
export type CreateClientResult = {
  snapshot: Snapshot;
  clientID: string;
  policyID: string;
};

// createClientFromIP builds a new host client from an unknown source IP, with a
// fresh own allowlist policy seeded by the given domain inputs. Returns null
// when the IP is not a valid network or the name is empty.
export function createClientFromIP(
  draft: Snapshot,
  ip: string,
  name: string,
  seedInputs: string[],
): CreateClientResult | null {
  const net = parseNetwork(ip);
  if (!net || !name.trim()) return null;
  const tlsID = draft.tls_profiles[0]?.id ?? "";
  const clientID = newEntityID(draft, "c-");
  const policyID = newEntityID(draft, "p-");
  const destinations = seedInputs
    .map((s) => parseDomainInput(s))
    .filter((d): d is DomainEntry => d !== null);
  const policy: Policy = {
    id: policyID,
    name,
    mode: "allowlist",
    destinations,
    tls_profile_id: tlsID,
  };
  const client: Client = {
    id: clientID,
    name,
    kind: "host",
    source_networks: [net],
    enabled: true,
    policy_id: policyID,
  };
  return {
    snapshot: {
      ...draft,
      clients: [...draft.clients, client],
      policies: [...draft.policies, policy],
    },
    clientID,
    policyID,
  };
}

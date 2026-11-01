// Pure draft-revert helpers. Each returns a new snapshot; inputs are not
// mutated. revertChange restores one object to its active-revision value, used
// by the review sheet's per-change "Отменить" action. No-op cases return the
// same draft object reference.

import type { Change, Client, Snapshot } from "../model";

export function canRevert(change: Pick<Change, "entity">): boolean {
  return change.entity !== "tls_profiles";
}

export function revertChange(
  draft: Snapshot,
  active: Snapshot,
  change: Pick<Change, "entity" | "id">,
): Snapshot {
  switch (change.entity) {
    case "client":
      return revertClient(draft, active, change.id);
    case "policy":
      return revertPolicy(draft, active, change.id);
    case "settings":
      return { ...draft, settings: active.settings };
    case "configuration": {
      const next: Snapshot = { ...draft };
      if (active.configuration_mode !== undefined) {
        next.configuration_mode = active.configuration_mode;
      } else {
        delete next.configuration_mode;
      }
      if (active.expert_config !== undefined) {
        next.expert_config = active.expert_config;
      } else {
        delete next.expert_config;
      }
      return next;
    }
    default:
      return draft;
  }
}

// restoreClientRefs appends active versions of policies and the TLS profile the
// reverted client references but the draft lacks, so the restored client stays
// valid. Called only when a client is replaced or added, never on removal.
function restoreClientRefs(
  next: Snapshot,
  active: Snapshot,
  client: Client,
): Snapshot {
  const missingPolicies = client.policy_ids.filter(
    (pid) => !next.policies.some((p) => p.id === pid),
  );
  const addPolicies = active.policies.filter((p) =>
    missingPolicies.includes(p.id),
  );
  if (addPolicies.length) {
    next = { ...next, policies: [...next.policies, ...addPolicies] };
  }
  if (
    client.tls_profile_id &&
    !next.tls_profiles.some((p) => p.id === client.tls_profile_id)
  ) {
    const tp = active.tls_profiles.find((p) => p.id === client.tls_profile_id);
    if (tp) {
      next = { ...next, tls_profiles: [...next.tls_profiles, tp] };
    }
  }
  return next;
}

function revertClient(
  draft: Snapshot,
  active: Snapshot,
  id: string | null,
): Snapshot {
  if (!id) return draft;
  const activeClient = active.clients.find((c) => c.id === id);
  const draftIdx = draft.clients.findIndex((c) => c.id === id);
  if (activeClient && draftIdx >= 0) {
    const clients = draft.clients.slice();
    clients[draftIdx] = activeClient;
    let next: Snapshot = { ...draft, clients };
    next = restoreClientRefs(next, active, activeClient);
    return next;
  }
  if (activeClient && draftIdx < 0) {
    let next: Snapshot = {
      ...draft,
      clients: [...draft.clients, activeClient],
    };
    next = restoreClientRefs(next, active, activeClient);
    return next;
  }
  if (!activeClient && draftIdx >= 0) {
    return {
      ...draft,
      clients: draft.clients.filter((c) => c.id !== id),
    };
  }
  return draft;
}

function revertPolicy(
  draft: Snapshot,
  active: Snapshot,
  id: string | null,
): Snapshot {
  if (!id) return draft;
  const activePolicy = active.policies.find((p) => p.id === id);
  const draftIdx = draft.policies.findIndex((p) => p.id === id);
  if (activePolicy && draftIdx >= 0) {
    const policies = draft.policies.slice();
    policies[draftIdx] = activePolicy;
    return { ...draft, policies };
  }
  if (activePolicy && draftIdx < 0) {
    return { ...draft, policies: [...draft.policies, activePolicy] };
  }
  if (!activePolicy && draftIdx >= 0) {
    const policies = draft.policies.filter((p) => p.id !== id);
    const clients = draft.clients.map((c) => ({
      ...c,
      policy_ids: c.policy_ids.filter((pid) => pid !== id),
    }));
    return { ...draft, policies, clients };
  }
  return draft;
}

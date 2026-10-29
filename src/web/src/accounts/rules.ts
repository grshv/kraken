// Pure rules for the Accounts screen. No DOM, no fetch.
//
// The server refuses to demote or disable the last enabled admin; these
// helpers mirror that guard client-side so the button can be disabled with a
// hint before the request, while the server remains the source of truth.

export type PanelUser = {
  username: string;
  role: "admin" | "manager";
  enabled: boolean;
};

// canDemote: false only when demoting this admin would leave zero enabled
// admins. Manager→admin and any non-admin user are always allowed.
export function canDemote(users: PanelUser[], username: string): boolean {
  if (!users.some((u) => u.username === username)) return true;
  const target = users.find((u) => u.username === username)!;
  if (target.role !== "admin") return true;
  const otherActiveAdmins = users.filter(
    (u) => u.username !== username && u.role === "admin" && u.enabled,
  );
  return otherActiveAdmins.length > 0;
}

// canDisable: false only when disabling this enabled admin would leave zero
// enabled admins. Disabling a manager or an already-disabled user is allowed.
export function canDisable(users: PanelUser[], username: string): boolean {
  if (!users.some((u) => u.username === username)) return true;
  const target = users.find((u) => u.username === username)!;
  if (!target.enabled) return true;
  if (target.role !== "admin") return true;
  const otherActiveAdmins = users.filter(
    (u) => u.username !== username && u.role === "admin" && u.enabled,
  );
  return otherActiveAdmins.length > 0;
}

// ManagerScope is the PUT /users/{username}/scope body: networks (CIDR) and
// client IDs assigned to a manager. parseNetwork from clients/networks.ts
// normalizes each CIDR; invalid entries are dropped so the server never sees
// them and the caller can warn.
export type ManagerScopeBody = {
  networks: string[];
  client_ids: string[];
};

// buildManagerScopeBody assembles the PUT body from raw network inputs and a
// set of client IDs. Empty/invalid CIDRs are dropped (the server would 422
// on them); the caller surfaces the rejection. Duplicates are removed while
// preserving first-seen order.
export function buildManagerScopeBody(
  networkInputs: string[],
  clientIDs: string[],
  parseNetwork: (s: string) => string | null,
): ManagerScopeBody {
  const nets: string[] = [];
  const seenNet = new Set<string>();
  for (const raw of networkInputs) {
    const n = parseNetwork(raw);
    if (!n || seenNet.has(n)) continue;
    seenNet.add(n);
    nets.push(n);
  }
  const ids: string[] = [];
  const seenId = new Set<string>();
  for (const id of clientIDs) {
    if (!id || seenId.has(id)) continue;
    seenId.add(id);
    ids.push(id);
  }
  return { networks: nets, client_ids: ids };
}

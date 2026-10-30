import type { Policy } from "../model";

export type HostCount = { host: string; requests: number; denied: number };

// policyCovers reports whether an allowlist entry matches host the way Squid
// dstdomain does (exact, or any subdomain when include_subdomains).
export function policyCovers(policy: Policy, host: string): boolean {
  return policy.destinations.some(
    (x) =>
      host === x.domain ||
      (x.include_subdomains && host.endsWith("." + x.domain)),
  );
}

// missingDomains: denied destinations a client's allowlist does not cover —
// the ones "Разрешить" can fix. Denials of covered hosts come from built-in
// protection or the global blocklist and are not offered.
export function missingDomains(
  top: HostCount[] | undefined,
  policy: Policy | null,
): HostCount[] {
  if (!policy || policy.mode !== "allowlist") return [];
  return (top ?? []).filter(
    (d) => d.denied > 0 && !policyCovers(policy, d.host),
  );
}

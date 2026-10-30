import type { Policy } from "../model";

export type HostCount = { host: string; requests: number; denied: number };

// missingDomains: denied destinations a client's allowlist does not cover —
// the ones "Разрешить" can fix. Denials of covered hosts come from built-in
// protection or the global blocklist and are not offered.
export function missingDomains(
  top: HostCount[] | undefined,
  policy: Policy | null,
): HostCount[] {
  if (!policy || policy.mode !== "allowlist") return [];
  return (top ?? []).filter(
    (d) =>
      d.denied > 0 &&
      !policy.destinations.some(
        (x) =>
          d.host === x.domain ||
          (x.include_subdomains && d.host.endsWith("." + x.domain)),
      ),
  );
}

// Pure settings-edit helpers. Each returns a new snapshot; inputs are not
// mutated. Used by the Settings screen to compute the next draft before PUT,
// and by vitest to test the edit logic without React.

import type { Destination, Settings, Snapshot, UpstreamProxy } from "../model";

function withSettings(draft: Snapshot, next: Settings): Snapshot {
  return { ...draft, settings: next };
}

// effectiveSettings returns the draft settings or defaults when absent.
export function effectiveSettings(draft: Snapshot): Settings {
  return draft.settings ?? defaults();
}

function defaults(): Settings {
  return {
    http_port: 3128,
    tls_port: 3129,
    safe_ports: [80, 443],
    connect_ports: [443],
    cache_enabled: false,
    cache_memory_mb: 64,
    maximum_object_kb: 512,
    dns_nameservers: [],
    positive_dns_ttl_seconds: 21600,
    negative_dns_ttl_seconds: 60,
    connect_timeout_seconds: 60,
    read_timeout_seconds: 900,
    request_timeout_seconds: 300,
    max_request_body_mb: 0,
    log_rotations: 3,
    allow_private_destinations: false,
    blocked_domains: [],
    hide_client_address: false,
    upstream_proxy: null,
  };
}

// patchSettings merges a partial patch into the effective settings. Fields not
// in the patch keep their current value.
export function patchSettings(
  draft: Snapshot,
  patch: Partial<Settings>,
): Snapshot {
  return withSettings(draft, { ...effectiveSettings(draft), ...patch });
}

// setBlockedDomains replaces the global blocklist. Used by "Запрещено для всех".
export function setBlockedDomains(
  draft: Snapshot,
  domains: Destination[],
): Snapshot {
  return patchSettings(draft, { blocked_domains: domains });
}

// setUpstream replaces the upstream proxy; null disables it.
export function setUpstream(
  draft: Snapshot,
  upstream: UpstreamProxy | null,
): Snapshot {
  return patchSettings(draft, { upstream_proxy: upstream });
}

// setExpertConfig switches the draft to expert mode with the given text; the
// server validates the allowlist at save time.
export function setExpertConfig(
  draft: Snapshot,
  expert_config: string,
  mode: "structured" | "expert",
): Snapshot {
  return { ...draft, configuration_mode: mode, expert_config };
}

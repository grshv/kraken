import { describe, it, expect } from "vitest";
import type { Snapshot, Destination, UpstreamProxy } from "../../src/model";
import {
  effectiveSettings,
  patchSettings,
  setBlockedDomains,
  setUpstream,
  setExpertConfig,
} from "../../src/settings/edits";

function base(): Snapshot {
  return {
    schema_version: 1,
    configuration_mode: "structured",
    settings: {
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
    },
    clients: [],
    policies: [],
    tls_profiles: [],
  };
}

describe("effectiveSettings", () => {
  it("returns draft settings when present", () => {
    const s = base();
    expect(effectiveSettings(s).cache_memory_mb).toBe(64);
  });
  it("returns defaults when settings is absent", () => {
    const s = base();
    s.settings = undefined;
    const e = effectiveSettings(s);
    expect(e.safe_ports).toEqual([80, 443]);
    expect(e.upstream_proxy).toBeNull();
    expect(e.blocked_domains).toEqual([]);
    expect(e.hide_client_address).toBe(false);
  });
});

describe("patchSettings", () => {
  it("merges only patched fields, preserves the rest", () => {
    const s = base();
    const next = patchSettings(s, { cache_memory_mb: 128, hide_client_address: true });
    expect(next.settings?.cache_memory_mb).toBe(128);
    expect(next.settings?.hide_client_address).toBe(true);
    expect(next.settings?.safe_ports).toEqual([80, 443]);
    expect(next.settings?.connect_ports).toEqual([443]);
  });
  it("does not mutate the input snapshot", () => {
    const s = base();
    const before = JSON.stringify(s);
    patchSettings(s, { cache_enabled: true });
    expect(JSON.stringify(s)).toBe(before);
  });
});

describe("setBlockedDomains", () => {
  it("replaces the blocklist", () => {
    const s = base();
    const doms: Destination[] = [
      { domain: "evil.example", include_subdomains: true },
    ];
    const next = setBlockedDomains(s, doms);
    expect(next.settings?.blocked_domains).toEqual(doms);
  });
});

describe("setUpstream", () => {
  it("stores the upstream proxy", () => {
    const s = base();
    const up: UpstreamProxy = { address: "10.0.0.10", port: 3128, exclusive: true };
    const next = setUpstream(s, up);
    expect(next.settings?.upstream_proxy).toEqual(up);
  });
  it("null clears the upstream proxy", () => {
    const s = base();
    s.settings!.upstream_proxy = { address: "10.0.0.10", port: 3128, exclusive: false };
    const next = setUpstream(s, null);
    expect(next.settings?.upstream_proxy).toBeNull();
  });
});

describe("setExpertConfig", () => {
  it("switches mode and stores text", () => {
    const s = base();
    const next = setExpertConfig(s, "acl office src 10.8.0.0/16\n", "expert");
    expect(next.configuration_mode).toBe("expert");
    expect(next.expert_config).toBe("acl office src 10.8.0.0/16\n");
  });
});

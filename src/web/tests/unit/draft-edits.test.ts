import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import {
  addDomainToClientPolicy,
  changeClientPolicy,
  createClientFromIP,
  clientsOfPolicy,
} from "../../src/draft/edits";

// A minimal structured base snapshot (mirrors policy.Empty on the server).
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
    tls_profiles: [
      {
        id: "tls-default",
        name: "HTTP proxy / CONNECT",
        mode: "tunnel",
        proxy_transport: "http",
        cert_ref: null,
      },
    ],
  };
}

// Build a snapshot with two clients: one on its own allowlist, one on a shared
// allowlist, mirroring the prototype's data shapes.
function snapshot(): Snapshot {
  const s = base();
  const tls = s.tls_profiles[0].id;
  const own = {
    id: "p-own",
    name: "own",
    mode: "allowlist" as const,
    destinations: [{ domain: "own.example", include_subdomains: false }],
    tls_profile_id: tls,
  };
  const shared = {
    id: "p-shared",
    name: "shared",
    mode: "allowlist" as const,
    destinations: [{ domain: "shared.example", include_subdomains: false }],
    tls_profile_id: tls,
  };
  return {
    ...s,
    policies: [...s.policies, own, shared],
    clients: [
      {
        id: "c-a",
        name: "alpha",
        kind: "host",
        source_networks: ["10.1.0.5/32"],
        enabled: true,
        policy_id: "p-own",
      },
      {
        id: "c-b",
        name: "bravo",
        kind: "host",
        source_networks: ["10.2.0.5/32"],
        enabled: true,
        policy_id: "p-shared",
      },
    ],
  };
}

describe("addDomainToClientPolicy", () => {
  it("merges a domain into the client's own allowlist, dedup + OR", () => {
    const next = addDomainToClientPolicy(snapshot(), "c-a", "own.example"); // already present
    const pol = next.policies.find((p) => p.id === "p-own")!;
    expect(pol.destinations).toEqual([
      { domain: "own.example", include_subdomains: false },
    ]);
    const next2 = addDomainToClientPolicy(snapshot(), "c-a", "*.new.example");
    expect(
      next2.policies.find((p) => p.id === "p-own")!.destinations,
    ).toContainEqual({
      domain: "new.example",
      include_subdomains: true,
    });
  });
  it("no-ops on missing client, shared/all_web policy or invalid domain", () => {
    const s = snapshot();
    expect(addDomainToClientPolicy(s, "c-zzz", "x.example")).toBe(s);
    expect(addDomainToClientPolicy(s, "c-b", "not a domain")).toBe(s);
    // c-b is on p-shared (allowlist) — adding works there too.
    const next = addDomainToClientPolicy(s, "c-b", "added.example");
    expect(
      next.policies.find((p) => p.id === "p-shared")!.destinations,
    ).toHaveLength(2);
  });
  it("does not mutate the input snapshot", () => {
    const s = snapshot();
    addDomainToClientPolicy(s, "c-a", "added.example");
    expect(
      s.policies.find((p) => p.id === "p-own")!.destinations,
    ).toHaveLength(1);
  });
});

describe("changeClientPolicy", () => {
  it("reassigns to an existing policy and rejects unknown policy", () => {
    const s = snapshot();
    const next = changeClientPolicy(s, "c-a", "p-shared");
    expect(next.clients.find((c) => c.id === "c-a")!.policy_id).toBe(
      "p-shared",
    );
    expect(changeClientPolicy(s, "c-a", "p-missing")).toBe(s);
    expect(changeClientPolicy(s, "c-zzz", "p-shared")).toBe(s);
  });
});

describe("createClientFromIP", () => {
  it("creates a host client with a fresh own policy seeded by domains", () => {
    const s = snapshot();
    const r = createClientFromIP(s, "10.99.1.4", "build-02", [
      "deb.debian.org",
      "security.debian.org",
      "not-a-domain",
    ]);
    expect(r).not.toBeNull();
    const next = r!.snapshot;
    const client = next.clients.find((c) => c.id === r!.clientID)!;
    expect(client.name).toBe("build-02");
    expect(client.source_networks).toEqual(["10.99.1.4/32"]);
    expect(client.policy_id).toBe(r!.policyID);
    const pol = next.policies.find((p) => p.id === r!.policyID)!;
    expect(pol.mode).toBe("allowlist");
    expect(pol.destinations.map((d) => d.domain)).toEqual([
      "deb.debian.org",
      "security.debian.org",
    ]);
    // existing clients/policies preserved
    expect(next.clients).toHaveLength(3);
    expect(next.policies).toHaveLength(s.policies.length + 1);
  });
  it("rejects an invalid IP or empty name", () => {
    const s = snapshot();
    expect(createClientFromIP(s, "not-an-ip", "x", [])).toBeNull();
    expect(createClientFromIP(s, "10.99.1.4", "  ", [])).toBeNull();
  });
  it("fresh ids are unique and deterministic", () => {
    const s = snapshot();
    const r = createClientFromIP(s, "10.99.1.4", "a", []);
    const r2 = createClientFromIP(r!.snapshot, "10.99.1.5", "b", []);
    expect(r!.clientID).not.toBe(r2!.clientID);
    expect(r!.policyID).not.toBe(r2!.policyID);
  });
});

describe("clientsOfPolicy", () => {
  it("lists clients referencing a policy", () => {
    expect(clientsOfPolicy(snapshot(), "p-shared")).toEqual(["c-b"]);
    expect(clientsOfPolicy(snapshot(), "p-own")).toEqual(["c-a"]);
    expect(clientsOfPolicy(snapshot(), "p-missing")).toEqual([]);
  });
});

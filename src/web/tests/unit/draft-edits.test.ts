import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import {
  addDomainToClient,
  addDomainsToClient,
  attachPolicy,
  blockDomainsForAll,
  clientAllows,
  clientTransport,
  clientsOfPolicy,
  createClientFromIP,
  deletePolicy,
  detachPolicy,
  effectiveDestinations,
  setClientAccess,
  setClientConnection,
  shareClientDomains,
} from "../../src/draft/edits";

// A minimal structured base snapshot (mirrors policy.Empty on the server).
function base(): Snapshot {
  return {
    schema_version: 2,
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

// Two clients: alpha keeps own domains and attaches "build"; bravo attaches
// "build" and "docs". Policies are plain domain sets.
function snapshot(): Snapshot {
  const s = base();
  const tls = s.tls_profiles[0].id;
  const d = (domain: string, include_subdomains = false) => ({
    domain,
    include_subdomains,
  });
  return {
    ...s,
    policies: [
      { id: "p-build", name: "build", destinations: [d("debian.org", true)] },
      {
        id: "p-docs",
        name: "docs",
        destinations: [d("docs.example"), d("own.example")],
      },
    ],
    clients: [
      {
        id: "c-a",
        name: "alpha",
        kind: "host",
        source_networks: ["10.1.0.5/32"],
        enabled: true,
        access: "list",
        tls_profile_id: tls,
        policy_ids: ["p-build"],
        destinations: [d("own.example"), d("deb.debian.org")],
      },
      {
        id: "c-b",
        name: "bravo",
        kind: "host",
        source_networks: ["10.2.0.5/32"],
        enabled: true,
        access: "list",
        tls_profile_id: tls,
        policy_ids: ["p-build", "p-docs"],
        destinations: [],
      },
    ],
  };
}

const client = (s: Snapshot, id: string) => s.clients.find((c) => c.id === id)!;

describe("effectiveDestinations", () => {
  it("unions own domains and policies, drops covered entries, keeps sources", () => {
    const s = snapshot();
    // deb.debian.org is covered by *.debian.org from "build".
    expect(effectiveDestinations(s, client(s, "c-a"))).toEqual([
      {
        destination: { domain: "debian.org", include_subdomains: true },
        own: false,
        policyIDs: ["p-build"],
      },
      {
        destination: { domain: "own.example", include_subdomains: false },
        own: true,
        policyIDs: [],
      },
    ]);
    const b = effectiveDestinations(s, client(s, "c-b"));
    expect(b.map((e) => e.destination.domain)).toEqual([
      "debian.org",
      "docs.example",
      "own.example",
    ]);
  });
  it("merges the same domain from own list and a policy into one line", () => {
    const s = attachPolicy(snapshot(), "c-a", "p-docs");
    const own = effectiveDestinations(s, client(s, "c-a")).find(
      (e) => e.destination.domain === "own.example",
    )!;
    expect(own.own).toBe(true);
    expect(own.policyIDs).toEqual(["p-docs"]);
  });
});

describe("clientAllows", () => {
  it("follows the access mode", () => {
    const s = snapshot();
    expect(clientAllows(s, client(s, "c-a"), "security.debian.org")).toBe(true);
    expect(clientAllows(s, client(s, "c-a"), "docs.example")).toBe(false);
    const none = setClientAccess(s, "c-a", "none");
    expect(clientAllows(none, client(none, "c-a"), "own.example")).toBe(false);
    const web = setClientAccess(s, "c-a", "all_web");
    expect(clientAllows(web, client(web, "c-a"), "anything.test")).toBe(true);
  });
});

describe("addDomainToClient", () => {
  it("merges into the client's own domains, not into a policy", () => {
    const s = snapshot();
    const next = addDomainToClient(s, "c-b", "*.new.example");
    expect(client(next, "c-b").destinations).toEqual([
      { domain: "new.example", include_subdomains: true },
    ]);
    expect(next.policies).toBe(s.policies);
  });
  it("switches access none to list and no-ops on all_web or bad input", () => {
    const s = setClientAccess(snapshot(), "c-a", "none");
    expect(client(addDomainToClient(s, "c-a", "x.example"), "c-a").access).toBe(
      "list",
    );
    const web = setClientAccess(snapshot(), "c-a", "all_web");
    expect(addDomainToClient(web, "c-a", "x.example")).toBe(web);
    expect(addDomainToClient(s, "c-zzz", "x.example")).toBe(s);
    expect(addDomainToClient(s, "c-a", "not a domain")).toBe(s);
  });
  it("does not mutate the input snapshot", () => {
    const s = snapshot();
    addDomainToClient(s, "c-a", "added.example");
    expect(client(s, "c-a").destinations).toHaveLength(2);
  });
});

describe("addDomainsToClient", () => {
  it("adds several hosts in one edit and skips invalid ones", () => {
    const next = addDomainsToClient(snapshot(), "c-b", [
      "pypi.org",
      "pypi.org",
      "not a domain",
    ]);
    expect(client(next, "c-b").destinations).toEqual([
      { domain: "pypi.org", include_subdomains: false },
    ]);
  });
  it("no-ops on a missing client or no valid host", () => {
    const s = snapshot();
    expect(addDomainsToClient(s, "c-zzz", ["x.example"])).toBe(s);
    expect(addDomainsToClient(s, "c-a", ["bad host"])).toBe(s);
  });
});

describe("attachPolicy / detachPolicy", () => {
  it("attaches once, rejects unknown policies and detaches", () => {
    const s = snapshot();
    const once = attachPolicy(
      attachPolicy(s, "c-a", "p-docs"),
      "c-a",
      "p-docs",
    );
    expect(client(once, "c-a").policy_ids).toEqual(["p-build", "p-docs"]);
    expect(attachPolicy(s, "c-a", "p-missing")).toBe(s);
    expect(
      client(detachPolicy(once, "c-a", "p-build"), "c-a").policy_ids,
    ).toEqual(["p-docs"]);
  });
  it("switches access none to list", () => {
    const s = setClientAccess(snapshot(), "c-a", "none");
    expect(client(attachPolicy(s, "c-a", "p-docs"), "c-a").access).toBe("list");
  });
});

describe("setClientAccess", () => {
  it("keeps the lists so switching back restores them", () => {
    const s = snapshot();
    const back = setClientAccess(
      setClientAccess(s, "c-a", "all_web"),
      "c-a",
      "list",
    );
    expect(client(back, "c-a")).toEqual(client(s, "c-a"));
  });
});

describe("shareClientDomains", () => {
  it("moves own domains into a new policy attached to the client", () => {
    const s = snapshot();
    const r = shareClientDomains(s, "c-a", " alpha-set ")!;
    const pol = r.snapshot.policies.find((p) => p.id === r.policyID)!;
    expect(pol.name).toBe("alpha-set");
    expect(pol.destinations).toEqual(client(s, "c-a").destinations);
    const a = client(r.snapshot, "c-a");
    expect(a.destinations).toEqual([]);
    expect(a.policy_ids).toEqual(["p-build", r.policyID]);
    // The final list does not change.
    expect(
      effectiveDestinations(r.snapshot, a).map((e) => e.destination),
    ).toEqual(
      effectiveDestinations(s, client(s, "c-a")).map((e) => e.destination),
    );
  });
  it("needs own domains and a name", () => {
    const s = snapshot();
    expect(shareClientDomains(s, "c-b", "x")).toBeNull();
    expect(shareClientDomains(s, "c-a", "  ")).toBeNull();
  });
});

describe("setClientConnection", () => {
  it("a login moves the client to a TLS profile, created once", () => {
    const s = snapshot();
    const tls = setClientConnection(s, "c-a", "ci");
    const a = client(tls, "c-a");
    expect(a.proxy_username).toBe("ci");
    expect(clientTransport(tls, a)).toBe("tls");
    const p = tls.tls_profiles.find((x) => x.id === a.tls_profile_id)!;
    expect(p.cert_ref).toBe("proxy-server");
    const again = setClientConnection(tls, "c-b", "ci2");
    expect(again.tls_profiles).toHaveLength(2);
    expect(client(again, "c-b").tls_profile_id).toBe(a.tls_profile_id);
  });
  it("no login returns to HTTP and clears the username", () => {
    const tls = setClientConnection(snapshot(), "c-a", "ci");
    const http = setClientConnection(tls, "c-a", undefined);
    const a = client(http, "c-a");
    expect(a.proxy_username).toBeUndefined();
    expect(a.tls_profile_id).toBe("tls-default");
    expect(clientTransport(http, a)).toBe("http");
  });
});

describe("createClientFromIP", () => {
  it("creates a list-access host client with own domains, no policy", () => {
    const s = snapshot();
    const r = createClientFromIP(s, "10.99.1.4", "build-02", [
      "deb.debian.org",
      "security.debian.org",
      "not-a-domain",
    ]);
    expect(r).not.toBeNull();
    const next = r!.snapshot;
    const c = client(next, r!.clientID);
    expect(c.name).toBe("build-02");
    expect(c.source_networks).toEqual(["10.99.1.4/32"]);
    expect(c.access).toBe("list");
    expect(c.policy_ids).toEqual([]);
    expect(c.tls_profile_id).toBe("tls-default");
    expect(c.destinations.map((d) => d.domain)).toEqual([
      "deb.debian.org",
      "security.debian.org",
    ]);
    expect(next.clients).toHaveLength(3);
    expect(next.policies).toBe(s.policies);
  });
  it("rejects an invalid IP or empty name", () => {
    const s = snapshot();
    expect(createClientFromIP(s, "not-an-ip", "x", [])).toBeNull();
    expect(createClientFromIP(s, "10.99.1.4", "  ", [])).toBeNull();
  });
  it("fresh ids are unique", () => {
    const r = createClientFromIP(snapshot(), "10.99.1.4", "a", []);
    const r2 = createClientFromIP(r!.snapshot, "10.99.1.5", "b", []);
    expect(r!.clientID).not.toBe(r2!.clientID);
  });
});

describe("clientsOfPolicy / deletePolicy", () => {
  it("lists clients attaching a policy and keeps an attached one", () => {
    const s = snapshot();
    expect(clientsOfPolicy(s, "p-build")).toEqual(["c-a", "c-b"]);
    expect(clientsOfPolicy(s, "p-docs")).toEqual(["c-b"]);
    expect(clientsOfPolicy(s, "p-missing")).toEqual([]);
    expect(deletePolicy(s, "p-docs")).toBe(s);
    const free = detachPolicy(s, "c-b", "p-docs");
    expect(deletePolicy(free, "p-docs").policies.map((p) => p.id)).toEqual([
      "p-build",
    ]);
  });
});

describe("blockDomainsForAll", () => {
  it("adds exact hosts to settings.blocked_domains once", () => {
    const once = blockDomainsForAll(snapshot(), ["evil.test", "evil.test"]);
    const twice = blockDomainsForAll(once, ["evil.test", "other.test"]);
    expect(twice.settings!.blocked_domains).toEqual([
      { domain: "evil.test", include_subdomains: false },
      { domain: "other.test", include_subdomains: false },
    ]);
  });
  it("no-ops without valid hosts", () => {
    const s = snapshot();
    expect(blockDomainsForAll(s, ["bad host"])).toBe(s);
  });
});

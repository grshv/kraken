import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import { canRevert, revertChange } from "../../src/draft/revert";

const d = (domain: string, include_subdomains = false) => ({
  domain,
  include_subdomains,
});

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
        id: "tls-http",
        name: "HTTP proxy / CONNECT",
        mode: "tunnel",
        proxy_transport: "http",
        cert_ref: null,
      },
    ],
  };
}

const tls = (id: string, transport: "http" | "tls") => ({
  id,
  name: transport === "tls" ? "TLS proxy / CONNECT" : "HTTP proxy / CONNECT",
  mode: "tunnel",
  proxy_transport: transport,
  cert_ref: transport === "tls" ? ("proxy-server" as string | null) : null,
});

describe("canRevert", () => {
  it("is true for client, policy, settings, configuration and false for tls_profiles", () => {
    expect(canRevert({ entity: "client" })).toBe(true);
    expect(canRevert({ entity: "policy" })).toBe(true);
    expect(canRevert({ entity: "settings" })).toBe(true);
    expect(canRevert({ entity: "configuration" })).toBe(true);
    expect(canRevert({ entity: "tls_profiles" })).toBe(false);
  });
});

describe("revertChange client", () => {
  it("replaces the draft version with the active one at the same position", () => {
    const active: Snapshot = {
      ...base(),
      clients: [
        {
          id: "c-a",
          name: "alpha",
          kind: "host",
          source_networks: ["10.1.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
          description: "active",
        },
        {
          id: "c-z",
          name: "zulu",
          kind: "host",
          source_networks: ["10.1.0.9/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
        },
      ],
    };
    const draft: Snapshot = {
      ...active,
      clients: [
        {
          ...active.clients[0],
          description: "draft edit",
        },
        active.clients[1],
      ],
    };
    const next = revertChange(draft, active, {
      entity: "client",
      id: "c-a",
    });
    expect(next.clients[0].description).toBe("active");
    expect(next.clients[1]).toBe(draft.clients[1]);
    expect(next.clients).toHaveLength(2);
  });

  it("removes a draft-only client without touching anything else", () => {
    const active: Snapshot = { ...base() };
    const draft: Snapshot = {
      ...base(),
      clients: [
        {
          id: "c-b",
          name: "bravo",
          kind: "host",
          source_networks: ["10.2.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
        },
      ],
    };
    const next = revertChange(draft, active, {
      entity: "client",
      id: "c-b",
    });
    expect(next.clients).toEqual([]);
    // policies and tls_profiles untouched.
    expect(next.policies).toBe(draft.policies);
    expect(next.tls_profiles).toBe(draft.tls_profiles);
  });

  it("appends an active-only client to the end of clients", () => {
    const active: Snapshot = {
      ...base(),
      clients: [
        {
          id: "c-c",
          name: "charlie",
          kind: "host",
          source_networks: ["10.3.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
        },
      ],
    };
    const draft: Snapshot = { ...base() };
    const next = revertChange(draft, active, {
      entity: "client",
      id: "c-c",
    });
    expect(next.clients).toEqual([active.clients[0]]);
  });

  it("restores missing referenced policies and the TLS profile with the client", () => {
    const active: Snapshot = {
      ...base(),
      tls_profiles: [tls("tls-http", "http"), tls("tls-tls", "tls")],
      policies: [
        {
          id: "p-shared",
          name: "shared",
          destinations: [d("shared.example")],
        },
        {
          id: "p-active-only",
          name: "active-only",
          destinations: [d("active.example")],
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
          tls_profile_id: "tls-tls",
          policy_ids: ["p-active-only"],
          destinations: [],
        },
      ],
    };
    const draft: Snapshot = {
      ...active,
      policies: [active.policies[0]],
      tls_profiles: [active.tls_profiles[0]],
      clients: [{ ...active.clients[0], description: "draft edit" }],
    };
    const next = revertChange(draft, active, {
      entity: "client",
      id: "c-a",
    });
    expect(next.clients[0].description).toBeUndefined();
    expect(next.clients[0].tls_profile_id).toBe("tls-tls");
    expect(next.policies.map((p) => p.id)).toEqual([
      "p-shared",
      "p-active-only",
    ]);
    expect(next.policies[1]).toEqual(active.policies[1]);
    expect(next.tls_profiles.map((p) => p.id)).toEqual(["tls-http", "tls-tls"]);
    expect(next.tls_profiles[1]).toEqual(active.tls_profiles[1]);
  });
});

describe("revertChange policy", () => {
  it("replaces the draft version with the active one at the same position", () => {
    const active: Snapshot = {
      ...base(),
      policies: [
        { id: "p-keep", name: "keep", destinations: [] },
        { id: "p-x", name: "x", destinations: [d("a.example")] },
      ],
    };
    const draft: Snapshot = {
      ...active,
      policies: [
        active.policies[0],
        {
          id: "p-x",
          name: "x",
          destinations: [d("a.example"), d("b.example")],
        },
      ],
    };
    const next = revertChange(draft, active, {
      entity: "policy",
      id: "p-x",
    });
    expect(next.policies[1]).toEqual(active.policies[1]);
    expect(next.policies[0]).toBe(draft.policies[0]);
  });

  it("removes a draft-only policy and detaches it from all clients", () => {
    const active: Snapshot = { ...base() };
    const draft: Snapshot = {
      ...base(),
      policies: [{ id: "p-new", name: "new", destinations: [] }],
      clients: [
        {
          id: "c-a",
          name: "alpha",
          kind: "host",
          source_networks: ["10.1.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: ["p-new"],
          destinations: [],
        },
        {
          id: "c-b",
          name: "bravo",
          kind: "host",
          source_networks: ["10.2.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: ["p-new", "p-other"],
          destinations: [],
        },
      ],
    };
    const next = revertChange(draft, active, {
      entity: "policy",
      id: "p-new",
    });
    expect(next.policies).toEqual([]);
    expect(next.clients[0].policy_ids).toEqual([]);
    expect(next.clients[1].policy_ids).toEqual(["p-other"]);
  });

  it("appends an active-only policy without attaching it to clients", () => {
    const active: Snapshot = {
      ...base(),
      policies: [{ id: "p-z", name: "z", destinations: [d("z.example")] }],
    };
    const draft: Snapshot = {
      ...base(),
      clients: [
        {
          id: "c-a",
          name: "alpha",
          kind: "host",
          source_networks: ["10.1.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
        },
      ],
    };
    const next = revertChange(draft, active, {
      entity: "policy",
      id: "p-z",
    });
    expect(next.policies).toEqual([active.policies[0]]);
    expect(next.clients).toBe(draft.clients);
  });
});

describe("revertChange settings", () => {
  it("copies settings from active", () => {
    const active: Snapshot = { ...base() };
    active.settings!.connect_timeout_seconds = 45;
    const draft: Snapshot = { ...base() };
    const next = revertChange(draft, active, {
      entity: "settings",
      id: null,
    });
    expect(next.settings).toBe(active.settings);
    expect(next.settings!.connect_timeout_seconds).toBe(45);
  });
});

describe("revertChange configuration", () => {
  it("copies configuration_mode and expert_config from active", () => {
    const active: Snapshot = {
      ...base(),
      configuration_mode: "expert",
      expert_config: "http_access deny all",
    };
    const draft: Snapshot = { ...base() };
    const next = revertChange(draft, active, {
      entity: "configuration",
      id: null,
    });
    expect(next.configuration_mode).toBe("expert");
    expect(next.expert_config).toBe("http_access deny all");
  });

  it("drops configuration_mode and expert_config when active has none", () => {
    const active: Snapshot = { ...base() };
    delete active.configuration_mode;
    delete active.expert_config;
    const draft: Snapshot = {
      ...base(),
      configuration_mode: "structured",
      expert_config: "http_access deny all",
    };
    const next = revertChange(draft, active, {
      entity: "configuration",
      id: null,
    });
    expect(next.configuration_mode).toBeUndefined();
    expect(next.expert_config).toBeUndefined();
  });
});

describe("revertChange no-op", () => {
  function snapshots(): { active: Snapshot; draft: Snapshot } {
    const active: Snapshot = {
      ...base(),
      clients: [
        {
          id: "c-a",
          name: "alpha",
          kind: "host",
          source_networks: ["10.1.0.5/32"],
          enabled: true,
          access: "list",
          tls_profile_id: "tls-http",
          policy_ids: [],
          destinations: [],
        },
      ],
    };
    const draft: Snapshot = {
      ...active,
      clients: [{ ...active.clients[0], description: "draft" }],
    };
    return { active, draft };
  }

  it("returns the same draft reference for tls_profiles", () => {
    const { active, draft } = snapshots();
    expect(
      revertChange(draft, active, { entity: "tls_profiles", id: null }),
    ).toBe(draft);
  });

  it("returns the same draft reference for an unknown id", () => {
    const { active, draft } = snapshots();
    expect(revertChange(draft, active, { entity: "client", id: "nope" })).toBe(
      draft,
    );
    expect(revertChange(draft, active, { entity: "policy", id: "nope" })).toBe(
      draft,
    );
  });

  it("returns the same draft reference for a null id", () => {
    const { active, draft } = snapshots();
    expect(revertChange(draft, active, { entity: "client", id: null })).toBe(
      draft,
    );
    expect(revertChange(draft, active, { entity: "policy", id: null })).toBe(
      draft,
    );
  });

  it("does not mutate inputs", () => {
    const { active, draft } = snapshots();
    const beforeDraft = JSON.stringify(draft);
    const beforeActive = JSON.stringify(active);
    revertChange(draft, active, { entity: "client", id: "c-a" });
    expect(JSON.stringify(draft)).toBe(beforeDraft);
    expect(JSON.stringify(active)).toBe(beforeActive);
  });
});

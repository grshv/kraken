import { describe, expect, it } from "vitest";
import type { ApiRequest, Client, Snapshot } from "../../src/model";
import type { Notice } from "../../src/clients/notifications";
import {
  loadPolicySuggestions,
  policySuggestions,
} from "../../src/policies/suggestions";

const client: Client = {
  id: "c-ci",
  name: "ci",
  kind: "host",
  source_networks: ["10.0.0.5/32"],
  enabled: true,
  access: "list",
  tls_profile_id: "http",
  policy_ids: ["p-build"],
  destinations: [],
};

function snapshot(): Snapshot {
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
    clients: [client],
    policies: [
      {
        id: "p-build",
        name: "build",
        destinations: [{ domain: "debian.org", include_subdomains: true }],
      },
    ],
    tls_profiles: [
      {
        id: "http",
        name: "HTTP",
        mode: "tunnel",
        proxy_transport: "http",
        cert_ref: null,
      },
    ],
  };
}

const notice = (clientID: string, host = "registry.npmjs.org"): Notice => ({
  id: `${clientID}|${host}`,
  kind: "denied_domain",
  status: "new",
  client_id: clientID,
  client_name: null,
  source_ip: null,
  host,
  requests: 3,
  denied: 3,
  last_seen: "2026-10-30T14:05:00+03:00",
  marked_by: null,
  marked_at: null,
});

describe("policy suggestions", () => {
  it("tells an empty answer from a failure and a malformed answer", async () => {
    const snap = snapshot();
    const failure = new Error("connection failed");
    await expect(
      loadPolicySuggestions(
        (async () => {
          throw failure;
        }) as ApiRequest,
        snap,
        snap.clients,
      ),
    ).rejects.toBe(failure);
    await expect(
      loadPolicySuggestions(
        (async () => ({})) as ApiRequest,
        snap,
        snap.clients,
      ),
    ).rejects.toThrow("Не удалось прочитать уведомления");
    await expect(
      loadPolicySuggestions(
        (async () => ({ items: [] })) as ApiRequest,
        snap,
        snap.clients,
      ),
    ).resolves.toEqual([]);
  });

  it("reads denied-domain notifications, not the traffic aggregate", async () => {
    const snap = snapshot();
    let path = "";
    await loadPolicySuggestions(
      (async (p: string) => {
        path = p;
        return { items: [] };
      }) as ApiRequest,
      snap,
      snap.clients,
    );
    expect(path).toBe("/notifications?status=all&kind=denied_domain");
  });

  it("suggests only list denials of the given clients", () => {
    const rows = policySuggestions(
      [
        notice(client.id),
        notice("foreign"),
        { ...notice(client.id), kind: "unknown_source" },
        { ...notice(client.id), status: "ignored" },
        { ...notice(client.id), denied: 0 },
      ],
      snapshot(),
      [client],
    );
    expect(rows).toEqual([
      {
        host: "registry.npmjs.org",
        denied: 3,
        client: "ci",
        clientID: client.id,
      },
    ]);
  });

  it("skips domains already allowed or denied for everyone", () => {
    const snap = snapshot();
    const n = notice(client.id);
    expect(
      policySuggestions([n], snap, [
        {
          ...client,
          destinations: [{ domain: n.host!, include_subdomains: false }],
        },
      ]),
    ).toEqual([]);
    expect(
      policySuggestions([notice(client.id, "deb.debian.org")], snap, [client]),
    ).toEqual([]);
    expect(
      policySuggestions(
        [n],
        {
          ...snap,
          settings: {
            ...snap.settings!,
            blocked_domains: [
              { domain: "npmjs.org", include_subdomains: true },
            ],
          },
        },
        [client],
      ),
    ).toEqual([]);
    expect(policySuggestions([n], snap, [client])).toHaveLength(1);
  });

  it("never widens a disabled, all-web, no-access or expert client", () => {
    const snap = snapshot();
    const n = notice(client.id);
    for (const c of [
      { ...client, enabled: false },
      { ...client, access: "all_web" as const },
      { ...client, access: "none" as const },
    ])
      expect(policySuggestions([n], snap, [c])).toEqual([]);
    expect(
      policySuggestions([n], { ...snap, configuration_mode: "expert" }, [
        client,
      ]),
    ).toEqual([]);
  });
});

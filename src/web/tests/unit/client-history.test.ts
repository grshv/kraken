import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import { clientChanges } from "../../src/clients/history";
import { missingDomains } from "../../src/clients/missing";

function snap(over: {
  networks?: string[];
  domains?: [string, boolean][];
  mode?: "allowlist" | "all_web";
  policyID?: string;
  enabled?: boolean;
  login?: string;
}): Snapshot {
  const policyID = over.policyID ?? "p-1";
  return {
    schema_version: 1,
    clients: [
      {
        id: "c-1",
        name: "ci-01",
        source_networks: over.networks ?? ["10.20.0.14/32"],
        enabled: over.enabled ?? true,
        policy_id: policyID,
        proxy_username: over.login,
      },
    ],
    policies: [
      {
        id: policyID,
        name: policyID === "p-1" ? "ci-01" : "Сборка",
        mode: over.mode ?? "allowlist",
        destinations: (over.domains ?? [["pypi.org", false]]).map(
          ([domain, include_subdomains]) => ({ domain, include_subdomains }),
        ),
        tls_profile_id: "tls",
      },
    ],
    tls_profiles: [],
  } as unknown as Snapshot;
}

describe("clientChanges", () => {
  it("describes a new client with its domains", () => {
    expect(clientChanges(null, snap({}), "c-1")).toEqual([
      "Создан клиент, политика: pypi.org",
    ]);
  });

  it("describes source, domain and login changes", () => {
    const before = snap({});
    const after = snap({
      networks: ["10.20.0.15/32"],
      domains: [
        ["pypi.org", false],
        ["pythonhosted.org", true],
      ],
      login: "ci",
    });
    expect(clientChanges(before, after, "c-1")).toEqual([
      "Источник 10.20.0.14/32 → 10.20.0.15/32",
      "Логин прокси: ci",
      "Добавлен *.pythonhosted.org",
    ]);
  });

  it("reports a subdomain toggle as one change", () => {
    const after = snap({ domains: [["pypi.org", true]] });
    expect(clientChanges(snap({}), after, "c-1")).toEqual([
      "pypi.org: с поддоменами",
    ]);
  });

  it("reports a policy switch instead of domain diffs", () => {
    const after = snap({ policyID: "p-2", domains: [["x.org", false]] });
    expect(clientChanges(snap({}), after, "c-1")).toEqual([
      "Политика: ci-01 → Сборка",
    ]);
  });

  it("is empty when nothing about the client changed", () => {
    expect(clientChanges(snap({}), snap({}), "c-1")).toEqual([]);
    expect(clientChanges(snap({}), snap({}), "c-other")).toEqual([]);
  });

  it("reports disable and deletion", () => {
    expect(clientChanges(snap({}), snap({ enabled: false }), "c-1")).toEqual([
      "Клиент отключён",
    ]);
    const gone = { ...snap({}), clients: [] };
    expect(clientChanges(snap({}), gone, "c-1")).toEqual(["Клиент удалён"]);
  });
});

describe("missingDomains", () => {
  const policy = snap({
    domains: [
      ["example.com", false],
      ["debian.org", true],
    ],
  }).policies[0];
  const top = [
    { host: "example.com", requests: 5, denied: 5 },
    { host: "deb.debian.org", requests: 3, denied: 3 },
    { host: "www.example.com", requests: 2, denied: 2 },
    { host: "pypi.org", requests: 4, denied: 0 },
  ];
  it("keeps only denied hosts the allowlist does not cover", () => {
    expect(missingDomains(top, policy).map((d) => d.host)).toEqual([
      "www.example.com",
    ]);
  });
  it("offers nothing for all-web or missing policies", () => {
    expect(missingDomains(top, { ...policy, mode: "all_web" })).toEqual([]);
    expect(missingDomains(top, null)).toEqual([]);
  });
});

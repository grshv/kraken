import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import { revisionLabel } from "../../src/model";
import { clientChanges } from "../../src/clients/history";
import { missingDomains } from "../../src/clients/missing";

function snap(over: {
  networks?: string[];
  domains?: [string, boolean][];
  access?: "list" | "all_web" | "none";
  policyIDs?: string[];
  enabled?: boolean;
  login?: string;
}): Snapshot {
  return {
    schema_version: 2,
    clients: [
      {
        id: "c-1",
        name: "ci-01",
        source_networks: over.networks ?? ["10.20.0.14/32"],
        enabled: over.enabled ?? true,
        access: over.access ?? "list",
        tls_profile_id: "tls",
        policy_ids: over.policyIDs ?? [],
        destinations: (over.domains ?? [["pypi.org", false]]).map(
          ([domain, include_subdomains]) => ({ domain, include_subdomains }),
        ),
        proxy_username: over.login,
      },
    ],
    policies: [
      {
        id: "p-build",
        name: "Сборка",
        destinations: [{ domain: "debian.org", include_subdomains: true }],
      },
    ],
    tls_profiles: [],
  } as unknown as Snapshot;
}

describe("clientChanges", () => {
  it("describes a new client with its final list", () => {
    expect(clientChanges(null, snap({}), "c-1")).toEqual([
      "Создан клиент, домены: pypi.org",
    ]);
    expect(clientChanges(null, snap({ access: "all_web" }), "c-1")).toEqual([
      "Создан клиент, весь веб",
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

  it("reports an attached policy and the domains it brings", () => {
    const after = snap({ policyIDs: ["p-build"] });
    expect(clientChanges(snap({}), after, "c-1")).toEqual([
      "Подключена политика «Сборка»",
      "Добавлен *.debian.org",
    ]);
    expect(clientChanges(after, snap({}), "c-1")).toEqual([
      "Отключена политика «Сборка»",
      "Удалён *.debian.org",
    ]);
  });

  it("reports an access mode change without list diffs", () => {
    expect(clientChanges(snap({}), snap({ access: "all_web" }), "c-1")).toEqual(
      ["Доступ: весь веб"],
    );
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
  const s = snap({
    domains: [["example.com", false]],
    policyIDs: ["p-build"],
  });
  const top = [
    { host: "example.com", requests: 5, denied: 5 },
    { host: "deb.debian.org", requests: 3, denied: 3 },
    { host: "www.example.com", requests: 2, denied: 2 },
    { host: "pypi.org", requests: 4, denied: 0 },
  ];
  it("keeps only denied hosts the final list does not cover", () => {
    expect(missingDomains(top, s, s.clients[0]).map((d) => d.host)).toEqual([
      "www.example.com",
    ]);
  });
  it("offers nothing for all-web or a missing client", () => {
    const web = snap({ access: "all_web" });
    expect(missingDomains(top, web, web.clients[0])).toEqual([]);
    expect(missingDomains(top, s, null)).toEqual([]);
  });
  it("offers every denied host to a client without access", () => {
    const none = snap({ access: "none" });
    expect(missingDomains(top, none, none.clients[0])).toHaveLength(3);
  });
});

describe("revisionLabel", () => {
  it("shows the sequential number, falling back to the UUID prefix", () => {
    expect(revisionLabel("0f3c9a12-aaaa-bbbb", 41)).toBe("r-41");
    expect(revisionLabel("0f3c9a12-aaaa-bbbb", 0)).toBe("0f3c9a12");
    expect(revisionLabel("short", null)).toBe("short");
  });
});

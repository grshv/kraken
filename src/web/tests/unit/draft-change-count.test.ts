import { describe, expect, it } from "vitest";
import type { Client, Snapshot } from "../../src/model";
import { draftChangeCount } from "../../src/draft/changeCount";

const client = (id: string, description = ""): Client => ({
  id,
  name: id,
  description,
  source_networks: ["10.0.0.1/32"],
  enabled: true,
  access: "list",
  tls_profile_id: "http",
  policy_ids: [],
  destinations: [],
});

const snap = (over: Partial<Snapshot> = {}): Snapshot => ({
  schema_version: 2,
  configuration_mode: "structured",
  expert_config: "",
  settings: { http_port: 3128 } as Snapshot["settings"],
  clients: [client("a"), client("b")],
  policies: [{ id: "p", name: "p", destinations: [] }],
  tls_profiles: [],
  ...over,
});

describe("draftChangeCount", () => {
  it("is zero for an unchanged draft and null without snapshots", () => {
    expect(draftChangeCount(snap(), snap())).toBe(0);
    expect(draftChangeCount(null, snap())).toBeNull();
    expect(draftChangeCount(snap(), undefined)).toBeNull();
  });

  it("counts changed, new and deleted objects once each", () => {
    const draft = snap({
      clients: [client("a", "changed"), client("c")],
      policies: [],
    });
    // a changed, b deleted, c new, p deleted
    expect(draftChangeCount(snap(), draft)).toBe(4);
  });

  it("counts settings and the expert mode as one change each", () => {
    const draft = snap({
      settings: {
        http_port: 3128,
        cache_enabled: true,
      } as Snapshot["settings"],
      configuration_mode: "expert",
      expert_config: "acl x src 10.0.0.1",
    });
    expect(draftChangeCount(snap(), draft)).toBe(2);
  });
});

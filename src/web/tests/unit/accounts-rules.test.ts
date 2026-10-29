import { describe, it, expect } from "vitest";
import {
  canDemote,
  canDisable,
  buildManagerScopeBody,
  type PanelUser,
} from "../../src/accounts/rules";
import { parseNetwork } from "../../src/clients/networks";

function u(
  username: string,
  role: "admin" | "manager",
  enabled: boolean,
): PanelUser {
  return { username, role, enabled };
}

describe("canDemote", () => {
  it("forbids demoting the only enabled admin", () => {
    const users = [u("admin", "admin", true)];
    expect(canDemote(users, "admin")).toBe(false);
  });
  it("allows when another enabled admin exists", () => {
    const users = [u("admin", "admin", true), u("backup", "admin", true)];
    expect(canDemote(users, "admin")).toBe(true);
  });
  it("forbids when other admins are disabled", () => {
    const users = [u("admin", "admin", true), u("backup", "admin", false)];
    expect(canDemote(users, "admin")).toBe(false);
  });
  it("always allows demoting a manager", () => {
    const users = [u("admin", "admin", true), u("anna", "manager", true)];
    expect(canDemote(users, "anna")).toBe(true);
  });
  it("allows demoting an unknown user", () => {
    expect(canDemote([], "nobody")).toBe(true);
  });
});

describe("canDisable", () => {
  it("forbids disabling the only enabled admin", () => {
    const users = [u("admin", "admin", true)];
    expect(canDisable(users, "admin")).toBe(false);
  });
  it("allows when another enabled admin exists", () => {
    const users = [u("admin", "admin", true), u("backup", "admin", true)];
    expect(canDisable(users, "admin")).toBe(true);
  });
  it("always allows disabling a manager", () => {
    const users = [u("admin", "admin", true), u("anna", "manager", true)];
    expect(canDisable(users, "anna")).toBe(true);
  });
  it("allows disabling an already-disabled admin", () => {
    const users = [u("admin", "admin", false), u("backup", "admin", true)];
    expect(canDisable(users, "admin")).toBe(true);
  });
});

describe("buildManagerScopeBody", () => {
  it("normalizes CIDRs and dedupes", () => {
    const body = buildManagerScopeBody(
      ["10.20.0.0/22", " 10.20.0.0/22 ", "10.30.0.5"],
      ["c-1", "c-2", "c-1"],
      parseNetwork,
    );
    expect(body.networks).toEqual(["10.20.0.0/22", "10.30.0.5/32"]);
    expect(body.client_ids).toEqual(["c-1", "c-2"]);
  });
  it("drops invalid CIDRs", () => {
    const body = buildManagerScopeBody(
      ["10.0.0.0/8", "not-a-network", ""],
      ["c-1", ""],
      parseNetwork,
    );
    expect(body.networks).toEqual(["10.0.0.0/8"]);
    expect(body.client_ids).toEqual(["c-1"]);
  });
  it("empty inputs produce empty arrays, not null", () => {
    const body = buildManagerScopeBody([], [], parseNetwork);
    expect(body).toEqual({ networks: [], client_ids: [] });
  });
});

import { describe, expect, it } from "vitest";
import { paletteIntent } from "../../src/shell/paletteIntent";

describe("paletteIntent", () => {
  it("resolves nav actions for sections visible to the role", () => {
    expect(paletteIntent("nav:clients", "admin")).toEqual({
      kind: "nav",
      nav: "clients",
    });
    expect(paletteIntent("nav:settings", "admin")).toEqual({
      kind: "nav",
      nav: "settings",
    });
    expect(paletteIntent("nav:help", "manager")).toEqual({
      kind: "nav",
      nav: "help",
    });
  });

  it("returns null for nav to a section the role cannot see", () => {
    expect(paletteIntent("nav:settings", "manager")).toBeNull();
  });

  it("resolves act:new-client to clients + new-client for both roles", () => {
    expect(paletteIntent("act:new-client", "admin")).toEqual({
      kind: "nav",
      nav: "clients",
      intent: "new-client",
    });
    expect(paletteIntent("act:new-client", "manager")).toEqual({
      kind: "nav",
      nav: "clients",
      intent: "new-client",
    });
  });

  it("resolves act:new-login to accounts + new-proxy-login", () => {
    expect(paletteIntent("act:new-login", "manager")).toEqual({
      kind: "nav",
      nav: "accounts",
      intent: "new-proxy-login",
    });
  });

  it("resolves act:block-domain only for admin", () => {
    expect(paletteIntent("act:block-domain", "admin")).toEqual({
      kind: "nav",
      nav: "policies",
      intent: "blocked-domains",
    });
    expect(paletteIntent("act:block-domain", "manager")).toBeNull();
  });

  it("resolves act:review for both roles", () => {
    expect(paletteIntent("act:review", "admin")).toEqual({ kind: "review" });
    expect(paletteIntent("act:review", "manager")).toEqual({ kind: "review" });
  });

  it("returns null for unknown ids", () => {
    expect(paletteIntent("act:unknown", "admin")).toBeNull();
    expect(paletteIntent("nav:nonexistent", "admin")).toBeNull();
    expect(paletteIntent("whatever", "admin")).toBeNull();
    expect(paletteIntent("", "admin")).toBeNull();
  });
});

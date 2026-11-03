import { describe, it, expect } from "vitest";
import { navItems } from "../../src/shell/nav";

describe("navItems", () => {
  it("returns main sections in fixed order for admin", () => {
    const ids = navItems("admin").map((n) => n.id);
    expect(ids.slice(0, 5)).toEqual([
      "clients",
      "policies",
      "log",
      "audit",
      "accounts",
    ]);
  });

  it("puts help and settings in bottom group for admin", () => {
    const items = navItems("admin");
    const bottom = items.filter((n) => n.group === "bottom");
    expect(bottom).toEqual([
      { id: "help", label: "Справка", group: "bottom" },
      { id: "settings", label: "Настройки", group: "bottom" },
    ]);
  });

  it("omits settings for manager", () => {
    const items = navItems("manager");
    expect(items.map((n) => n.id)).toEqual([
      "clients",
      "policies",
      "log",
      "audit",
      "accounts",
      "help",
    ]);
    expect(items.find((n) => n.id === "settings")).toBeUndefined();
  });

  it("uses Russian labels", () => {
    const items = navItems("admin");
    const byId = Object.fromEntries(items.map((n) => [n.id, n.label]));
    expect(byId).toEqual({
      clients: "Клиенты",
      policies: "Политики",
      log: "Журнал",
      audit: "Аудит",
      accounts: "Аккаунты",
      help: "Справка",
      settings: "Настройки",
    });
  });
});

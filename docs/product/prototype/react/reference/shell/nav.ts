// Navigation model for the inspector shell. The list is pure data so role
// gating and ordering can be tested without React.

export type Role = "admin" | "manager";
export type NavId =
  "clients" | "policies" | "log" | "audit" | "accounts" | "help" | "settings";

export type NavItem = {
  id: NavId;
  label: string;
  group: "main" | "bottom";
};

// Main sections appear in this fixed order; bottom holds help and (admin only)
// settings. Manager never sees settings (admin-owned: installation, blocklist,
// expert squid.conf, panel users and assignments).
export function navItems(role: Role): NavItem[] {
  const main: NavItem[] = [
    { id: "clients", label: "Клиенты", group: "main" },
    { id: "policies", label: "Политики", group: "main" },
    { id: "log", label: "Журнал", group: "main" },
    { id: "audit", label: "Аудит", group: "main" },
    { id: "accounts", label: "Аккаунты", group: "main" },
  ];
  const bottom: NavItem[] = [{ id: "help", label: "Справка", group: "bottom" }];
  if (role === "admin") {
    bottom.push({ id: "settings", label: "Настройки", group: "bottom" });
  }
  return [...main, ...bottom];
}

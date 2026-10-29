import type { NavId, Role } from "./nav";
import { navItems } from "./nav";

// Resolves a command-palette action id into a concrete intent the shell can
// execute. Returns null for actions the role may not use (e.g. block-domain
// for manager) or unknown ids, so the palette can hide them.
export type PaletteIntent =
  | {
      kind: "nav";
      nav: NavId;
      intent?: "new-client" | "new-proxy-login" | "blocked-domains";
    }
  | { kind: "review" };

export function paletteIntent(id: string, role: Role): PaletteIntent | null {
  if (id === "act:review") return { kind: "review" };
  if (id === "act:new-client")
    return { kind: "nav", nav: "clients", intent: "new-client" };
  if (id === "act:new-login")
    return { kind: "nav", nav: "accounts", intent: "new-proxy-login" };
  if (id === "act:block-domain" && role === "admin")
    return { kind: "nav", nav: "settings", intent: "blocked-domains" };
  if (id.startsWith("nav:")) {
    const nav = id.slice(4) as NavId;
    if (navItems(role).some((n) => n.id === nav)) return { kind: "nav", nav };
  }
  return null;
}

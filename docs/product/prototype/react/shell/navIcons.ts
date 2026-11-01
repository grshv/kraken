import {
  Activity,
  BookOpen,
  History,
  KeyRound,
  ShieldCheck,
  SlidersHorizontal,
  Users,
} from "lucide-react";
import type { NavId } from "./nav";

// Section icons shared by the rail and the command palette.
export const navIcons: Record<NavId, typeof Users> = {
  clients: Users,
  policies: ShieldCheck,
  log: Activity,
  audit: History,
  accounts: KeyRound,
  help: BookOpen,
  settings: SlidersHorizontal,
};

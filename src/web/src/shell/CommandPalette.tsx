import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command";
import { Ban, Eye, KeyRound, Plus, Users } from "lucide-react";
import type { Client } from "../model";
import type { NavId } from "./nav";
import { navIcons } from "./navIcons";
import { navItems } from "./nav";
import type { Role } from "./nav";
import { paletteIntent } from "./paletteIntent";

type Action = {
  id: string;
  label: string;
  group: string;
  icon: typeof Plus;
  // Extra text matched by the filter (client networks).
  keywords?: string;
  // hint is a plain right-aligned description (e.g. "Создать источник доступа");
  // shortcut is a key cap (e.g. "⌘↵") rendered with CommandShortcut's tracking.
  hint?: string;
  shortcut?: string;
  run: () => void;
};

// ⌘K / Ctrl+K command palette. Actions: navigation to every visible section,
// plus new client, block domain for all, review & apply, and new proxy login.
// Actions whose paletteIntent is null for the role (e.g. block-domain for
// manager) are hidden, so the palette never offers a dead action.
export function CommandPalette({
  role,
  open,
  onOpenChange,
  onNavigate,
  onAction,
  clients,
  onOpenClient,
}: {
  role: Role;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onNavigate: (id: NavId) => void;
  onAction: (id: string) => void;
  clients: Client[];
  onOpenClient: (id: string) => void;
}) {
  const [query, setQuery] = useState("");

  const actions = useMemo<Action[]>(() => {
    const nav: Action[] = navItems(role).map((n) => ({
      id: `nav:${n.id}`,
      label: n.label,
      group: "Разделы",
      icon: navIcons[n.id],
      run: () => onNavigate(n.id),
    }));
    const stubs: Action[] = [
      {
        id: "act:new-client",
        label: "Новый клиент",
        group: "Действия",
        icon: Plus,
        hint: "Создать источник доступа",
        run: () => onAction("act:new-client"),
      },
      {
        id: "act:block-domain",
        label: "Запретить домен для всех",
        group: "Действия",
        icon: Ban,
        run: () => onAction("act:block-domain"),
      },
      {
        id: "act:review",
        label: "Проверить и применить черновик",
        group: "Действия",
        icon: Eye,
        shortcut: "⌘↵",
        run: () => onAction("act:review"),
      },
      {
        id: "act:new-login",
        label: "Новый логин прокси",
        group: "Действия",
        icon: KeyRound,
        run: () => onAction("act:new-login"),
      },
    ];
    const people: Action[] = clients.map((c) => ({
      id: `client:${c.id}`,
      label: `${c.name} · ${c.source_networks[0] ?? ""}`,
      keywords: c.source_networks.join(" "),
      group: "Клиенты",
      icon: Users,
      run: () => onOpenClient(c.id),
    }));
    return [
      ...stubs.filter((a) => paletteIntent(a.id, role) !== null),
      ...people,
      ...nav.filter((a) => paletteIntent(a.id, role) !== null),
    ];
  }, [role, onNavigate, onAction, clients, onOpenClient]);

  // Reset the query when the palette closes so the next open starts fresh.
  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  const groups = Array.from(new Set(actions.map((a) => a.group)));

  function choose(a: Action) {
    onOpenChange(false);
    a.run();
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        className="top-20 translate-y-0 w-[min(560px,92%)] max-w-none p-0 sm:max-w-none"
      >
        <DialogHeader className="sr-only">
          <DialogTitle>Команды</DialogTitle>
          <DialogDescription>Разделы и действия</DialogDescription>
        </DialogHeader>
        <Command loop className="bg-transparent">
          <CommandInput
            value={query}
            onValueChange={setQuery}
            placeholder="Клиент, IP, домен или действие…"
            aria-label="Команды"
          />
          <CommandList className="max-h-[420px]">
            <CommandEmpty>Ничего не найдено</CommandEmpty>
            {groups.map((g) => (
              <CommandGroup key={g} heading={g}>
                {actions
                  .filter((a) => a.group === g)
                  .map((a) => (
                    <CommandItem
                      key={a.id}
                      value={a.id}
                      keywords={[a.label, a.keywords ?? ""]}
                      onSelect={() => choose(a)}
                      className="data-[selected=true]:bg-sel data-[selected=true]:text-foreground"
                    >
                      <a.icon aria-hidden className="text-muted-foreground" />
                      <span className="grow">{a.label}</span>
                      {a.hint && (
                        <span className="ml-auto text-sm text-muted-foreground">
                          {a.hint}
                        </span>
                      )}
                      {a.shortcut && (
                        <CommandShortcut>{a.shortcut}</CommandShortcut>
                      )}
                    </CommandItem>
                  ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

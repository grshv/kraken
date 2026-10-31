import { useEffect, useMemo, useState } from "react";
import { Ban, Globe, KeyRound, Play, Plus, Shield, Users } from "lucide-react";
import { toast } from "sonner";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Request } from "../api";
import { blockDomainsForAll } from "../draft/edits";
import { defaultFilters, logIntent, toQuery } from "../log/filters";
import type { DomainsReport } from "../log/data";
import type { Snapshot } from "../model";
import type { CommitResult, SectionIntent } from "../sections/sectionProps";
import type { NavId, Role } from "./nav";
import { navItems } from "./nav";
import { navIcons } from "./navIcons";
import { paletteIntent } from "./paletteIntent";

// ⌘K / Ctrl+K command palette from the prototype: actions, clients, then —
// once something is typed — domains from the journal and the lists (open them
// in the journal) and "Запретить для всех" for each (admin), policies and
// sections. Actions whose paletteIntent is null for the role are hidden.
export function CommandPalette({
  role,
  open,
  onOpenChange,
  onNavigate,
  onAction,
  snapshot,
  request,
  onOpen,
  onCommit,
}: {
  role: Role;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onNavigate: (id: NavId) => void;
  onAction: (id: string) => void;
  // The draft: clients, policies and domain lists to search.
  snapshot: Snapshot | null;
  request: Request;
  onOpen?: (nav: NavId, intent: SectionIntent) => void;
  onCommit?: (next: Snapshot) => Promise<CommitResult>;
}) {
  const [query, setQuery] = useState("");
  const [journal, setJournal] = useState<DomainsReport | null>(null);
  // Domains seen in the journal over the last 24 h, fetched once per opening.
  useEffect(() => {
    if (!open) {
      setQuery("");
      return;
    }
    let live = true;
    const q = toQuery({ ...defaultFilters(), period: "24 ч" }, new Date());
    request<DomainsReport>(`/events/domains?${q}`)
      .then((r) => live && setJournal(r))
      .catch(() => live && setJournal(null));
    return () => {
      live = false;
    };
  }, [open, request]);

  const run = (fn: () => void) => {
    onOpenChange(false);
    fn();
  };
  const clients = snapshot?.clients ?? [];
  const policies = snapshot?.policies ?? [];
  const blocked = snapshot?.settings?.blocked_domains ?? [];
  const canBlock = paletteIntent("act:block-domain", role) !== null;
  const hosts = useMemo(() => {
    const who = new Map<string, Set<string>>();
    for (const d of journal?.items ?? [])
      who.set(
        d.host,
        new Set(
          d.sources.map((s) =>
            s.client_id ? (s.client_name ?? s.client_id) : "не описан",
          ),
        ),
      );
    const all = new Set<string>(who.keys());
    for (const p of policies) for (const d of p.destinations) all.add(d.domain);
    for (const c of clients) for (const d of c.destinations) all.add(d.domain);
    if (canBlock) for (const d of blocked) all.add(d.domain);
    return [...all]
      .sort()
      .map((host) => ({ host, who: [...(who.get(host) ?? [])] }));
  }, [journal, policies, clients, blocked, canBlock]);

  const actions = [
    { id: "act:new-client", label: "Новый клиент", Icon: Plus },
    { id: "act:new-login", label: "Новый логин прокси", Icon: KeyRound },
    { id: "act:block-domain", label: "Запретить домен для всех", Icon: Ban },
    { id: "act:review", label: "Проверить и применить черновик", Icon: Play },
  ].filter((a) => paletteIntent(a.id, role) !== null);

  const block = async (host: string) => {
    if (!snapshot || !onCommit) return;
    const res = await onCommit(blockDomainsForAll(snapshot, [host]));
    if (res.ok) toast(`${host} запрещён для всех в черновике`);
    else if (res.code !== "revision_conflict")
      toast(res.message ?? "Не удалось изменить черновик");
  };
  const typed = query.trim() !== "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="p-0" showCloseButton={false}>
        <DialogHeader className="sr-only">
          <DialogTitle>Поиск и действия</DialogTitle>
          <DialogDescription>
            Поиск клиентов, адресов, доменов, политик и разделов
          </DialogDescription>
        </DialogHeader>
        <Command loop>
          <CommandInput
            placeholder="Клиент, IP, домен или действие…"
            aria-label="Поиск и действия"
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            <CommandEmpty>Ничего не найдено</CommandEmpty>
            <CommandGroup heading="Действия">
              {actions.map((a) => (
                <CommandItem
                  key={a.id}
                  value={a.label}
                  onSelect={() => run(() => onAction(a.id))}
                >
                  <a.Icon />
                  {a.label}
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandGroup heading="Клиенты">
              {clients.map((c) => (
                <CommandItem
                  key={c.id}
                  value={`Клиент ${c.name} ${c.source_networks.join(" ")}`}
                  onSelect={() =>
                    run(() => onOpen?.("clients", `client:${c.id}`))
                  }
                >
                  <Users />
                  {c.name}
                  <span className="ml-auto text-muted-foreground">
                    {c.source_networks[0]}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
            {typed && (
              <CommandGroup heading="Домены">
                {hosts.map(({ host, who }) => (
                  <CommandItem
                    key={host}
                    value={`Домен ${host}`}
                    onSelect={() =>
                      run(() =>
                        onOpen?.(
                          "log",
                          logIntent({
                            view: "domains",
                            filters: {
                              ...defaultFilters(),
                              period: "24 ч",
                              host,
                            },
                          }),
                        ),
                      )
                    }
                  >
                    <Globe />
                    {host}
                    <span className="ml-auto truncate text-sm text-muted-foreground">
                      {who.length ? who.join(", ") : "нет запросов"} · в журнале
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {typed && canBlock && onCommit && (
              <CommandGroup heading="Запретить для всех">
                {hosts
                  .filter(({ host }) => !blocked.some((b) => b.domain === host))
                  .map(({ host }) => (
                    <CommandItem
                      key={host}
                      value={`Запретить для всех ${host}`}
                      onSelect={() => run(() => void block(host))}
                    >
                      {host}
                      <span className="ml-auto text-sm text-muted-foreground">
                        в черновик
                      </span>
                    </CommandItem>
                  ))}
              </CommandGroup>
            )}
            <CommandGroup heading="Политики">
              {policies.map((p) => (
                <CommandItem
                  key={p.id}
                  value={`Политика ${p.name}`}
                  onSelect={() =>
                    run(() => onOpen?.("policies", `policy:${p.id}`))
                  }
                >
                  <Shield />
                  {p.name}
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandGroup heading="Разделы">
              {navItems(role).map((n) => {
                const Icon = navIcons[n.id];
                return (
                  <CommandItem
                    key={n.id}
                    value={`Раздел ${n.label}`}
                    onSelect={() => run(() => onNavigate(n.id))}
                  >
                    <Icon />
                    {n.label}
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

import { useEffect, useId, useMemo, useState } from "react";
import {
  Ban,
  Copy,
  Eye,
  Globe,
  MoreHorizontal,
  Plus,
  Power,
  Search,
  ShieldCheck,
  Trash2,
  Users,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import {
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListGroupLabel,
  ListRow,
  InspectorPane,
  InspectorHeader,
  InspectorBody,
} from "@/components/kraken/layout";
import {
  Callout,
  Chip,
  Field,
  Kpi,
  KpiGroup,
  KeyValue,
} from "@/components/kraken/blocks";
import { cn } from "cn";
import { EmptyState } from "../shell/EmptyState";
import { ConfirmDialog, type ConfirmOptions } from "../shell/ConfirmDialog";
import { DomainEditor, type DomainHits } from "../policies/DomainEditor";
import {
  managerScope,
  type CommitResult,
  type SectionProps,
} from "../sections/sectionProps";
import { entityMarks, type Mark } from "../draft/marks";
import { parseNetwork, networksOverlap, networkWithin } from "./networks";
import {
  addDomainToClientPolicy,
  createClientFromIP,
  deleteClient,
  newEntityID,
  setClientEnabled,
  setClientNetworks,
} from "../draft/edits";
import type { Client, Event, Policy, Session, Snapshot } from "../model";
import { pluralRu } from "../model";
import type { NavId } from "../shell/nav";

// Inspector shapes (subset of fields the screens render).
type UnknownSource = {
  source_ip: string;
  requests: number;
  denied: number;
  last_seen: string;
  top_destinations: { host: string; requests: number; denied: number }[];
};
type ClientStats = {
  requests: number;
  denied: number;
  bytes: number;
  series: { start: string; requests: number; denied: number; bytes: number }[];
  open_connections: number | null;
  top_destinations: { host: string; requests: number; denied: number }[];
};
type ProxyAccount = {
  username: string;
  owner?: string | null;
  enabled?: boolean;
};
type AuditRow = {
  id: string;
  actor: string;
  action: string;
  entity_id: string;
  timestamp: string;
};

type Tab = "overview" | "access" | "source" | "requests" | "history";

// ClientsSection: list + inspector with five tabs. All edits go through
// onCommit (PUT /configuration/draft with expected_version); 409 toasts and
// reloads; per-field errors surface via ApiError.code returned by onCommit.
export function ClientsSection(props: SectionProps) {
  const {
    request,
    draft,
    active,
    session,
    onCommit,
    onToast,
    onNavigate,
    intent,
    onIntentHandled,
  } = props;
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<"all" | "denied" | "draft">("all");
  const [selected, setSelected] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("overview");
  const [creating, setCreating] = useState<{
    from?: string;
    name: string;
    src: string;
    domains: string[];
  } | null>(null);
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null);
  const [confirmFn, setConfirmFn] = useState<(() => void) | null>(null);
  const [unknown, setUnknown] = useState<UnknownSource[] | null>(null);
  const [stats, setStats] = useState<Record<string, ClientStats>>({});

  const snap = draft?.snapshot;
  const activeSnap = active;
  const clientMarks = useMemo(
    () => entityMarks(activeSnap?.clients ?? [], snap?.clients ?? []),
    [activeSnap, snap],
  );
  const clients = snap?.clients ?? [];

  // Command palette "new-client": open the blank creation form once.
  useEffect(() => {
    if (intent !== "new-client") return;
    setCreating({ name: "", src: "", domains: [] });
    onIntentHandled?.();
  }, [intent, onIntentHandled]);

  // Unknown sources for the list group + "create from IP".
  useEffect(() => {
    let live = true;
    request<{ sources: UnknownSource[] }>("/inspector/unknown-sources")
      .then((r) => {
        if (live) setUnknown(r.sources ?? []);
      })
      .catch(() => {
        if (live) setUnknown([]);
      });
    return () => {
      live = false;
    };
  }, [request]);

  // Per-client stats for the list (denied count, sparkline) and the "с отказами"
  // filter. Bounded to the visible client set.
  useEffect(() => {
    let live = true;
    const ids = clients.map((c) => c.id).slice(0, 50);
    Promise.all(
      ids.map((id) =>
        request<ClientStats>(`/inspector/clients/${id}/stats`)
          .then((s) => [id, s] as const)
          .catch(() => [id, null] as const),
      ),
    ).then((rows) => {
      if (!live) return;
      const next: Record<string, ClientStats> = {};
      for (const [id, s] of rows) {
        if (s) next[id] = s;
      }
      setStats(next);
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request, clients.map((c) => c.id).join(",")]);

  // Select the first client when the section opens with no selection.
  useEffect(() => {
    if (selected === null && clients.length > 0 && !creating) {
      setSelected(clients[0].id);
    }
  }, [selected, clients, creating]);

  const filtered = clients.filter((c) => {
    const q = query.trim().toLowerCase();
    if (q) {
      const inName = c.name.toLowerCase().includes(q);
      const inNet = c.source_networks.some((n) => n.includes(q));
      const pol = snap?.policies.find((p) => p.id === c.policy_id);
      const inDom =
        pol?.destinations.some((d) => d.domain.includes(q)) ?? false;
      if (!inName && !inNet && !inDom) return false;
    }
    if (filter === "denied" && !(stats[c.id]?.denied > 0)) return false;
    if (filter === "draft" && !clientMarks[c.id]) return false;
    return true;
  });

  const selectedClient = clients.find((c) => c.id === selected) ?? null;

  // Empty state: no clients at all (first run). Show unknown sources if any.
  if (clients.length === 0 && !creating) {
    return (
      <div className="flex w-full min-h-0">
        <ListPane>
          <ListPaneHeader
            title="Клиенты"
            action={
              <Button
                size="xs"
                onClick={() => setCreating({ name: "", src: "", domains: [] })}
              >
                <Plus aria-hidden /> Новый
              </Button>
            }
          />
          <ListPaneBody>
            <ListGroupLabel>Неописанные источники</ListGroupLabel>
            {(unknown ?? []).map((u) => (
              <ListRow
                key={u.source_ip}
                title={<span className="font-mono text-sm">{u.source_ip}</span>}
                meta={
                  <span className="text-bad">
                    {pluralRu(u.denied, "отказ", "отказа", "отказов")}
                  </span>
                }
                onClick={() =>
                  setCreating({
                    from: u.source_ip,
                    name: "",
                    src: u.source_ip,
                    domains: u.top_destinations.map((d) => d.host),
                  })
                }
              />
            ))}
            {(!unknown || unknown.length === 0) && (
              <div className="px-3.5 py-3 text-sm text-muted-foreground">
                Клиентов пока нет. Пока их нет, прокси отказывает всем.
              </div>
            )}
          </ListPaneBody>
        </ListPane>
        <InspectorPane>
          <InspectorBody>
            <EmptyState icon={<Users />} title="Добро пожаловать в Kraken">
              Опишите первого клиента, чтобы разрешить ему доступ через прокси.
            </EmptyState>
          </InspectorBody>
        </InspectorPane>
      </div>
    );
  }

  if (creating) {
    return (
      <NewClientForm
        draft={snap!}
        from={creating.from}
        initialName={creating.name}
        initialSrc={creating.src}
        initialDomains={creating.domains}
        onCancel={() => setCreating(null)}
        onSubmit={async (name, src, domains, mode) => {
          const r = createClientFromIP(
            snap!,
            src || creating.from || "",
            name,
            domains,
          );
          if (!r) {
            onToast("Проверьте адрес источника и имя");
            return;
          }
          let next = r.snapshot;
          if (mode === "all") {
            next = {
              ...next,
              policies: next.policies.map((p) =>
                p.id === r.policyID
                  ? { ...p, mode: "all_web", destinations: [] }
                  : p,
              ),
            };
          } else if (mode === "shared") {
            // caller picks a shared policy via the access tab later; keep own.
          }
          const res = await onCommit(next);
          if (res.ok) {
            setSelected(r.clientID);
            setCreating(null);
            setTab("access");
            onToast("Клиент добавлен в черновик");
          } else if (res.code !== "revision_conflict") {
            onToast(res.message ?? "Не удалось сохранить клиента");
          }
        }}
      />
    );
  }

  const commit = async (next: Snapshot): Promise<CommitResult> => {
    const res = await onCommit(next);
    if (!res.ok && res.code !== "revision_conflict") {
      onToast(res.message ?? "Ошибка сохранения");
    }
    return res;
  };

  return (
    <div className="flex w-full min-h-0">
      <ListPane>
        <ListPaneHeader
          title={session.role === "manager" ? "Мои клиенты" : "Клиенты"}
          action={
            <Button
              size="xs"
              onClick={() => setCreating({ name: "", src: "", domains: [] })}
            >
              <Plus aria-hidden /> Новый
            </Button>
          }
        >
          <div className="relative flex items-center">
            <Search
              aria-hidden
              className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
            />
            <Input
              className="h-8 pl-8"
              aria-label="Поиск клиентов"
              placeholder="Имя, IP или домен…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <ToggleGroup
            type="single"
            variant="chip"
            size="xs"
            spacing={1}
            value={filter}
            onValueChange={(v) => {
              if (v) setFilter(v as "all" | "denied" | "draft");
            }}
            className="gap-1.5"
          >
            <ToggleGroupItem value="all">все {clients.length}</ToggleGroupItem>
            <ToggleGroupItem value="denied">с отказами</ToggleGroupItem>
            <ToggleGroupItem value="draft">
              в черновике {clients.filter((c) => clientMarks[c.id]).length}
            </ToggleGroupItem>
          </ToggleGroup>
        </ListPaneHeader>
        <ListPaneBody>
          {filtered.map((c) => {
            const st = stats[c.id];
            return (
              <ListRow
                key={c.id}
                selected={selected === c.id}
                onClick={() => {
                  setSelected(c.id);
                  setTab("overview");
                }}
                title={<SpanMark mark={clientMarks[c.id]} name={c.name} />}
                meta={<Sparkline series={st?.series} />}
                subtitle={
                  <span className="font-mono">
                    {c.source_networks.join(", ")}
                  </span>
                }
                aside={
                  <span className={cn(st?.denied && "text-bad")}>
                    {!c.enabled
                      ? "выкл"
                      : st?.denied
                        ? pluralRu(st.denied, "отказ", "отказа", "отказов")
                        : st
                          ? `${st.requests}`
                          : "—"}
                  </span>
                }
              />
            );
          })}
          {unknown && unknown.length > 0 && (
            <>
              <ListGroupLabel>Неописанные источники</ListGroupLabel>
              {unknown.map((u) => (
                <ListRow
                  key={u.source_ip}
                  title={
                    <span className="font-mono text-sm">{u.source_ip}</span>
                  }
                  meta={
                    <span className="text-bad">
                      {pluralRu(u.denied, "отказ", "отказа", "отказов")}
                    </span>
                  }
                  onClick={() =>
                    setCreating({
                      from: u.source_ip,
                      name: "",
                      src: u.source_ip,
                      domains: u.top_destinations.map((d) => d.host),
                    })
                  }
                />
              ))}
            </>
          )}
          {filtered.length === 0 && (
            <div className="px-3.5 py-3 text-sm text-muted-foreground">
              Ничего не найдено
            </div>
          )}
        </ListPaneBody>
      </ListPane>
      <InspectorPane>
        {selectedClient ? (
          <ClientDetail
            client={selectedClient}
            tab={tab}
            onTab={setTab}
            snap={snap!}
            stats={stats[selectedClient.id]}
            marks={clientMarks}
            request={request}
            session={session}
            onCommit={commit}
            onToast={onToast}
            onNavigate={onNavigate}
            onConfirm={setConfirm}
            setConfirmFn={setConfirmFn}
            onDelete={async () => {
              const res = await onCommit(
                deleteClient(snap!, selectedClient.id),
              );
              if (res.ok) {
                setSelected(null);
                onToast("Клиент удалён из черновика");
              }
            }}
            onToggleEnabled={async () => {
              await onCommit(
                setClientEnabled(
                  snap!,
                  selectedClient.id,
                  !selectedClient.enabled,
                ),
              );
            }}
            onDuplicate={() => {
              setCreating({
                name: selectedClient.name + "-копия",
                src: selectedClient.source_networks[0] ?? "",
                domains: (
                  snap!.policies.find((p) => p.id === selectedClient.policy_id)
                    ?.destinations ?? []
                ).map((d) => (d.include_subdomains ? "*." : "") + d.domain),
              });
            }}
          />
        ) : (
          <InspectorBody>
            <EmptyState icon={<Users />} title="Выберите клиента">
              Выберите клиента слева или создайте нового.
            </EmptyState>
          </InspectorBody>
        )}
      </InspectorPane>
      <ConfirmDialog
        options={confirm}
        onConfirm={() => {
          setConfirm(null);
          confirmFn?.();
          setConfirmFn(null);
        }}
        onCancel={() => {
          setConfirm(null);
          setConfirmFn(null);
        }}
      />
    </div>
  );
}

function SpanMark({ mark, name }: { mark?: Mark; name: string }) {
  return (
    <span className="truncate font-medium">
      {name}
      {mark === "new" && <Badge variant="add">новый</Badge>}
      {mark === "changed" && <Badge variant="mod">изменён</Badge>}
      {mark === "deleted" && <Badge variant="del">удалён</Badge>}
    </span>
  );
}

function Sparkline({
  series,
}: {
  series?: { requests: number; denied: number }[];
}) {
  if (!series || series.length === 0)
    return <svg className="block" width={52} height={16} />;
  const max = Math.max(1, ...series.map((s) => s.requests));
  const w = 52;
  const h = 16;
  const step = w / (series.length - 1 || 1);
  const pts = series
    .map((s, i) => `${i * step},${h - (s.requests / max) * h}`)
    .join(" ");
  return (
    <svg className="block" width={w} height={h} aria-hidden>
      <polyline
        points={pts}
        fill="none"
        stroke="currentColor"
        strokeWidth={1}
      />
    </svg>
  );
}

// Radio option card: RadioGroupItem inside a label styled like .opt.
function RadioCard({
  value,
  active,
  title,
  hint,
  disabled,
}: {
  value: string;
  active: boolean;
  title: string;
  hint?: string;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <label
      htmlFor={id}
      className={cn(
        "grid grid-cols-[18px_minmax(0,1fr)] gap-2.5 rounded-lg border px-3 py-2.5 items-start",
        active
          ? "border-foreground shadow-[0_0_0_1px_var(--foreground)]"
          : "border-border",
        disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
      )}
    >
      <RadioGroupItem
        id={id}
        value={value}
        disabled={disabled}
        className="mt-px"
      />
      <span className="min-w-0">
        <div className="font-medium">{title}</div>
        {hint && <div className="text-sm text-muted-foreground">{hint}</div>}
      </span>
    </label>
  );
}

// ClientDetail renders the tabbed inspector for one client.
function ClientDetail({
  client,
  tab,
  onTab,
  snap,
  stats,
  marks,
  request,
  session,
  onCommit,
  onToast,
  onNavigate,
  onConfirm,
  setConfirmFn,
  onDelete,
  onToggleEnabled,
  onDuplicate,
}: {
  client: Client;
  tab: Tab;
  onTab: (t: Tab) => void;
  snap: Snapshot;
  stats?: ClientStats;
  marks: Record<string, Mark>;
  request: SectionProps["request"];
  session: Session;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
  onConfirm: (o: ConfirmOptions) => void;
  setConfirmFn: (fn: (() => void) | null) => void;
  onDelete: () => Promise<void>;
  onToggleEnabled: () => Promise<void>;
  onDuplicate: () => void;
}) {
  const policy = snap.policies.find((p) => p.id === client.policy_id) ?? null;
  const sharedWith = policy
    ? snap.clients.filter((c) => c.policy_id === policy.id)
    : [];
  const scope = managerScope(session);
  // Manager may not edit a policy referenced by a foreign client.
  const policyEditable =
    !scope || sharedWith.every((c) => scope.clientIDs.includes(c.id));
  const tabs: [Tab, string][] = [
    ["overview", "Обзор"],
    ["access", "Доступ"],
    ["source", "Источник и подключение"],
    ["requests", "Запросы"],
    ["history", "История"],
  ];
  return (
    <>
      <InspectorHeader
        crumbs={
          <Breadcrumb>
            <BreadcrumbList>
              <BreadcrumbItem>
                <BreadcrumbLink asChild>
                  <button type="button" onClick={() => onNavigate("clients")}>
                    Клиенты
                  </button>
                </BreadcrumbLink>
              </BreadcrumbItem>
              <BreadcrumbSeparator>/</BreadcrumbSeparator>
              <BreadcrumbItem>
                <BreadcrumbPage className="truncate">
                  {client.name}
                </BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
        }
        title={client.name}
        status={
          <>
            {marks[client.id] === "new" && (
              <Badge variant="add">новый, в черновике</Badge>
            )}
            {marks[client.id] === "changed" && (
              <Badge variant="mod">изменён</Badge>
            )}
          </>
        }
        actions={
          <>
            <Button size="sm" variant="outline" onClick={onToggleEnabled}>
              <Power aria-hidden /> {client.enabled ? "Отключить" : "Включить"}
            </Button>
            <Button size="sm" variant="outline" onClick={onDuplicate}>
              <Copy aria-hidden /> Дублировать
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Ещё">
                  <MoreHorizontal aria-hidden />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={() => {
                    onConfirm({
                      title: `Удалить ${client.name}?`,
                      text: "Клиент исчезнет из правил после применения. История запросов и аудит сохранятся.",
                      ok: "Удалить",
                      danger: true,
                    });
                    setConfirmFn(onDelete);
                  }}
                >
                  <Trash2 aria-hidden /> Удалить клиента
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
        meta={
          <>
            <span>{kindLabel(client.kind)}</span>·
            {client.description && (
              <>
                <span>{client.description}</span>·
              </>
            )}
            <span className="font-mono text-sm">
              {client.source_networks.join(", ")}
            </span>
            ·<span>{connectionLabel(client)}</span>·
            <PolicyBadge
              client={client}
              policy={policy}
              sharedCount={sharedWith.length}
            />
          </>
        }
      />
      <Tabs
        value={tab}
        onValueChange={(v) => onTab(v as Tab)}
        className="gap-0"
      >
        <TabsList
          variant="line"
          className="group-data-[orientation=horizontal]/tabs:h-auto h-auto w-full p-0 justify-start gap-[18px] border-b px-5"
        >
          {tabs.map(([k, l]) => (
            <TabsTrigger
              key={k}
              value={k}
              className="flex-none px-0 py-[9px] text-base after:bg-accent group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
            >
              {l}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <InspectorBody>
        {tab === "overview" && (
          <OverviewTab
            client={client}
            policy={policy}
            stats={stats}
            snap={snap}
            onCommit={onCommit}
            onToast={onToast}
            onNavigate={onNavigate}
            onTab={onTab}
          />
        )}
        {tab === "access" && (
          <AccessTab
            client={client}
            policy={policy}
            snap={snap}
            sharedWith={sharedWith}
            policyEditable={policyEditable}
            stats={stats}
            request={request}
            onCommit={onCommit}
            onToast={onToast}
            onNavigate={onNavigate}
          />
        )}
        {tab === "source" && (
          <SourceTab
            client={client}
            snap={snap}
            request={request}
            session={session}
            onCommit={onCommit}
            onToast={onToast}
          />
        )}
        {tab === "requests" && (
          <RequestsTab
            client={client}
            request={request}
            onNavigate={onNavigate}
          />
        )}
        {tab === "history" && <HistoryTab client={client} request={request} />}
      </InspectorBody>
    </>
  );
}

function kindLabel(kind?: string): string {
  if (kind === "network") return "Сеть";
  if (kind === "group") return "Группа";
  return "Устройство";
}

// The model has no transport field; a proxy login implies TLS in the prototype.
function connectionLabel(client: Client): string {
  if (client.proxy_username) return `TLS, логин ${client.proxy_username}`;
  return "HTTP без логина";
}

function PolicyBadge({
  client,
  policy,
  sharedCount,
}: {
  client: Client;
  policy: Policy | null;
  sharedCount: number;
}) {
  if (!client.enabled)
    return (
      <Badge variant="secondary">
        <Ban className="size-3.5" /> выключен
      </Badge>
    );
  if (!policy) return <Badge variant="secondary">нет доступа</Badge>;
  if (policy.mode === "all_web")
    return (
      <Badge variant="secondary">
        <Globe className="size-3.5" /> Весь веб
      </Badge>
    );
  return (
    <Badge variant="secondary">
      <ShieldCheck className="size-3.5" />
      {sharedCount > 1 ? `${policy.name} (общая)` : "своя политика"} ·{" "}
      {pluralRu(policy.destinations.length, "домен", "домена", "доменов")}
    </Badge>
  );
}

function OverviewTab({
  client,
  policy,
  stats,
  snap,
  onCommit,
  onToast,
  onNavigate,
  onTab,
}: {
  client: Client;
  policy: Policy | null;
  stats?: ClientStats;
  snap: Snapshot;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
  onTab: (t: Tab) => void;
}) {
  const deniedTop = (stats?.top_destinations ?? []).filter((d) => d.denied > 0);
  const top3 = deniedTop.slice(0, 3);
  const rest = deniedTop.length - top3.length;
  return (
    <>
      {top3.map((d) => (
        <Callout
          key={d.host}
          tone="bad"
          icon={<Ban aria-hidden />}
          actions={
            <Button
              size="xs"
              onClick={async () => {
                const res = await onCommit(
                  addDomainToClientPolicy(snap, client.id, d.host),
                );
                if (res.ok) onToast(`Домен ${d.host} добавлен в политику`);
              }}
            >
              <Plus aria-hidden /> Разрешить {d.host}
            </Button>
          }
        >
          <div className="font-medium">
            {pluralRu(d.denied, "отказ", "отказа", "отказов")} к{" "}
            <span className="font-mono text-sm">{d.host}</span> за 24 ч
          </div>
          <div className="text-sm text-muted-foreground">
            Домена нет в политике клиента.
          </div>
        </Callout>
      ))}
      {rest > 0 && (
        <div className="flex justify-end">
          <Button size="xs" variant="link" onClick={() => onTab("requests")}>
            ещё {rest}
          </Button>
        </div>
      )}
      <KpiGroup>
        <Kpi
          label="Запросы, 24 ч"
          value={stats ? String(stats.requests) : "—"}
        />
        <Kpi
          label="Отказы, 24 ч"
          value={stats ? String(stats.denied) : "—"}
          tone={stats?.denied ? "bad" : undefined}
        />
        <Kpi
          label="Трафик, 24 ч"
          value={stats ? formatBytes(stats.bytes) : "—"}
        />
        <Kpi
          label="Открытые соединения"
          value={
            stats?.open_connections != null
              ? String(stats.open_connections)
              : "—"
          }
        />
      </KpiGroup>
      <div className="grid grid-cols-1 gap-3 @[960px]:grid-cols-[minmax(0,1.65fr)_minmax(300px,1fr)]">
        <div className="flex flex-col gap-3">
          <Card>
            <CardHeader>
              <CardTitle>Куда обращается</CardTitle>
            </CardHeader>
            <CardContent>
              <Table>
                <TableBody>
                  {(stats?.top_destinations ?? []).slice(0, 7).map((d) => (
                    <TableRow key={d.host}>
                      <TableCell className="font-mono text-sm">
                        {d.host}
                      </TableCell>
                      <TableCell className="tabular-nums">
                        {d.requests}
                      </TableCell>
                      {d.denied > 0 && (
                        <TableCell>
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={async () => {
                              const res = await onCommit(
                                addDomainToClientPolicy(
                                  snap,
                                  client.id,
                                  d.host,
                                ),
                              );
                              if (res.ok) onToast(`Домен ${d.host} добавлен`);
                            }}
                          >
                            Разрешить
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </div>
        <div className="flex flex-col gap-3">
          <Card>
            <CardHeader>
              <CardTitle className="flex-1 min-w-0">Правила</CardTitle>
              <CardAction>
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => onNavigate("clients")}
                >
                  Изменить
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent>
              <div className="flex flex-wrap items-center gap-1.5">
                {(policy?.destinations ?? []).slice(0, 8).map((d) => (
                  <Chip key={d.domain}>
                    {d.include_subdomains ? "*." : ""}
                    {d.domain}
                  </Chip>
                ))}
              </div>
              <KeyValue
                className="mt-2.5"
                items={[["Политика", policy ? policy.name : "—"]]}
              />
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}

function AccessTab({
  client,
  policy,
  snap,
  sharedWith,
  policyEditable,
  stats,
  request,
  onCommit,
  onToast,
  onNavigate,
}: {
  client: Client;
  policy: Policy | null;
  snap: Snapshot;
  sharedWith: Client[];
  policyEditable: boolean;
  stats?: ClientStats;
  request: SectionProps["request"];
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
}) {
  const [hits, setHits] = useState<DomainHits>({});
  useEffect(() => {
    if (!policy) return;
    let live = true;
    request<{
      domains: {
        domain: string;
        include_subdomains: boolean;
        hits_24h: number;
        hits_7d: number;
      }[];
    }>(`/inspector/policies/${policy.id}/hits`)
      .then((r) => {
        if (!live) return;
        const next: DomainHits = {};
        for (const d of r.domains ?? [])
          next[d.domain] = { hits_24h: d.hits_24h, hits_7d: d.hits_7d };
        setHits(next);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [policy?.id]);
  const deniedTop = (stats?.top_destinations ?? []).filter((d) => d.denied > 0);
  if (!policy) {
    return (
      <Callout tone="info" icon={<Eye aria-hidden />}>
        <div>Клиенту не назначена политика, доступа нет.</div>
      </Callout>
    );
  }
  const others = sharedWith.filter((c) => c.id !== client.id);
  // Derive the radio value from the current policy state.
  const activeMode =
    policy.mode === "all_web"
      ? "all"
      : sharedWith.length > 1
        ? "shared"
        : "own";
  const select = async (v: string) => {
    if (v === activeMode) return;
    if (v === "own") {
      // Switch to a fresh own policy.
      const ownID = newEntityID(snap, "p-");
      const tlsID = snap.tls_profiles[0]?.id ?? "";
      const own: Policy = {
        id: ownID,
        name: client.name,
        mode: "allowlist",
        destinations: policy.destinations,
        tls_profile_id: tlsID,
      };
      await onCommit({
        ...snap,
        policies: [...snap.policies, own],
        clients: snap.clients.map((c) =>
          c.id === client.id ? { ...c, policy_id: ownID } : c,
        ),
      });
    } else if (v === "all") {
      await onCommit({
        ...snap,
        policies: snap.policies.map((p) =>
          p.id === policy.id ? { ...p, mode: "all_web", destinations: [] } : p,
        ),
      });
    }
  };
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Политика</CardTitle>
        </CardHeader>
        <CardContent>
          <RadioGroup
            value={activeMode}
            onValueChange={select}
            className="grid grid-cols-1 gap-2 min-[1180px]:grid-cols-2"
          >
            <RadioCard
              value="own"
              active={activeMode === "own"}
              title="Своя политика"
              hint="Только для этого клиента"
            />
            <RadioCard
              value="shared"
              active={activeMode === "shared"}
              title="Общая политика"
              hint={policy.name}
              disabled
            />
            <RadioCard
              value="all"
              active={activeMode === "all"}
              title="Весь веб"
              hint="Кроме запрещённого для всех"
            />
            <RadioCard
              value="none"
              active={false}
              title="Нет доступа"
              hint="Клиент описан, но всё закрыто"
              disabled
            />
          </RadioGroup>
          {others.length > 0 && (
            <Callout
              tone="warn"
              icon={<ShieldCheck aria-hidden />}
              className="mt-2.5"
            >
              <div className="text-sm">
                Общая политика: правка затронет также{" "}
                {others.map((c) => c.name).join(", ")}.
              </div>
            </Callout>
          )}
        </CardContent>
      </Card>
      {policy.mode === "allowlist" && (
        <Card>
          <CardHeader>
            <CardTitle className="flex-1 min-w-0">Разрешённые домены</CardTitle>
            <CardAction>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => onNavigate("policies")}
              >
                Открыть политику
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {deniedTop.length > 0 && (
              <Callout
                tone="warn"
                icon={<Ban aria-hidden />}
                className="mb-2.5"
              >
                <div className="text-sm">
                  Отказанные домены этого клиента:{" "}
                  {deniedTop.map((d) => (
                    <Button
                      key={d.host}
                      size="xs"
                      variant="outline"
                      onClick={async () => {
                        const res = await onCommit(
                          addDomainToClientPolicy(snap, client.id, d.host),
                        );
                        if (res.ok) onToast(`Домен ${d.host} добавлен`);
                      }}
                    >
                      <Plus aria-hidden /> {d.host} · {d.denied}
                    </Button>
                  ))}
                </div>
              </Callout>
            )}
            <DomainEditor
              destinations={policy.destinations}
              hits={hits}
              readOnly={!policyEditable}
              readOnlyReason="Политика общая ещё с клиентами вне ваших прав. Изменить её может администратор."
              onChange={async (next) => {
                await onCommit({
                  ...snap,
                  policies: snap.policies.map((p) =>
                    p.id === policy.id ? { ...p, destinations: next } : p,
                  ),
                });
              }}
            />
          </CardContent>
        </Card>
      )}
    </>
  );
}

function SourceTab({
  client,
  snap,
  request,
  session,
  onCommit,
  onToast,
}: {
  client: Client;
  snap: Snapshot;
  request: SectionProps["request"];
  session: Session;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
}) {
  const [name, setName] = useState(client.name);
  const [description, setDescription] = useState(client.description ?? "");
  const [kind, setKind] = useState<Client["kind"]>(client.kind ?? "host");
  const [networks, setNetworks] = useState(client.source_networks.join("\n"));
  const [fieldError, setFieldError] = useState("");
  const [accounts, setAccounts] = useState<ProxyAccount[]>([]);
  useEffect(() => {
    let live = true;
    request<{ items: ProxyAccount[] }>("/proxy-accounts")
      .then((r) => {
        if (live) setAccounts(r.items ?? []);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [request]);
  const scope = managerScope(session);
  const others = snap.clients.filter((c) => c.id !== client.id);
  const save = async () => {
    const lines = networks
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const parsed: string[] = [];
    for (const ln of lines) {
      const n = parseNetwork(ln);
      if (!n) {
        setFieldError(`«${ln}» не похоже на IP-адрес или сеть.`);
        return;
      }
      // Overlap with another client (same family) unless distinct proxy logins.
      for (const c of others) {
        if (!c.enabled) continue;
        if (
          client.proxy_username &&
          c.proxy_username &&
          client.proxy_username !== c.proxy_username
        )
          continue;
        if (c.source_networks.some((m) => networksOverlap(n, m))) {
          setFieldError(`${n} пересекается с клиентом ${c.name}.`);
          return;
        }
      }
      // Manager: source must be inside his networks.
      if (scope && !scope.networks.some((net) => networkWithin(n, net))) {
        setFieldError(`${n} вне ваших сетей.`);
        return;
      }
      parsed.push(n);
    }
    if (parsed.length === 0) {
      setFieldError("Укажите хотя бы один адрес или сеть.");
      return;
    }
    setFieldError("");
    let next = setClientNetworks(snap, client.id, parsed);
    next = {
      ...next,
      clients: next.clients.map((c) =>
        c.id === client.id ? { ...c, name, description, kind } : c,
      ),
    };
    const res = await onCommit(next);
    if (!res.ok && res.code === "source_outside_scope")
      setFieldError("Адрес вне ваших сетей.");
    else if (!res.ok && res.code === "source_overlap")
      setFieldError("Адрес пересекается с чужим клиентом.");
    else if (!res.ok && res.code !== "revision_conflict")
      onToast(res.message ?? "Ошибка сохранения");
  };
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="flex-1 min-w-0">Кто это</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-3.5 min-[1180px]:grid-cols-2">
            <Field label="Имя" htmlFor="f-src-name">
              <Input
                id="f-src-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label="Описание" htmlFor="f-src-desc">
              <Input
                id="f-src-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </Field>
            <Field label="Тип" htmlFor="f-src-kind">
              <Select
                value={kind}
                onValueChange={(v) => setKind(v as Client["kind"])}
              >
                <SelectTrigger id="f-src-kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="host">Устройство</SelectItem>
                  <SelectItem value="network">Сеть</SelectItem>
                  <SelectItem value="group">Группа сетей</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Включён">
              <Badge variant={client.enabled ? "default" : "secondary"}>
                {client.enabled
                  ? "правила действуют"
                  : "все запросы получают отказ"}
              </Badge>
            </Field>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex-1 min-w-0">Адреса источника</CardTitle>
        </CardHeader>
        <CardContent>
          <Field
            label="Адреса (по одному в строке)"
            htmlFor="f-src-networks"
            error={fieldError}
          >
            <Textarea
              id="f-src-networks"
              className="font-mono text-sm"
              rows={3}
              value={networks}
              onChange={(e) => setNetworks(e.target.value)}
              placeholder="10.20.0.77 или 10.20.0.0/24"
            />
          </Field>
          <Button size="sm" className="mt-2" onClick={save}>
            Сохранить адреса
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex-1 min-w-0">Подключение к прокси</CardTitle>
        </CardHeader>
        <CardContent>
          <Field
            label="Логин прокси"
            htmlFor="f-src-login"
            help="Логины прокси настраиваются в разделе «Аккаунты». Пароль в политике не хранится."
          >
            <Select
              value={client.proxy_username ?? ""}
              onValueChange={async (v) => {
                const next = {
                  ...snap,
                  clients: snap.clients.map((c) =>
                    c.id === client.id
                      ? { ...c, proxy_username: v || undefined }
                      : c,
                  ),
                };
                const res = await onCommit(next);
                if (!res.ok && res.code === "username_taken")
                  onToast("Логин уже занят");
              }}
            >
              <SelectTrigger id="f-src-login">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">Не требуется</SelectItem>
                {accounts.map((a) => (
                  <SelectItem key={a.username} value={a.username}>
                    {a.username}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </CardContent>
      </Card>
    </>
  );
}

function RequestsTab({
  client,
  request,
  onNavigate,
}: {
  client: Client;
  request: SectionProps["request"];
  onNavigate: (id: NavId) => void;
}) {
  const [items, setItems] = useState<Event[]>([]);
  useEffect(() => {
    let live = true;
    request<{ items: Event[] }>(
      `/events?client_id=${encodeURIComponent(client.id)}&limit=50`,
    )
      .then((r) => {
        if (live) setItems(r.items ?? []);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [request, client.id]);
  return (
    <>
      <div className="flex justify-end">
        <Button size="xs" variant="ghost" onClick={() => onNavigate("log")}>
          Открыть в журнале
        </Button>
      </div>
      <Card>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Время</TableHead>
                <TableHead>Назначение</TableHead>
                <TableHead>Результат</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.slice(0, 20).map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="tabular-nums text-sm text-muted-foreground">
                    {e.timestamp.slice(11, 19)}
                  </TableCell>
                  <TableCell className="font-mono text-sm">
                    {e.destination}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={e.proxy_result.includes("DENIED") ? "bad" : "ok"}
                    >
                      {e.proxy_result}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className="text-sm text-muted-foreground"
                  >
                    Нет запросов
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </>
  );
}

function HistoryTab({
  client,
  request,
}: {
  client: Client;
  request: SectionProps["request"];
}) {
  const [items, setItems] = useState<AuditRow[]>([]);
  useEffect(() => {
    let live = true;
    request<{ items: AuditRow[] }>(
      `/audit?entity_id=${encodeURIComponent(client.id)}&limit=50`,
    )
      .then((r) => {
        if (live) setItems(r.items ?? []);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [request, client.id]);
  return (
    <Card>
      <CardContent>
        {items.map((r) => (
          <div key={r.id} className="flex items-start gap-2 border-b py-2">
            <span className="w-[120px] shrink-0 tabular-nums text-sm text-muted-foreground">
              {r.timestamp.replace("T", " ").slice(0, 19)}
            </span>
            <div className="flex-1 min-w-0">
              <div>{actionLabel(r.action)}</div>
              <div className="text-sm text-muted-foreground">{r.actor}</div>
            </div>
            {r.action.includes("draft") && (
              <Badge variant="mod">в черновике</Badge>
            )}
          </div>
        ))}
        {items.length === 0 && (
          <div className="text-sm text-muted-foreground">Истории нет</div>
        )}
      </CardContent>
    </Card>
  );
}

function actionLabel(action: string): string {
  if (action === "update_draft") return "Изменение в черновике";
  if (action === "discard_draft") return "Сброс черновика";
  if (action === "create_revision") return "Создана ревизия";
  if (action === "login") return "Вход в панель";
  return action;
}

function NewClientForm({
  draft,
  from,
  initialName,
  initialSrc,
  initialDomains,
  onCancel,
  onSubmit,
}: {
  draft: Snapshot;
  from?: string;
  initialName: string;
  initialSrc: string;
  initialDomains: string[];
  onCancel: () => void;
  onSubmit: (
    name: string,
    src: string,
    domains: string[],
    mode: "own" | "shared" | "all" | "none",
  ) => Promise<void>;
}) {
  const [name, setName] = useState(initialName);
  const [src, setSrc] = useState(initialSrc);
  const [domains, setDomains] = useState(initialDomains.join("\n"));
  const [mode, setMode] = useState<"own" | "shared" | "all" | "none">("own");
  const [err, setErr] = useState("");
  return (
    <InspectorPane>
      <InspectorHeader
        title="Новый клиент"
        status={
          from ? (
            <Badge variant="warn">из неописанного источника {from}</Badge>
          ) : undefined
        }
        actions={
          <Button size="xs" variant="ghost" onClick={onCancel}>
            <X aria-hidden /> Отмена
          </Button>
        }
        meta="Клиент попадёт в черновик. Трафик не изменится, пока вы не примените изменения."
      />
      <InspectorBody>
        <Card>
          <CardHeader>
            <CardTitle>Кто</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-1 gap-3.5 min-[1180px]:grid-cols-2">
              <Field label="Имя" htmlFor="f-nc-name">
                <Input
                  id="f-nc-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="например, build-02"
                />
              </Field>
              <Field label="Тип" htmlFor="f-nc-kind">
                <Select defaultValue="host">
                  <SelectTrigger id="f-nc-kind">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="host">Устройство</SelectItem>
                    <SelectItem value="network">Сеть</SelectItem>
                    <SelectItem value="group">Группа сетей</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Адреса источника"
                htmlFor="f-nc-src"
                className="col-span-full"
              >
                <Textarea
                  id="f-nc-src"
                  className="font-mono text-sm"
                  rows={2}
                  value={src}
                  onChange={(e) => setSrc(e.target.value)}
                  placeholder="10.20.0.77 или 10.20.0.0/24, по одному в строке"
                />
              </Field>
              {from && (
                <div className="col-span-full text-sm text-muted-foreground">
                  За 24 ч с {from} были запросы, все отказаны (источник не
                  описан).
                </div>
              )}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Куда можно</CardTitle>
          </CardHeader>
          <CardContent>
            <RadioGroup
              value={mode}
              onValueChange={(v) =>
                setMode(v as "own" | "shared" | "all" | "none")
              }
              className="grid grid-cols-1 gap-2 min-[1180px]:grid-cols-2"
            >
              <RadioCard
                value="own"
                active={mode === "own"}
                title="Свой список доменов"
                hint="Только перечисленные сайты"
              />
              <RadioCard
                value="all"
                active={mode === "all"}
                title="Весь веб"
                hint="Кроме запрещённого для всех"
              />
              <RadioCard
                value="none"
                active={mode === "none"}
                title="Пока без доступа"
                hint="Описать и решить позже"
              />
            </RadioGroup>
            {mode === "own" && (
              <Field label="Домены" htmlFor="f-nc-domains" className="mt-2.5">
                <Textarea
                  id="f-nc-domains"
                  className="font-mono text-sm"
                  rows={3}
                  value={domains}
                  onChange={(e) => setDomains(e.target.value)}
                />
              </Field>
            )}
          </CardContent>
        </Card>
        <div className="flex justify-end gap-2">
          {err && <span className="text-sm text-bad">{err}</span>}
          <Button variant="ghost" onClick={onCancel}>
            Отмена
          </Button>
          <Button
            onClick={async () => {
              if (!name.trim()) {
                setErr("Укажите имя клиента.");
                return;
              }
              const dom = domains
                .split(/[\s,]+/)
                .map((s) => s.trim())
                .filter(Boolean);
              await onSubmit(name.trim(), src, dom, mode);
            }}
          >
            <Plus aria-hidden /> Добавить в черновик
          </Button>
        </div>
      </InspectorBody>
    </InspectorPane>
  );
}

function formatBytes(n: number): string {
  if (!n) return "0";
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

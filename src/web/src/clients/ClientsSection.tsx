import { useEffect, useId, useMemo, useState } from "react";
import {
  Ban,
  Check,
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
  CardDescription,
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
import { TemplateButtons } from "../policies/PoliciesSection";
import {
  managerScope,
  type CommitResult,
  type SectionProps,
} from "../sections/sectionProps";
import { entityMarks, type Mark } from "../draft/marks";
import { parseNetwork, networksOverlap, networkWithin } from "./networks";
import { Sparkline, TrafficChart, type Bucket } from "./TrafficChart";
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
  series: Bucket[];
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

  const createForm = creating && snap && (
      <NewClientForm
        key={creating.from ?? creating.name}
        draft={snap!}
        request={request}
        from={creating.from}
        fromSource={unknown?.find((u) => u.source_ip === creating.from)}
        initialName={creating.name}
        initialSrc={creating.src}
        initialDomains={creating.domains}
        onCancel={() => setCreating(null)}
        onSubmit={async (input) => {
          const r = createClientFromIP(
            snap!,
            input.networks[0],
            input.name,
            input.domains,
          );
          if (!r) {
            onToast("Проверьте адрес источника и имя");
            return;
          }
          let next = r.snapshot;
          const usePolicy =
            input.mode === "shared" ? input.sharedPolicyID : r.policyID;
          next = {
            ...next,
            clients: next.clients.map((c) =>
              c.id === r.clientID
                ? {
                    ...c,
                    kind: input.kind,
                    source_networks: input.networks,
                    policy_id: usePolicy,
                    proxy_username: input.proxyUsername || undefined,
                  }
                : c,
            ),
            policies: next.policies
              .filter((p) => input.mode !== "shared" || p.id !== r.policyID)
              .map((p) =>
                p.id === r.policyID && input.mode === "all"
                  ? { ...p, mode: "all_web" as const, destinations: [] }
                  : p,
              ),
          };
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
            <ToggleGroupItem value="denied">
              с отказами {clients.filter((c) => stats[c.id]?.denied > 0).length}
            </ToggleGroupItem>
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
                selected={!creating && selected === c.id}
                onClick={() => {
                  setCreating(null);
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
                  selected={creating?.from === u.source_ip}
                  title={<span className="font-mono">{u.source_ip}</span>}
                  meta={
                    <span className="text-bad">
                      {pluralRu(u.denied, "отказ", "отказа", "отказов")}
                    </span>
                  }
                  subtitle={u.top_destinations.map((d) => d.host).join(", ")}
                  aside={
                    <span className="text-muted-foreground">
                      {ago(u.last_seen)}
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
      {createForm || (
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
      )}
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
  const [recent, setRecent] = useState<Event[] | null>(null);
  useEffect(() => {
    let live = true;
    setRecent(null);
    request<{ items: Event[] }>(
      `/events?client_id=${encodeURIComponent(client.id)}&limit=5`,
    )
      .then((r) => {
        if (live) setRecent(r.items ?? []);
      })
      .catch(() => {
        if (live) setRecent([]);
      });
    return () => {
      live = false;
    };
  }, [request, client.id]);
  const lastSeen = recent?.[0]?.timestamp;
  const missing = (stats?.top_destinations ?? []).filter((d) => d.denied > 0)
    .length;
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
            {!client.enabled ? (
              <Badge variant="secondary">отключён</Badge>
            ) : lastSeen ? (
              <Badge variant="ok">
                <span className="size-1.5 rounded-full bg-current" />
                последний запрос {ago(lastSeen)}
              </Badge>
            ) : recent ? (
              <Badge variant="secondary">запросов не было</Badge>
            ) : null}
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
              {k === "access" && missing > 0 && (
                <Badge variant="bad" className="px-1.5">
                  {missing}
                </Badge>
              )}
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
            recent={recent}
            snap={snap}
            onCommit={onCommit}
            onToast={onToast}
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

// ago renders a past timestamp as "сейчас", "N мин", "N ч" or "N д".
function ago(iso: string | null | undefined): string {
  if (!iso) return "—";
  const min = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 60000));
  if (min < 1) return "сейчас";
  if (min < 60) return `${min} мин`;
  if (min < 48 * 60) return `${Math.round(min / 60)} ч`;
  return `${Math.round(min / 1440)} д`;
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

// OverviewTab follows the prototype: a callout per denied domain missing from
// the policy, KPIs, then traffic and top destinations on the left and rules
// and recent requests on the right.
function OverviewTab({
  client,
  policy,
  stats,
  recent,
  snap,
  onCommit,
  onToast,
  onTab,
}: {
  client: Client;
  policy: Policy | null;
  stats?: ClientStats;
  recent: Event[] | null;
  snap: Snapshot;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onTab: (t: Tab) => void;
}) {
  const top = stats?.top_destinations ?? [];
  const deniedTop = top.filter((d) => d.denied > 0);
  const shown = deniedTop.slice(0, 3);
  const rest = deniedTop.length - shown.length;
  const policyName = policy
    ? policy.name
    : "";
  const shared = policy
    ? snap.clients.filter((c) => c.policy_id === policy.id).length
    : 0;
  const blocked = snap.settings?.blocked_domains?.length ?? 0;
  const allow = async (host: string) => {
    const res = await onCommit(addDomainToClientPolicy(snap, client.id, host));
    if (res.ok) onToast(`Домен ${host} добавлен в политику`);
  };
  const max = Math.max(1, ...top.map((d) => d.requests));
  return (
    <>
      {shown.map((d) => (
        <Callout
          key={d.host}
          tone="bad"
          icon={<Ban aria-hidden />}
          actions={
            <Button size="xs" onClick={() => allow(d.host)}>
              <Plus aria-hidden /> Разрешить
            </Button>
          }
        >
          <div className="font-medium">
            {pluralRu(d.denied, "отказ", "отказа", "отказов")} к{" "}
            <span className="font-mono text-sm">{d.host}</span> за 24 ч
          </div>
          <div className="text-sm text-muted-foreground">
            Домена нет в политике «{policyName}»
          </div>
        </Callout>
      ))}
      {rest > 0 && (
        <Button
          size="xs"
          variant="ghost"
          className="self-start"
          onClick={() => onTab("access")}
        >
          Ещё {pluralRu(rest, "домен", "домена", "доменов")} с отказами
        </Button>
      )}
      <KpiGroup>
        <Kpi
          label="Запросы, 24 ч"
          value={stats?.requests ? stats.requests.toLocaleString("ru-RU") : "—"}
        />
        <Kpi
          label="Отказы, 24 ч"
          value={stats?.denied ? stats.denied.toLocaleString("ru-RU") : "—"}
          hint={
            stats?.denied
              ? deniedTop.length
                ? "нет в политике клиента"
                : "запрещено правилами"
              : "нет"
          }
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
      <div className="grid grid-cols-1 items-start gap-3.5 @[900px]:grid-cols-[minmax(0,1.65fr)_minmax(280px,1fr)]">
        <div className="flex min-w-0 flex-col gap-3.5">
          <Card>
            <CardHeader>
              <CardTitle>Трафик за 24 часа</CardTitle>
            </CardHeader>
            <CardContent>
              <TrafficChart series={stats?.series ?? []} />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Куда обращается</CardTitle>
                <CardDescription>
                  Топ назначений за 24 ч, включая отказы
                </CardDescription>
              </div>
            </CardHeader>
            <CardContent>
              {top.length === 0 ? (
                <div className="text-sm text-muted-foreground">
                  Запросов за 24 ч не было
                </div>
              ) : (
                <Table className="table-fixed">
                  <colgroup>
                    <col />
                    <col className="w-[28%]" />
                    <col className="w-[92px]" />
                    <col className="w-[96px]" />
                  </colgroup>
                  <TableBody>
                    {top.slice(0, 7).map((d) => (
                      <TableRow key={d.host}>
                        <TableCell className="pl-0">
                          <div className="truncate font-mono text-sm">
                            {d.host}
                          </div>
                          {d.denied > 0 && (
                            <div className="truncate text-sm text-muted-foreground">
                              нет в политике
                            </div>
                          )}
                        </TableCell>
                        <TableCell>
                          <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                            <div
                              className={cn(
                                "h-full rounded-full",
                                d.denied > 0 ? "bg-s-deny" : "bg-s-allow",
                              )}
                              style={{
                                width: `${Math.max(2, (d.requests / max) * 100)}%`,
                              }}
                            />
                          </div>
                        </TableCell>
                        <TableCell
                          className={cn(
                            "text-right tabular-nums",
                            d.denied > 0 && "font-medium text-bad",
                          )}
                        >
                          {d.denied > 0
                            ? pluralRu(d.denied, "отказ", "отказа", "отказов")
                            : d.requests.toLocaleString("ru-RU")}
                        </TableCell>
                        <TableCell className="pr-0 text-right">
                          {d.denied > 0 && (
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => allow(d.host)}
                            >
                              Разрешить
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </div>
        <div className="flex min-w-0 flex-col gap-3.5">
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Правила</CardTitle>
                <CardDescription>
                  {!policy
                    ? "Нет доступа"
                    : policy.mode === "all_web"
                      ? "Любые внешние сайты, кроме запрещённых для всех"
                      : "Только домены политики"}
                </CardDescription>
              </div>
              <CardAction>
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => onTab("access")}
                >
                  Изменить
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-col gap-2.5">
              {policy?.mode === "allowlist" &&
                policy.destinations.length > 0 && (
                  <div className="flex flex-wrap gap-1.5">
                    {policy.destinations.slice(0, 8).map((d) => (
                      <Chip key={d.domain}>
                        {d.include_subdomains ? "*." : ""}
                        {d.domain}
                      </Chip>
                    ))}
                    {policy.destinations.length > 8 && (
                      <Chip className="text-muted-foreground">
                        +{policy.destinations.length - 8}
                      </Chip>
                    )}
                  </div>
                )}
              <KeyValue
                items={[
                  [
                    "Политика",
                    !policy
                      ? "—"
                      : shared > 1
                        ? `${policy.name}, общая: ${pluralRu(shared, "клиент", "клиента", "клиентов")}`
                        : "своя у клиента",
                  ],
                  [
                    "Поверх неё",
                    `${blocked ? pluralRu(blocked, "запрещённый домен", "запрещённых домена", "запрещённых доменов") + ", " : ""}встроенная защита`,
                  ],
                  ["Подключение", connectionLabel(client)],
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Последние запросы</CardTitle>
              <CardAction>
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => onTab("requests")}
                >
                  Все
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent>
              {recent === null ? null : recent.length === 0 ? (
                <div className="text-sm text-muted-foreground">
                  Запросов не было
                </div>
              ) : (
                <div className="flex flex-col">
                  {recent.map((e) => {
                    const denied = e.proxy_result.includes("DENIED");
                    return (
                      <div
                        key={e.id}
                        className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 border-b py-1.5 last:border-b-0"
                      >
                        <span className="text-sm text-muted-foreground tabular-nums">
                          {new Date(e.timestamp).toLocaleTimeString("ru-RU")}
                        </span>
                        <span
                          className="truncate font-mono text-sm"
                          title={e.destination}
                        >
                          {e.destination}
                        </span>
                        <Badge variant={denied ? "bad" : "ok"}>
                          {e.proxy_http_status ?? (denied ? 403 : 200)}
                        </Badge>
                      </div>
                    );
                  })}
                </div>
              )}
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

export type NewClientInput = {
  name: string;
  kind: "host" | "network" | "group";
  networks: string[];
  domains: string[];
  mode: "own" | "shared" | "all" | "none";
  sharedPolicyID: string;
  proxyUsername: string;
};

// Numbered step marker on the creation form cards.
function Step({ n }: { n: number }) {
  return (
    <span className="grid size-5 place-items-center rounded-md bg-add-bg text-sm font-semibold text-add-fg">
      {n}
    </span>
  );
}

// NewClientForm follows the prototype's three steps: who (name, type,
// source addresses), where to (own list, shared policy, all web or nothing
// yet) and how it connects (no login or a proxy login).
function NewClientForm({
  draft,
  request,
  from,
  fromSource,
  initialName,
  initialSrc,
  initialDomains,
  onCancel,
  onSubmit,
}: {
  draft: Snapshot;
  request: SectionProps["request"];
  from?: string;
  fromSource?: UnknownSource;
  initialName: string;
  initialSrc: string;
  initialDomains: string[];
  onCancel: () => void;
  onSubmit: (input: NewClientInput) => Promise<void>;
}) {
  const [name, setName] = useState(initialName);
  const [kind, setKind] = useState<NewClientInput["kind"]>("host");
  const [src, setSrc] = useState(initialSrc);
  const [domains, setDomains] = useState(initialDomains.join("\n"));
  const [mode, setMode] = useState<NewClientInput["mode"]>("own");
  const [err, setErr] = useState("");
  const [login, setLogin] = useState("");
  const [accounts, setAccounts] = useState<ProxyAccount[]>([]);
  // Shared policies: allowlists already used by at least one client.
  const shared = draft.policies.filter(
    (p) =>
      p.mode === "allowlist" && draft.clients.some((c) => c.policy_id === p.id),
  );
  const [sharedID, setSharedID] = useState(shared[0]?.id ?? "");
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
  const lines = src
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  const bad = lines.filter((l) => !parseNetwork(l));
  const overlap = draft.clients.find((c) =>
    lines.some((l) => {
      const n = parseNetwork(l);
      return n && c.source_networks.some((x) => networksOverlap(x, n));
    }),
  );
  const srcError = bad.length
    ? `Не адрес и не сеть: ${bad.join(", ")}`
    : overlap && !login
      ? `Пересекается с клиентом «${overlap.name}». Для пересечения нужен свой логин прокси.`
      : "";
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
            Отмена
          </Button>
        }
        meta="Клиент попадёт в черновик. Трафик не изменится, пока вы не примените изменения."
      />
      <InspectorBody className="border-t">
        <Card>
          <CardHeader className="flex items-center gap-2">
            <Step n={1} />
            <CardTitle>Кто</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-1 gap-3.5 @[700px]:grid-cols-2">
              <Field label="Имя" htmlFor="f-nc-name">
                <Input
                  id="f-nc-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="например, build-02"
                />
              </Field>
              <Field label="Тип">
                <ToggleGroup
                  type="single"
                  variant="seg"
                  size="sm"
                  spacing={0.5}
                  className="w-full rounded-lg bg-muted p-[3px]"
                  value={kind}
                  aria-label="Тип клиента"
                  onValueChange={(v) => {
                    if (v) setKind(v as NewClientInput["kind"]);
                  }}
                >
                  <ToggleGroupItem value="host">Устройство</ToggleGroupItem>
                  <ToggleGroupItem value="network">Сеть</ToggleGroupItem>
                  <ToggleGroupItem value="group">Группа сетей</ToggleGroupItem>
                </ToggleGroup>
              </Field>
              <Field
                label="Адреса источника"
                htmlFor="f-nc-src"
                className="col-span-full"
                error={src ? srcError : ""}
                help={
                  fromSource ? (
                    <span className="flex items-center gap-1.5 text-foreground">
                      <Check aria-hidden className="size-3.5" />
                      За 24 ч с {from}:{" "}
                      {pluralRu(
                        fromSource.requests,
                        "запрос",
                        "запроса",
                        "запросов",
                      )}
                      , отказано {fromSource.denied} (источник не описан).
                    </span>
                  ) : (
                    "Пересечение с другим клиентом допустимо только при разных логинах прокси. Проверяется при вводе"
                  )
                }
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
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex items-center gap-2">
            <Step n={2} />
            <CardTitle>Куда можно</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <RadioGroup
              value={mode}
              onValueChange={(v) => setMode(v as NewClientInput["mode"])}
              className="grid grid-cols-1 gap-2 @[700px]:grid-cols-2"
            >
              <RadioCard
                value="own"
                active={mode === "own"}
                title="Свой список доменов"
                hint="Только перечисленные сайты"
              />
              <RadioCard
                value="shared"
                active={mode === "shared"}
                title="Общая политика"
                hint={
                  shared.length
                    ? "Как у похожих клиентов"
                    : "Общих политик пока нет"
                }
                disabled={!shared.length}
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
              <Field
                label="Домены"
                htmlFor="f-nc-domains"
                help={
                  <span className="flex flex-wrap items-center gap-1.5">
                    {from && "Подставлены домены из отказанных запросов этого IP. "}
                    Шаблоны:
                    <TemplateButtons
                      request={request}
                      onAdd={(add) =>
                        setDomains((cur) =>
                          [
                            ...new Set([
                              ...cur.split(/\s+/).filter(Boolean),
                              ...add,
                            ]),
                          ].join("\n"),
                        )
                      }
                    />
                  </span>
                }
              >
                <Textarea
                  id="f-nc-domains"
                  className="font-mono text-sm"
                  rows={3}
                  value={domains}
                  onChange={(e) => setDomains(e.target.value)}
                />
              </Field>
            )}
            {mode === "shared" && (
              <Field label="Политика" htmlFor="f-nc-policy">
                <Select value={sharedID} onValueChange={setSharedID}>
                  <SelectTrigger id="f-nc-policy" className="max-w-[320px]">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {shared.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}:{" "}
                        {pluralRu(
                          p.destinations.length,
                          "домен",
                          "домена",
                          "доменов",
                        )}
                        ,{" "}
                        {pluralRu(
                          draft.clients.filter((c) => c.policy_id === p.id)
                            .length,
                          "клиент",
                          "клиента",
                          "клиентов",
                        )}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex items-center gap-2">
            <Step n={3} />
            <CardTitle>Подключение</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <RadioGroup
              value={login ? "login" : "plain"}
              onValueChange={(v) =>
                setLogin(v === "login" ? (accounts[0]?.username ?? "") : "")
              }
              className="grid grid-cols-1 gap-2 @[700px]:grid-cols-2"
            >
              <RadioCard
                value="plain"
                active={!login}
                title="Без логина"
                hint="Клиент узнаётся по адресу источника"
              />
              <RadioCard
                value="login"
                active={!!login}
                title="С логином прокси"
                hint={
                  accounts.length
                    ? "Можно требовать логин и пароль"
                    : "Логины прокси создаются в разделе «Аккаунты»"
                }
                disabled={!accounts.length}
              />
            </RadioGroup>
            {login && (
              <Field label="Логин прокси" htmlFor="f-nc-login">
                <Select value={login} onValueChange={setLogin}>
                  <SelectTrigger id="f-nc-login" className="max-w-[320px]">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {accounts.map((a) => (
                      <SelectItem key={a.username} value={a.username}>
                        {a.username}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </CardContent>
        </Card>
        <div className="flex items-center justify-end gap-2">
          {err && <span className="text-sm text-bad">{err}</span>}
          <Button variant="outline" onClick={onCancel}>
            Отмена
          </Button>
          <Button
            onClick={async () => {
              if (!name.trim()) {
                setErr("Укажите имя клиента.");
                return;
              }
              if (!lines.length || srcError) {
                setErr(srcError || "Укажите адрес источника.");
                return;
              }
              setErr("");
              await onSubmit({
                name: name.trim(),
                kind,
                networks: lines.map((l) => parseNetwork(l)!),
                domains:
                  mode === "own"
                    ? domains.split(/[\s,]+/).filter(Boolean)
                    : [],
                mode,
                sharedPolicyID: sharedID,
                proxyUsername: login,
              });
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

import { useEffect, useId, useMemo, useState } from "react";
import {
  ArrowRight,
  Ban,
  Check,
  Info,
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
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
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
import { clientChanges } from "./history";
import { missingDomains } from "./missing";
import {
  countNotices,
  useClientNotices,
  useSourceNotices,
  type Notice,
  type NoticeStatus,
} from "./notifications";
import { DeniedDomains } from "./DeniedDomains";
import {
  addDomainToClient,
  addDomainsToClient,
  attachPolicy,
  clientTransport,
  createClientFromIP,
  deleteClient,
  detachPolicy,
  effectiveDestinations,
  setClientAccess,
  setClientConnection,
  setClientEnabled,
  setClientNetworks,
  shareClientDomains,
} from "../draft/edits";
import type { Access, Client, Event, Session, Snapshot } from "../model";
import { ago, pluralRu, revisionLabel } from "../model";
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
    onOpen,
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
    access?: Access;
    policyIDs?: string[];
    proxyUsername?: string;
  } | null>(null);
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null);
  const [confirmFn, setConfirmFn] = useState<(() => void) | null>(null);
  const [unknown, setUnknown] = useState<UnknownSource[] | null>(null);
  const [stats, setStats] = useState<Record<string, ClientStats>>({});
  // Ignored undescribed sources leave the list until shown again.
  const sources = useSourceNotices(request);
  const [showIgnored, setShowIgnored] = useState(false);
  const isIgnored = (ip: string) => sources.byIP[ip]?.status === "ignored";
  const ignoredCount = (unknown ?? []).filter((u) =>
    isIgnored(u.source_ip),
  ).length;
  const visibleUnknown = (unknown ?? []).filter(
    (u) => showIgnored || !isIgnored(u.source_ip),
  );

  const snap = draft?.snapshot;
  const activeSnap = active;
  const clientMarks = useMemo(
    () => entityMarks(activeSnap?.clients ?? [], snap?.clients ?? []),
    [activeSnap, snap],
  );
  const clients = snap?.clients ?? [];

  // Command palette "new-client" opens the blank creation form once;
  // "client:<id>" (from another section) selects that client.
  useEffect(() => {
    if (intent === "new-client") {
      setCreating({ name: "", src: "", domains: [] });
      onIntentHandled?.();
    } else if (intent?.startsWith("source:")) {
      const ip = intent.slice("source:".length);
      const u = unknown?.find((x) => x.source_ip === ip);
      setCreating({
        from: ip,
        name: "",
        src: ip,
        domains: u?.top_destinations.map((d) => d.host) ?? [],
      });
      onIntentHandled?.();
    } else if (intent?.startsWith("client:")) {
      setCreating(null);
      setSelected(intent.slice("client:".length));
      setTab("overview");
      onIntentHandled?.();
    }
  }, [intent, onIntentHandled, unknown]);

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
      const inDom =
        !!snap &&
        effectiveDestinations(snap, c).some((e) =>
          e.destination.domain.includes(q),
        );
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
            {visibleUnknown.map((u) => (
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
      sourceStatus={
        creating.from
          ? (sources.byIP[creating.from]?.status ?? "new")
          : undefined
      }
      onMarkSource={async (status) => {
        if (!creating.from) return;
        try {
          await sources.mark(creating.from, status);
        } catch {
          onToast("Не удалось сохранить отметку");
          return;
        }
        if (status === "ignored") setCreating(null);
        onToast(
          status === "ignored"
            ? "Источник скрыт из списка"
            : status === "seen"
              ? "Отмечено как просмотренное"
              : "Отметка снята",
        );
      }}
      initialName={creating.name}
      initialSrc={creating.src}
      initialDomains={creating.domains}
      initialAccess={creating.access}
      initialPolicyIDs={creating.policyIDs}
      initialLogin={creating.proxyUsername}
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
        let next: Snapshot = {
          ...r.snapshot,
          clients: r.snapshot.clients.map((c) =>
            c.id === r.clientID
              ? {
                  ...c,
                  kind: input.kind,
                  source_networks: input.networks,
                  access: input.access,
                  policy_ids: input.policyIDs,
                }
              : c,
          ),
        };
        next = setClientConnection(
          next,
          r.clientID,
          input.proxyUsername || undefined,
        );
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
          {(visibleUnknown.length > 0 || ignoredCount > 0) && (
            <>
              <ListGroupLabel>Неописанные источники</ListGroupLabel>
              {visibleUnknown.map((u) => (
                <ListRow
                  key={u.source_ip}
                  selected={creating?.from === u.source_ip}
                  title={<span className="font-mono">{u.source_ip}</span>}
                  meta={
                    <span
                      className={cn(
                        sources.byIP[u.source_ip]?.status === "new" ||
                          !sources.byIP[u.source_ip]
                          ? "text-bad"
                          : "text-muted-foreground",
                      )}
                    >
                      {pluralRu(u.denied, "отказ", "отказа", "отказов")}
                      {isIgnored(u.source_ip)
                        ? " · игнорируется"
                        : sources.byIP[u.source_ip]?.status === "seen"
                          ? " · просмотрено"
                          : ""}
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
              {ignoredCount > 0 && (
                <button
                  type="button"
                  className="px-3.5 py-2 text-left text-sm text-muted-foreground hover:text-foreground"
                  onClick={() => setShowIgnored((v) => !v)}
                >
                  {showIgnored
                    ? "Скрыть игнорируемые"
                    : `Показать игнорируемые (${ignoredCount})`}
                </button>
              )}
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
              active={activeSnap ?? null}
              tab={tab}
              onTab={setTab}
              snap={snap!}
              stats={stats[selectedClient.id]}
              marks={clientMarks}
              request={request}
              session={session}
              onOpen={onOpen}
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
                  domains: selectedClient.destinations.map(
                    (d) => (d.include_subdomains ? "*." : "") + d.domain,
                  ),
                  access: selectedClient.access,
                  policyIDs: selectedClient.policy_ids,
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
  children,
}: {
  value: string;
  active: boolean;
  title: string;
  hint?: string;
  disabled?: boolean;
  children?: React.ReactNode;
}) {
  const id = useId();
  // Extra controls (a policy picker) sit below the label so clicking them
  // does not toggle the radio.
  return (
    <div
      className={cn(
        "rounded-lg border px-3 py-2.5",
        active ? "border-muted-foreground/45 bg-hover" : "border-border",
        disabled && "opacity-60",
      )}
    >
      <label
        htmlFor={id}
        className={cn(
          "grid grid-cols-[18px_minmax(0,1fr)] items-start gap-2.5",
          disabled ? "cursor-not-allowed" : "cursor-pointer",
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
      {children && <div className="pl-7">{children}</div>}
    </div>
  );
}

// ClientDetail renders the tabbed inspector for one client.
function ClientDetail({
  client,
  active,
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
  onOpen,
  onConfirm,
  setConfirmFn,
  onDelete,
  onToggleEnabled,
  onDuplicate,
}: {
  client: Client;
  active: Snapshot | null;
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
  onOpen?: SectionProps["onOpen"];
  onConfirm: (o: ConfirmOptions) => void;
  setConfirmFn: (fn: (() => void) | null) => void;
  onDelete: () => Promise<void>;
  onToggleEnabled: () => Promise<void>;
  onDuplicate: () => void;
}) {
  const scope = managerScope(session);
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
  const notices = useClientNotices(request, client.id);
  const missing = countNotices(notices.items).new;
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
            ·<span>{connectionLabel(snap, client)}</span>·
            <AccessBadge client={client} snap={snap} />
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
            stats={stats}
            recent={recent}
            snap={snap}
            notices={notices.items}
            onMark={notices.mark}
            onCommit={onCommit}
            onToast={onToast}
            onTab={onTab}
          />
        )}
        {tab === "access" && (
          <AccessTab
            client={client}
            snap={snap}
            scope={scope}
            notices={notices.items}
            onMark={notices.mark}
            canBlock={!scope}
            request={request}
            onCommit={onCommit}
            onToast={onToast}
            onOpen={onOpen}
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
        {tab === "history" && (
          <HistoryTab
            client={client}
            snap={snap}
            active={active}
            request={request}
          />
        )}
      </InspectorBody>
    </>
  );
}

function kindLabel(kind?: string): string {
  if (kind === "network") return "Сеть";
  if (kind === "group") return "Группа";
  return "Устройство";
}

// connectionLabel: the client's listener and proxy login.
function connectionLabel(snap: Snapshot, client: Client): string {
  if (clientTransport(snap, client) === "tls")
    return client.proxy_username
      ? `TLS, логин ${client.proxy_username}`
      : "TLS без логина";
  return "HTTP без логина";
}

// AccessBadge: the access mode and, for a list, the size of the final list.
function AccessBadge({ client, snap }: { client: Client; snap: Snapshot }) {
  if (!client.enabled)
    return (
      <Badge variant="secondary">
        <Ban className="size-3.5" /> выключен
      </Badge>
    );
  if (client.access === "all_web")
    return (
      <Badge variant="secondary">
        <Globe className="size-3.5" /> Весь веб
      </Badge>
    );
  const n = effectiveDestinations(snap, client).length;
  if (client.access === "none" || n === 0)
    return <Badge variant="secondary">нет доступа</Badge>;
  return (
    <Badge variant="secondary">
      <ShieldCheck className="size-3.5" />
      {pluralRu(n, "домен", "домена", "доменов")}
    </Badge>
  );
}

// OverviewTab follows the prototype: a callout with the new denied domains
// the client may not reach (notifications; "Просмотрено" marks them seen for
// everyone), KPIs, then traffic and top destinations on the left and rules
// and recent requests on the right.
function OverviewTab({
  client,
  stats,
  recent,
  snap,
  notices,
  onMark,
  onCommit,
  onToast,
  onTab,
}: {
  client: Client;
  stats?: ClientStats;
  recent: Event[] | null;
  snap: Snapshot;
  notices: Notice[] | null;
  onMark: (ids: string[], status: NoticeStatus) => Promise<void>;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onTab: (t: Tab) => void;
}) {
  const top = stats?.top_destinations ?? [];
  const deniedTop = missingDomains(stats?.top_destinations, snap, client);
  const isMissing = (host: string) => deniedTop.some((d) => d.host === host);
  const fresh = (notices ?? []).filter((n) => n.status === "new");
  const shown = fresh.slice(0, 5);
  const rest = fresh.length - shown.length;
  const list = effectiveDestinations(snap, client).map((e) => e.destination);
  const attached = snap.policies.filter((p) =>
    client.policy_ids.includes(p.id),
  );
  const blocked = snap.settings?.blocked_domains?.length ?? 0;
  const allow = async (host: string) => {
    const res = await onCommit(addDomainToClient(snap, client.id, host));
    if (res.ok) onToast(`Домен ${host} добавлен клиенту`);
  };
  const max = Math.max(1, ...top.map((d) => d.requests));
  return (
    <>
      {fresh.length > 0 && (
        <Callout tone="bad" icon={<Ban aria-hidden />}>
          <div className="flex flex-wrap items-start gap-x-3 gap-y-1.5">
            <div className="min-w-0 flex-1">
              <div className="font-medium">
                {fresh.length === 1
                  ? "Новый домен, которого нет"
                  : `${pluralRu(fresh.length, "новый домен", "новых домена", "новых доменов")}, которых нет`}{" "}
                в списке клиента ·{" "}
                {pluralRu(
                  fresh.reduce((n, d) => n + d.denied, 0),
                  "отказ",
                  "отказа",
                  "отказов",
                )}{" "}
                за 7 дней
              </div>
              <div className="mt-0.5 text-sm text-muted-foreground">
                <span className="font-mono">
                  {shown.map((d) => d.host).join(", ")}
                </span>
                {rest > 0 && ` и ещё ${rest}`}
              </div>
            </div>
            <div className="flex gap-1.5">
              <Button
                size="xs"
                variant="outline"
                onClick={() => onTab("access")}
              >
                Разобрать
              </Button>
              <Button
                size="xs"
                variant="outline"
                onClick={() =>
                  void onMark(
                    fresh.map((n) => n.id),
                    "seen",
                  )
                }
              >
                <Check aria-hidden /> Отметить просмотренными
              </Button>
            </div>
          </div>
        </Callout>
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
                ? "нет в списке клиента"
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
                              {isMissing(d.host)
                                ? "нет в списке клиента"
                                : "запрещено защитой или для всех"}
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
                          {isMissing(d.host) && (
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
                  {client.access === "all_web"
                    ? "Любые внешние сайты, кроме запрещённых для всех"
                    : client.access === "none" || list.length === 0
                      ? "Нет доступа"
                      : "Только домены из итогового списка"}
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
              {client.access === "list" && list.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                  {list.slice(0, 8).map((d) => (
                    <Chip key={d.domain + d.include_subdomains}>
                      {d.include_subdomains ? "*." : ""}
                      {d.domain}
                    </Chip>
                  ))}
                  {list.length > 8 && (
                    <Chip className="text-muted-foreground">
                      +{list.length - 8}
                    </Chip>
                  )}
                </div>
              )}
              <KeyValue
                items={[
                  [
                    "Политики",
                    client.access !== "list" || attached.length === 0
                      ? "—"
                      : attached.map((p) => p.name).join(", "),
                  ],
                  [
                    "Свои домены",
                    client.access !== "list" || client.destinations.length === 0
                      ? "—"
                      : pluralRu(
                          client.destinations.length,
                          "домен",
                          "домена",
                          "доменов",
                        ),
                  ],
                  [
                    "Поверх неё",
                    `${blocked ? pluralRu(blocked, "запрещённый домен", "запрещённых домена", "запрещённых доменов") + ", " : ""}встроенная защита`,
                  ],
                  ["Подключение", connectionLabel(snap, client)],
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

// AccessTab: the client's access mode; for a list, the denied domains to
// review, attached policies (shared domain sets), the client's own domains
// and the final list with the source of each line. The client may reach the
// union of its own domains and the attached policies.
function AccessTab({
  client,
  snap,
  scope,
  notices,
  onMark,
  canBlock,
  request,
  onCommit,
  onToast,
  onOpen,
}: {
  client: Client;
  snap: Snapshot;
  scope: ReturnType<typeof managerScope>;
  notices: Notice[] | null;
  onMark: (ids: string[], status: NoticeStatus) => Promise<void>;
  canBlock: boolean;
  request: SectionProps["request"];
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onOpen?: SectionProps["onOpen"];
}) {
  const [shareName, setShareName] = useState<string | null>(null);
  const attached = client.policy_ids
    .map((id) => snap.policies.find((p) => p.id === id))
    .filter((p): p is NonNullable<typeof p> => !!p);
  const available = snap.policies.filter(
    (p) => !client.policy_ids.includes(p.id),
  );
  const usersOf = (id: string) =>
    snap.clients.filter((c) => c.policy_ids.includes(id));
  // A manager may not edit a policy attached by a foreign client.
  const editableBy = (id: string) =>
    !scope || usersOf(id).every((c) => scope.clientIDs.includes(c.id));
  const final = effectiveDestinations(snap, client);
  const policyName = (id: string) =>
    snap.policies.find((p) => p.id === id)?.name ?? id;
  const select = async (v: string) => {
    if (v !== client.access)
      await onCommit(setClientAccess(snap, client.id, v as Access));
  };
  return (
    <>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>Доступ</CardTitle>
            <CardDescription>
              Встроенная защита и «запрещено для всех» действуют в любом режиме
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent>
          <RadioGroup
            value={client.access}
            onValueChange={select}
            className="grid grid-cols-1 gap-2 min-[1180px]:grid-cols-3"
          >
            <RadioCard
              value="list"
              active={client.access === "list"}
              title="Список доменов"
              hint="Свои домены и подключённые политики"
            />
            <RadioCard
              value="all_web"
              active={client.access === "all_web"}
              title="Весь веб"
              hint="Кроме запрещённого для всех"
            />
            <RadioCard
              value="none"
              active={client.access === "none"}
              title="Нет доступа"
              hint="Клиент описан, но всё закрыто"
            />
          </RadioGroup>
        </CardContent>
      </Card>
      {client.access === "all_web" && (
        <Callout tone="info" icon={<Globe aria-hidden />}>
          <div className="text-sm">
            Разрешены любые внешние сайты. Списки клиента сохранены и снова
            действуют, если вернуть режим «Список доменов».
          </div>
        </Callout>
      )}
      {client.access === "none" && (
        <Callout tone="info" icon={<Eye aria-hidden />}>
          <div className="text-sm">
            Все запросы клиента получают отказ. Добавленный домен или
            подключённая политика включат режим «Список доменов».
          </div>
        </Callout>
      )}
      {client.access !== "all_web" && (
        <DeniedDomains
          client={client}
          snap={snap}
          items={notices}
          canBlock={canBlock}
          onMark={onMark}
          onCommit={onCommit}
          onToast={onToast}
        />
      )}
      {client.access !== "all_web" && (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Политики</CardTitle>
              <CardDescription>
                Общие наборы доменов. Правка политики действует на всех её
                клиентов
              </CardDescription>
            </div>
            {available.length > 0 && (
              <CardAction>
                <Select
                  value=""
                  onValueChange={(id) =>
                    onCommit(attachPolicy(snap, client.id, id))
                  }
                >
                  <SelectTrigger
                    className="h-7"
                    aria-label="Подключить политику"
                  >
                    <SelectValue placeholder="Подключить…" />
                  </SelectTrigger>
                  <SelectContent>
                    {available.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}:{" "}
                        {pluralRu(
                          p.destinations.length,
                          "домен",
                          "домена",
                          "доменов",
                        )}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </CardAction>
            )}
          </CardHeader>
          <CardContent>
            {attached.length === 0 ? (
              <div className="text-sm text-muted-foreground">
                {snap.policies.length
                  ? "Не подключено ни одной политики"
                  : "Политик пока нет. Их создают в разделе «Политики» или из своих доменов клиента"}
              </div>
            ) : (
              attached.map((p) => {
                const others = usersOf(p.id).filter((c) => c.id !== client.id);
                return (
                  <div
                    key={p.id}
                    className="flex items-center gap-2.5 border-b py-1.5 last:border-b-0"
                  >
                    <ShieldCheck
                      aria-hidden
                      className="size-4 text-muted-foreground"
                    />
                    <span className="min-w-0 grow">
                      <span className="font-medium">{p.name}</span>
                      <span className="ml-2 text-sm text-muted-foreground">
                        {pluralRu(
                          p.destinations.length,
                          "домен",
                          "домена",
                          "доменов",
                        )}
                        {others.length > 0 &&
                          ` · ещё у ${others.map((c) => c.name).join(", ")}`}
                        {!editableBy(p.id) && " · правит администратор"}
                      </span>
                    </span>
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={() => onOpen?.("policies", `policy:${p.id}`)}
                    >
                      Открыть
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Отключить ${p.name}`}
                      onClick={() =>
                        onCommit(detachPolicy(snap, client.id, p.id))
                      }
                    >
                      <X aria-hidden />
                    </Button>
                  </div>
                );
              })
            )}
          </CardContent>
        </Card>
      )}
      {client.access !== "all_web" && (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Свои домены</CardTitle>
              <CardDescription>
                Действуют только для этого клиента
              </CardDescription>
            </div>
            {client.destinations.length > 0 && shareName === null && (
              <CardAction>
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => setShareName(client.name)}
                >
                  Сделать политикой…
                </Button>
              </CardAction>
            )}
          </CardHeader>
          <CardContent>
            {shareName !== null && (
              <form
                className="mb-2.5 flex flex-wrap items-center gap-2"
                onSubmit={async (e) => {
                  e.preventDefault();
                  const r = shareClientDomains(snap, client.id, shareName);
                  if (!r) {
                    onToast("Укажите имя политики");
                    return;
                  }
                  const res = await onCommit(r.snapshot);
                  if (res.ok) {
                    setShareName(null);
                    onToast("Свои домены перенесены в новую политику");
                  }
                }}
              >
                <Input
                  autoFocus
                  aria-label="Имя новой политики"
                  className="max-w-[260px]"
                  value={shareName}
                  onChange={(e) => setShareName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Escape") setShareName(null);
                  }}
                />
                <Button size="xs" type="submit">
                  Создать политику
                </Button>
                <Button
                  size="xs"
                  variant="ghost"
                  type="button"
                  onClick={() => setShareName(null)}
                >
                  Отмена
                </Button>
                <span className="text-sm text-muted-foreground">
                  Домены станут политикой, которую можно подключить другим
                  клиентам
                </span>
              </form>
            )}
            <DomainEditor
              destinations={client.destinations}
              onChange={async (next) => {
                await onCommit({
                  ...snap,
                  clients: snap.clients.map((c) =>
                    c.id === client.id
                      ? {
                          ...c,
                          access: next.length ? "list" : c.access,
                          destinations: next,
                        }
                      : c,
                  ),
                });
              }}
            />
            <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
              <TemplateButtons
                request={request}
                onAdd={async (doms) => {
                  const res = await onCommit(
                    addDomainsToClient(snap, client.id, doms),
                  );
                  if (res.ok) onToast("Шаблон добавлен");
                }}
              />
            </div>
          </CardContent>
        </Card>
      )}
      {client.access === "list" && (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Итоговый список</CardTitle>
              <CardDescription>
                Что открыто клиенту: свои домены и политики без повторов
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            {final.length === 0 ? (
              <div className="text-sm text-muted-foreground">
                Список пуст: клиенту ничего не открыто
              </div>
            ) : (
              <Table className="table-fixed">
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-0">Домен</TableHead>
                    <TableHead className="pr-0">Откуда</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {final.map((e) => (
                    <TableRow
                      key={
                        e.destination.domain + e.destination.include_subdomains
                      }
                    >
                      <TableCell className="pl-0">
                        <Chip>
                          {e.destination.include_subdomains ? "*." : ""}
                          {e.destination.domain}
                        </Chip>
                      </TableCell>
                      <TableCell className="truncate pr-0 text-sm text-muted-foreground">
                        {[
                          ...(e.own ? ["свой"] : []),
                          ...e.policyIDs.map((id) => `«${policyName(id)}»`),
                        ].join(", ")}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}
    </>
  );
}

// addressCount renders how many addresses a network holds ("1 адрес",
// "256 адресов"); IPv6 prefixes beyond /64 are not counted.
function addressCount(net: string): string {
  const [ip, bits] = net.split("/");
  const v6 = ip.includes(":");
  const prefix = bits === undefined ? (v6 ? 128 : 32) : Number(bits);
  const free = (v6 ? 128 : 32) - prefix;
  if (free === 0) return "1 адрес";
  if (free > 52) return "сеть IPv6";
  const n = Math.pow(2, free);
  return pluralRu(n, "адрес", "адреса", "адресов").replace(
    String(n),
    n.toLocaleString("ru-RU"),
  );
}

// SourceTab: who the client is, its source addresses (one row each, saved
// when a row loses focus) and how it connects: HTTP without a login, or the
// TLS listener with a proxy login (tls_profile_id and proxy_username).
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
  const [rows, setRows] = useState<string[]>(client.source_networks);
  const [fieldError, setFieldError] = useState("");
  const [accounts, setAccounts] = useState<ProxyAccount[]>([]);
  useEffect(() => {
    setName(client.name);
    setDescription(client.description ?? "");
    setRows(client.source_networks);
    setFieldError("");
  }, [client]);
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
  const others = snap.clients.filter((c) => c.id !== client.id && c.enabled);
  const patch = (p: Partial<Client>) =>
    onCommit({
      ...snap,
      clients: snap.clients.map((c) =>
        c.id === client.id ? { ...c, ...p } : c,
      ),
    });
  // Overlaps allowed only between different proxy logins.
  const overlapOf = (n: string) =>
    others.find((c) => c.source_networks.some((m) => networksOverlap(n, m)));
  const loginsDiffer = (c: Client) =>
    !!client.proxy_username &&
    !!c.proxy_username &&
    client.proxy_username !== c.proxy_username;
  const saveNetworks = async (list: string[]) => {
    const parsed: string[] = [];
    for (const ln of list.map((s) => s.trim()).filter(Boolean)) {
      const n = parseNetwork(ln);
      if (!n) {
        setFieldError(`«${ln}» не похоже на IP-адрес или сеть.`);
        return;
      }
      const o = overlapOf(n);
      if (o && !loginsDiffer(o)) {
        setFieldError(`${n} пересекается с клиентом ${o.name}.`);
        return;
      }
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
    if (parsed.join() === client.source_networks.join()) return;
    const res = await onCommit(setClientNetworks(snap, client.id, parsed));
    if (!res.ok && res.code === "source_outside_scope")
      setFieldError("Адрес вне ваших сетей.");
    else if (!res.ok && res.code === "source_overlap")
      setFieldError("Адрес пересекается с чужим клиентом.");
    else if (!res.ok && res.code !== "revision_conflict")
      onToast(res.message ?? "Ошибка сохранения");
  };
  const transport = clientTransport(snap, client);
  const connect = async (username: string | undefined) => {
    const res = await onCommit(setClientConnection(snap, client.id, username));
    if (!res.ok && res.code === "username_taken") onToast("Логин уже занят");
  };
  const allowedOverlaps = client.source_networks
    .map((n) => [n, overlapOf(n)] as const)
    .filter(([, o]) => o && loginsDiffer(o));
  const settings = snap.settings;
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Кто это</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-3.5 @[700px]:grid-cols-2">
            <Field
              label="Имя"
              htmlFor="f-src-name"
              help="Латиница, цифры и дефис. Под этим именем клиент виден в журнале"
            >
              <Input
                id="f-src-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                onBlur={() => {
                  const v = name.trim();
                  if (v && v !== client.name) void patch({ name: v });
                }}
              />
            </Field>
            <Field label="Описание" htmlFor="f-src-desc">
              <Input
                id="f-src-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onBlur={() => {
                  if (description !== (client.description ?? ""))
                    void patch({ description: description || undefined });
                }}
              />
            </Field>
            <Field
              label="Тип"
              help="Подсказка для людей; доступ определяется адресами ниже"
            >
              <ToggleGroup
                type="single"
                variant="seg"
                size="sm"
                spacing={0.5}
                className="w-full rounded-lg bg-muted p-[3px]"
                aria-label="Тип клиента"
                value={client.kind ?? "host"}
                onValueChange={(v) => {
                  if (v) void patch({ kind: v as Client["kind"] });
                }}
              >
                <ToggleGroupItem value="host">Устройство</ToggleGroupItem>
                <ToggleGroupItem value="network">Сеть</ToggleGroupItem>
                <ToggleGroupItem value="group">Группа сетей</ToggleGroupItem>
              </ToggleGroup>
            </Field>
            <Field label="Включён">
              <label className="flex h-8 items-center gap-2 text-sm text-muted-foreground">
                <Switch
                  checked={client.enabled}
                  onCheckedChange={(on) =>
                    onCommit(setClientEnabled(snap, client.id, on))
                  }
                />
                {client.enabled
                  ? "Правила действуют"
                  : "Все запросы получают отказ"}
              </label>
            </Field>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>Адреса источника</CardTitle>
            <CardDescription>
              IP или CIDR, как их видит прокси (после NAT)
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {rows.map((r, i) => {
            const n = parseNetwork(r);
            return (
              <div key={i} className="flex items-center gap-2">
                <Input
                  className="max-w-[260px] font-mono text-sm"
                  aria-label={`Адрес ${i + 1}`}
                  value={r}
                  placeholder="10.20.0.77 или 10.20.0.0/24"
                  onChange={(e) =>
                    setRows(rows.map((x, j) => (j === i ? e.target.value : x)))
                  }
                  onBlur={() => saveNetworks(rows)}
                />
                <span className="text-sm text-muted-foreground">
                  {n ? addressCount(n) : ""}
                </span>
                <span className="grow" />
                {rows.length > 1 && (
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`Удалить ${r}`}
                    onClick={() => {
                      const next = rows.filter((_, j) => j !== i);
                      setRows(next);
                      void saveNetworks(next);
                    }}
                  >
                    <X aria-hidden />
                  </Button>
                )}
              </div>
            );
          })}
          {fieldError && <div className="text-sm text-bad">{fieldError}</div>}
          {allowedOverlaps.map(([n, o]) => (
            <Callout key={n} tone="info" icon={<Info aria-hidden />}>
              <div className="text-sm">
                {n} пересекается с {o!.name}: допустимо, у клиентов разные
                логины прокси
              </div>
            </Callout>
          ))}
          <Button
            size="xs"
            variant="outline"
            className="self-start border-dashed"
            onClick={() => setRows([...rows, ""])}
          >
            <Plus aria-hidden /> Адрес или сеть
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Подключение к прокси</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <RadioGroup
            value={transport}
            onValueChange={(v) => {
              if (v === "http") void connect(undefined);
              else if (accounts[0]) void connect(accounts[0].username);
            }}
            className="grid grid-cols-1 gap-2 @[700px]:grid-cols-2"
          >
            <RadioCard
              value="http"
              active={transport === "http"}
              title={`HTTP, порт ${settings?.http_port ?? 3128}`}
              hint="Без шифрования до прокси. Логин недоступен"
            />
            <RadioCard
              value="tls"
              active={transport === "tls"}
              title={`TLS, порт ${settings?.tls_port ?? 3129}`}
              hint={
                accounts.length
                  ? "Шифрование до прокси, вход по логину прокси"
                  : "Сначала создайте логин прокси в разделе «Аккаунты»"
              }
              disabled={!accounts.length && transport !== "tls"}
            >
              {transport === "tls" && (
                <Select
                  value={client.proxy_username ?? ""}
                  onValueChange={(v) => void connect(v)}
                >
                  <SelectTrigger
                    className="mt-1.5 h-7"
                    aria-label="Логин прокси"
                  >
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
              )}
            </RadioCard>
          </RadioGroup>
        </CardContent>
      </Card>
    </>
  );
}

// RequestsTab: the client's recent events with a destination filter and an
// all/denied switch; the full search lives in the log.
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
  const [dest, setDest] = useState("");
  const [only, setOnly] = useState<"all" | "denied">("all");
  useEffect(() => {
    let live = true;
    const q = new URLSearchParams({ client_id: client.id, limit: "50" });
    if (dest.trim()) q.set("destination", dest.trim());
    if (only === "denied") q.set("failures_only", "true");
    const t = setTimeout(() => {
      request<{ items: Event[] }>(`/events?${q}`)
        .then((r) => {
          if (live) setItems(r.items ?? []);
        })
        .catch(() => {});
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [request, client.id, dest, only]);
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex max-w-[420px] min-w-0 grow items-center">
          <Search
            aria-hidden
            className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
          />
          <Chip className="pointer-events-none absolute left-7 h-5 text-xs">
            клиент:{client.name}
          </Chip>
          <Input
            aria-label="Фильтр по назначению"
            className="h-8"
            style={{ paddingLeft: `${client.name.length * 7 + 86}px` }}
            placeholder="домен…"
            value={dest}
            onChange={(e) => setDest(e.target.value)}
          />
        </div>
        <ToggleGroup
          type="single"
          variant="seg"
          size="sm"
          spacing={0.5}
          className="rounded-lg bg-muted p-[3px]"
          value={only}
          aria-label="Какие запросы"
          onValueChange={(v) => {
            if (v) setOnly(v as "all" | "denied");
          }}
        >
          <ToggleGroupItem value="all">Все</ToggleGroupItem>
          <ToggleGroupItem value="denied">Отказы</ToggleGroupItem>
        </ToggleGroup>
        <span className="grow" />
        <Button size="xs" variant="outline" onClick={() => onNavigate("log")}>
          Открыть в журнале <ArrowRight aria-hidden />
        </Button>
      </div>
      <div className="overflow-hidden rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Время</TableHead>
              <TableHead>IP</TableHead>
              <TableHead>Назначение</TableHead>
              <TableHead>Результат</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.slice(0, 50).map((e) => {
              const denied = e.proxy_result.includes("DENIED");
              const code = e.proxy_http_status ?? (denied ? 403 : 200);
              return (
                <TableRow key={e.id}>
                  <TableCell className="text-muted-foreground tabular-nums">
                    {new Date(e.timestamp).toLocaleTimeString("ru-RU")}
                  </TableCell>
                  <TableCell className="font-mono text-sm">
                    {e.source_ip}
                  </TableCell>
                  <TableCell className="max-w-0 truncate font-mono text-sm">
                    {e.method !== "CONNECT" && (
                      <span className="text-muted-foreground">{e.method} </span>
                    )}
                    {e.destination}
                  </TableCell>
                  <TableCell>
                    <Badge variant={denied ? "bad" : "ok"}>
                      {denied ? `${code} отказ` : code}
                    </Badge>
                    {e.count > 1 && (
                      <span className="ml-1.5 text-sm text-muted-foreground">
                        ×{e.count}
                      </span>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
            {items.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="text-muted-foreground">
                  Нет запросов
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

type HistoryEntry = {
  at: string;
  actor: string;
  changes: string[];
  revision?: string;
  label?: string;
};

// HistoryTab: what changed for this client, newest first. Applied revisions
// (up to 15) are diffed pairwise; the unapplied draft is diffed against the
// active configuration. Actors come from the create_revision audit rows.
function HistoryTab({
  client,
  snap,
  active,
  request,
}: {
  client: Client;
  snap: Snapshot;
  active: Snapshot | null;
  request: SectionProps["request"];
}) {
  const [entries, setEntries] = useState<HistoryEntry[] | null>(null);
  useEffect(() => {
    let live = true;
    (async () => {
      const [revs, audit] = await Promise.all([
        request<{
          items: {
            id: string;
            number: number;
            created_at: string;
            was_applied: boolean;
          }[];
          next_cursor?: string;
        }>("/revisions?limit=50"),
        request<{ items: AuditRow[] }>(
          "/audit?action=create_revision&limit=200",
        ).catch(() => ({
          items: [] as AuditRow[],
        })),
      ]);
      const appliedAll = revs.items.filter((r) => r.was_applied);
      const applied = appliedAll.slice(0, 15);
      // Without a baseline the oldest fetched revision is only diffable when
      // it is the very first applied one (everything in it was created).
      const complete = !revs.next_cursor && appliedAll.length <= 15;
      const snaps = await Promise.all(
        applied.map((r) =>
          request<{ snapshot: Snapshot }>(`/revisions/${r.id}`)
            .then((x) => x.snapshot)
            .catch(() => null),
        ),
      );
      const actorOf = (id: string) =>
        audit.items.find(
          (a) => a.action === "create_revision" && a.entity_id === id,
        )?.actor ?? "—";
      const out: HistoryEntry[] = [];
      applied.forEach((r, i) => {
        const after = snaps[i];
        if (!after) return;
        // The next item is older; the oldest fetched one has no baseline.
        const before = i + 1 < applied.length ? snaps[i + 1] : null;
        if (i + 1 === applied.length && !complete) return;
        if (i + 1 < applied.length && !before) return; // fetch failed
        const changes = clientChanges(before, after, client.id);
        if (changes.length)
          out.push({
            at: r.created_at,
            actor: actorOf(r.id),
            changes,
            revision: r.id,
            label: revisionLabel(r.id, r.number),
          });
      });
      if (live) setEntries(out);
    })().catch(() => {
      if (live) setEntries([]);
    });
    return () => {
      live = false;
    };
  }, [request, client.id]);
  const pending = active ? clientChanges(active, snap, client.id) : [];
  const rows: HistoryEntry[] = [
    ...(pending.length ? [{ at: "", actor: "", changes: pending }] : []),
    ...(entries ?? []),
  ];
  return (
    <Card className="py-1">
      <CardContent>
        {rows.map((r, i) => (
          <div
            key={r.revision ?? `draft-${i}`}
            className="grid grid-cols-[98px_minmax(0,1fr)_auto] items-start gap-3 border-b py-2.5 last:border-b-0"
          >
            <span className="text-muted-foreground tabular-nums">
              {r.at
                ? new Date(r.at).toLocaleString("ru-RU", {
                    day: "2-digit",
                    month: "2-digit",
                    hour: "2-digit",
                    minute: "2-digit",
                  })
                : "сейчас"}
            </span>
            <div className="min-w-0">
              <div>{r.changes.join("; ")}</div>
              {r.actor && (
                <div className="text-sm text-muted-foreground">{r.actor}</div>
              )}
            </div>
            {r.revision ? (
              <Badge variant="secondary" className="font-mono">
                {r.label}
              </Badge>
            ) : (
              <Badge variant="mod">в черновике</Badge>
            )}
          </div>
        ))}
        {entries !== null && rows.length === 0 && (
          <div className="py-2 text-sm text-muted-foreground">
            Изменений не было
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export type NewClientInput = {
  name: string;
  kind: "host" | "network" | "group";
  networks: string[];
  domains: string[];
  access: Access;
  policyIDs: string[];
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
// source addresses), where to (own domains and attached policies, all web or
// nothing yet) and how it connects (HTTP, or TLS with a proxy login).
function NewClientForm({
  draft,
  request,
  from,
  fromSource,
  sourceStatus,
  onMarkSource,
  initialName,
  initialSrc,
  initialDomains,
  initialAccess,
  initialPolicyIDs,
  initialLogin,
  onCancel,
  onSubmit,
}: {
  draft: Snapshot;
  request: SectionProps["request"];
  from?: string;
  fromSource?: UnknownSource;
  sourceStatus?: NoticeStatus;
  onMarkSource?: (status: NoticeStatus) => Promise<void>;
  initialName: string;
  initialSrc: string;
  initialDomains: string[];
  initialAccess?: Access;
  initialPolicyIDs?: string[];
  initialLogin?: string;
  onCancel: () => void;
  onSubmit: (input: NewClientInput) => Promise<void>;
}) {
  const [name, setName] = useState(initialName);
  const [kind, setKind] = useState<NewClientInput["kind"]>("host");
  const [src, setSrc] = useState(initialSrc);
  const [domains, setDomains] = useState(initialDomains.join("\n"));
  const [access, setAccess] = useState<Access>(initialAccess ?? "list");
  const [policyIDs, setPolicyIDs] = useState<string[]>(initialPolicyIDs ?? []);
  const [err, setErr] = useState("");
  const [login, setLogin] = useState(initialLogin ?? "");
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
          <>
            {from && onMarkSource && sourceStatus === "new" && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => void onMarkSource("seen")}
              >
                Просмотрено
              </Button>
            )}
            {from && onMarkSource && sourceStatus !== "ignored" && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => void onMarkSource("ignored")}
              >
                Игнорировать
              </Button>
            )}
            {from && onMarkSource && sourceStatus && sourceStatus !== "new" && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => void onMarkSource("new")}
              >
                Снять отметку
              </Button>
            )}
            <Button size="xs" variant="ghost" onClick={onCancel}>
              Отмена
            </Button>
          </>
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
              value={access}
              onValueChange={(v) => setAccess(v as Access)}
              className="grid grid-cols-1 gap-2 @[700px]:grid-cols-3"
            >
              <RadioCard
                value="list"
                active={access === "list"}
                title="Список доменов"
                hint="Свои домены и политики"
              />
              <RadioCard
                value="all_web"
                active={access === "all_web"}
                title="Весь веб"
                hint="Кроме запрещённого для всех"
              />
              <RadioCard
                value="none"
                active={access === "none"}
                title="Пока без доступа"
                hint="Описать и решить позже"
              />
            </RadioGroup>
            {access === "list" && draft.policies.length > 0 && (
              <Field label="Политики">
                <div className="flex flex-col gap-1.5">
                  {draft.policies.map((p) => (
                    <label
                      key={p.id}
                      className="flex items-center gap-2 text-sm"
                    >
                      <Checkbox
                        checked={policyIDs.includes(p.id)}
                        onCheckedChange={(on) =>
                          setPolicyIDs((cur) =>
                            on
                              ? [...cur, p.id]
                              : cur.filter((id) => id !== p.id),
                          )
                        }
                      />
                      <span className="font-medium">{p.name}</span>
                      <span className="text-muted-foreground">
                        {pluralRu(
                          p.destinations.length,
                          "домен",
                          "домена",
                          "доменов",
                        )}
                        ,{" "}
                        {pluralRu(
                          draft.clients.filter((c) =>
                            c.policy_ids.includes(p.id),
                          ).length,
                          "клиент",
                          "клиента",
                          "клиентов",
                        )}
                      </span>
                    </label>
                  ))}
                </div>
              </Field>
            )}
            {access === "list" && (
              <Field
                label="Свои домены"
                htmlFor="f-nc-domains"
                help={
                  <span className="flex flex-wrap items-center gap-1.5">
                    {from &&
                      "Подставлены домены из отказанных запросов этого IP. "}
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
                title="HTTP без логина"
                hint="Клиент узнаётся по адресу источника"
              />
              <RadioCard
                value="login"
                active={!!login}
                title="TLS с логином прокси"
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
                  access === "list"
                    ? domains.split(/[\s,]+/).filter(Boolean)
                    : [],
                access,
                policyIDs: access === "list" ? policyIDs : [],
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

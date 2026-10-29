import { useEffect, useMemo, useState } from "react";
import {
  Ban,
  Copy,
  Eye,
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
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
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
      <div className="placeholder-section">
        <aside className="list-pane">
          <div className="list-head">
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="list-title">Клиенты</span>
              <Button
                size="xs"
                onClick={() => setCreating({ name: "", src: "", domains: [] })}
              >
                <Plus aria-hidden /> Новый
              </Button>
            </div>
          </div>
          <div className="list-body">
            <div className="list-group-label">Неописанные источники</div>
            {(unknown ?? []).map((u) => (
              <button
                key={u.source_ip}
                className="list-item"
                onClick={() =>
                  setCreating({
                    from: u.source_ip,
                    name: "",
                    src: u.source_ip,
                    domains: u.top_destinations.map((d) => d.host),
                  })
                }
              >
                <span className="mono">{u.source_ip}</span>
                <Badge variant="destructive">{u.denied} отказов</Badge>
              </button>
            ))}
            {(!unknown || unknown.length === 0) && (
              <div
                className="muted-text small"
                style={{ padding: "12px 14px" }}
              >
                Клиентов пока нет. Пока их нет, прокси отказывает всем.
              </div>
            )}
          </div>
        </aside>
        <section className="inspector-pane">
          <div className="inspector-body">
            <EmptyState icon={<Users />} title="Добро пожаловать в Kraken">
              Опишите первого клиента, чтобы разрешить ему доступ через прокси.
            </EmptyState>
          </div>
        </section>
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
    <div className="placeholder-section">
      <aside className="list-pane">
        <div className="list-head">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <span className="list-title">
              {session.role === "manager" ? "Мои клиенты" : "Клиенты"}
            </span>
            <Button
              size="xs"
              onClick={() => setCreating({ name: "", src: "", domains: [] })}
            >
              <Plus aria-hidden /> Новый
            </Button>
          </div>
          <div className="search-input">
            <Search aria-hidden />
            <Input
              aria-label="Поиск клиентов"
              placeholder="Имя, IP или домен…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
            <FilterChip
              active={filter === "all"}
              onClick={() => setFilter("all")}
            >
              все {clients.length}
            </FilterChip>
            <FilterChip
              active={filter === "denied"}
              onClick={() => setFilter("denied")}
            >
              с отказами
            </FilterChip>
            <FilterChip
              active={filter === "draft"}
              onClick={() => setFilter("draft")}
            >
              в черновике {clients.filter((c) => clientMarks[c.id]).length}
            </FilterChip>
          </div>
        </div>
        <div className="list-body">
          {filtered.map((c) => {
            const st = stats[c.id];
            return (
              <button
                key={c.id}
                className={"list-item" + (selected === c.id ? " selected" : "")}
                onClick={() => {
                  setSelected(c.id);
                  setTab("overview");
                }}
              >
                <SpanMark mark={clientMarks[c.id]} name={c.name} />
                <Sparkline series={st?.series} />
                <span className="mono muted-text small ellipsis">
                  {c.source_networks.join(", ")}
                </span>
                <span className={"small num" + (st?.denied ? " up" : "")}>
                  {!c.enabled
                    ? "выкл"
                    : st?.denied
                      ? `${st.denied} отказов`
                      : st
                        ? `${st.requests}`
                        : "—"}
                </span>
              </button>
            );
          })}
          {unknown && unknown.length > 0 && (
            <>
              <div className="list-group-label">Неописанные источники</div>
              {unknown.map((u) => (
                <button
                  key={u.source_ip}
                  className="list-item"
                  onClick={() =>
                    setCreating({
                      from: u.source_ip,
                      name: "",
                      src: u.source_ip,
                      domains: u.top_destinations.map((d) => d.host),
                    })
                  }
                >
                  <span className="mono">{u.source_ip}</span>
                  <span className="small up">{u.denied} отказов</span>
                </button>
              ))}
            </>
          )}
          {filtered.length === 0 && (
            <div className="muted-text small" style={{ padding: "12px 14px" }}>
              Ничего не найдено
            </div>
          )}
        </div>
      </aside>
      <section className="inspector-pane">
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
          <div className="inspector-body">
            <EmptyState icon={<Users />} title="Выберите клиента">
              Выберите клиента слева или создайте нового.
            </EmptyState>
          </div>
        )}
      </section>
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
    <span className="ellipsis" style={{ fontWeight: 500 }}>
      {name}
      {mark === "new" && <Badge variant="default">новый</Badge>}
      {mark === "changed" && <Badge variant="secondary">изменён</Badge>}
      {mark === "deleted" && <Badge variant="destructive">удалён</Badge>}
    </span>
  );
}

function Sparkline({
  series,
}: {
  series?: { requests: number; denied: number }[];
}) {
  if (!series || series.length === 0)
    return <svg className="spark" width={52} height={16} />;
  const max = Math.max(1, ...series.map((s) => s.requests));
  const w = 52;
  const h = 16;
  const step = w / (series.length - 1 || 1);
  const pts = series
    .map((s, i) => `${i * step},${h - (s.requests / max) * h}`)
    .join(" ");
  return (
    <svg className="spark" width={w} height={h} aria-hidden>
      <polyline
        points={pts}
        fill="none"
        stroke="currentColor"
        strokeWidth={1}
      />
    </svg>
  );
}

function FilterChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={"badge" + (active ? " badge-selected" : "")}
      aria-pressed={active}
      onClick={onClick}
    >
      {children}
    </button>
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
      <header className="inspector-head">
        <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
          <button className="link" onClick={() => onNavigate("clients")}>
            Клиенты
          </button>
          <span>/</span>
          <h1 className="page-title" style={{ margin: 0 }}>
            {client.name}
          </h1>
          {marks[client.id] === "new" && <Badge>новый, в черновике</Badge>}
          {marks[client.id] === "changed" && (
            <Badge variant="secondary">изменён</Badge>
          )}
          <span className="grow" />
          <Button size="xs" onClick={onToggleEnabled}>
            <Power aria-hidden /> {client.enabled ? "Отключить" : "Включить"}
          </Button>
          <Button size="xs" onClick={onDuplicate}>
            <Copy aria-hidden /> Дублировать
          </Button>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => {
              onConfirm({
                title: `Удалить ${client.name}?`,
                text: "Клиент исчезнет из правил после применения. История запросов и аудит сохранятся.",
                ok: "Удалить",
                danger: true,
              });
              setConfirmFn(onDelete);
            }}
          >
            <Trash2 aria-hidden /> Удалить
          </Button>
        </div>
        <div
          className="muted-text small row"
          style={{ gap: 8, flexWrap: "wrap" }}
        >
          <span>{client.kind ?? "host"}</span>·
          <span className="mono">{client.source_networks.join(", ")}</span>·
          <PolicyBadge
            client={client}
            policy={policy}
            sharedCount={sharedWith.length}
          />
        </div>
      </header>
      <div
        className="tabs"
        role="tablist"
        style={{ borderBottom: "1px solid var(--border)" }}
      >
        {tabs.map(([k, l]) => (
          <span
            key={k}
            role="tab"
            className={"tab" + (tab === k ? " selected" : "")}
            tabIndex={0}
            onClick={() => onTab(k)}
          >
            {l}
          </span>
        ))}
      </div>
      <div className="inspector-body">
        {tab === "overview" && (
          <OverviewTab
            client={client}
            policy={policy}
            stats={stats}
            snap={snap}
            onCommit={onCommit}
            onToast={onToast}
            onNavigate={onNavigate}
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
      </div>
    </>
  );
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
  if (!client.enabled) return <span>выключен</span>;
  if (!policy) return <span>нет доступа</span>;
  if (policy.mode === "all_web") return <span>весь веб</span>;
  return (
    <span>
      {sharedCount > 1 ? `${policy.name} (общая)` : "своя политика"} ·{" "}
      {policy.destinations.length} доменов
    </span>
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
}: {
  client: Client;
  policy: Policy | null;
  stats?: ClientStats;
  snap: Snapshot;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
}) {
  const deniedTop = (stats?.top_destinations ?? []).filter((d) => d.denied > 0);
  return (
    <>
      {deniedTop.map((d) => (
        <div key={d.host} className="callout callout-bad">
          <Ban aria-hidden />
          <div className="grow">
            <div style={{ fontWeight: 500 }}>
              {d.denied} отказов к <span className="mono">{d.host}</span> за 24
              ч
            </div>
            <div className="small muted-text">
              Домена нет в политике клиента.
            </div>
          </div>
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
        </div>
      ))}
      <div className="kpis">
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
      </div>
      <div className="card">
        <div className="card-head">
          <span className="card-title">Куда обращается</span>
        </div>
        <div className="card-body">
          <table style={{ width: "100%" }}>
            <tbody>
              {(stats?.top_destinations ?? []).slice(0, 7).map((d) => (
                <tr key={d.host}>
                  <td className="mono">{d.host}</td>
                  <td className="num">{d.requests}</td>
                  {d.denied > 0 && (
                    <td>
                      <Button
                        size="xs"
                        onClick={async () => {
                          const res = await onCommit(
                            addDomainToClientPolicy(snap, client.id, d.host),
                          );
                          if (res.ok) onToast(`Домен ${d.host} добавлен`);
                        }}
                      >
                        Разрешить
                      </Button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card">
        <div className="card-head">
          <span className="card-title grow">Правила</span>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => onNavigate("clients")}
          >
            Изменить
          </Button>
        </div>
        <div className="card-body">
          <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
            {(policy?.destinations ?? []).slice(0, 8).map((d) => (
              <span key={d.domain} className="chip">
                {d.include_subdomains ? "*." : ""}
                {d.domain}
              </span>
            ))}
          </div>
          <dl className="kv-row" style={{ marginTop: 10 }}>
            <dt>Политика</dt>
            <dd>{policy ? policy.name : "—"}</dd>
          </dl>
        </div>
      </div>
    </>
  );
}

function Kpi({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "bad";
}) {
  return (
    <div className="kpi">
      <div className={"num" + (tone === "bad" ? " up" : "")}>{value}</div>
      <div className="muted-text small">{label}</div>
    </div>
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
      <div className="callout callout-info">
        <Eye aria-hidden />
        <div>Клиенту не назначена политика, доступа нет.</div>
      </div>
    );
  }
  const others = sharedWith.filter((c) => c.id !== client.id);
  return (
    <>
      <div className="card">
        <div className="card-head">
          <span className="card-title">Политика</span>
        </div>
        <div className="card-body">
          <div className="opts">
            <RadioOption
              active={policy.mode === "allowlist" && sharedWith.length <= 1}
              title="Своя политика"
              hint="Только для этого клиента"
              onClick={async () => {
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
              }}
            />
            <RadioOption
              active={policy.mode === "allowlist" && sharedWith.length > 1}
              title="Общая политика"
              hint={policy.name}
            />
            <RadioOption
              active={policy.mode === "all_web"}
              title="Весь веб"
              hint="Кроме запрещённого для всех"
              onClick={async () => {
                await onCommit({
                  ...snap,
                  policies: snap.policies.map((p) =>
                    p.id === policy.id
                      ? { ...p, mode: "all_web", destinations: [] }
                      : p,
                  ),
                });
              }}
            />
            <RadioOption
              active={false}
              title="Нет доступа"
              hint="Клиент описан, но всё закрыто"
            />
          </div>
          {others.length > 0 && (
            <div className="callout callout-warn">
              <ShieldCheck aria-hidden />
              <div className="small">
                Общая политика: правка затронет также{" "}
                {others.map((c) => c.name).join(", ")}.
              </div>
            </div>
          )}
        </div>
      </div>
      {policy.mode === "allowlist" && (
        <div className="card">
          <div className="card-head">
            <span className="card-title">Разрешённые домены</span>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => onNavigate("policies")}
            >
              Открыть политику
            </Button>
          </div>
          <div className="card-body">
            {deniedTop.length > 0 && (
              <div
                className="callout callout-warn"
                style={{ marginBottom: 10 }}
              >
                <Ban aria-hidden />
                <div className="small">
                  Отказанные домены этого клиента:{" "}
                  {deniedTop.map((d) => (
                    <Button
                      key={d.host}
                      size="xs"
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
              </div>
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
          </div>
        </div>
      )}
    </>
  );
}

function RadioOption({
  active,
  title,
  hint,
  onClick,
}: {
  active: boolean;
  title: string;
  hint?: string;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      className={"opt" + (active ? " selected" : "")}
      onClick={onClick}
      disabled={!onClick}
      aria-pressed={active}
    >
      <span className="radio" aria-hidden />
      <span>
        <div style={{ fontWeight: 500 }}>{title}</div>
        {hint && <div className="small muted-text">{hint}</div>}
      </span>
    </button>
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
      <div className="card">
        <div className="card-head">
          <span className="card-title grow">Кто это</span>
        </div>
        <div className="card-body fgrid">
          <Label>
            Имя
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Label>
          <Label>
            Описание
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Label>
          <Label>
            Тип
            <select
              className="in"
              value={kind}
              onChange={(e) => setKind(e.target.value as Client["kind"])}
            >
              <option value="host">Устройство</option>
              <option value="network">Сеть</option>
              <option value="group">Группа сетей</option>
            </select>
          </Label>
          <Label>
            Включён
            <Badge variant={client.enabled ? "default" : "secondary"}>
              {client.enabled
                ? "правила действуют"
                : "все запросы получают отказ"}
            </Badge>
          </Label>
        </div>
      </div>
      <div className="card">
        <div className="card-head">
          <span className="card-title grow">Адреса источника</span>
        </div>
        <div className="card-body">
          <Label>
            Адреса (по одному в строке)
            <Textarea
              className="mono"
              rows={3}
              value={networks}
              onChange={(e) => setNetworks(e.target.value)}
              placeholder="10.20.0.77 или 10.20.0.0/24"
            />
          </Label>
          {fieldError && <div className="err">{fieldError}</div>}
          <Button size="sm" style={{ marginTop: 8 }} onClick={save}>
            Сохранить адреса
          </Button>
        </div>
      </div>
      <div className="card">
        <div className="card-head">
          <span className="card-title grow">Подключение к прокси</span>
        </div>
        <div className="card-body">
          <Label>
            Логин прокси
            <select
              className="in"
              value={client.proxy_username ?? ""}
              onChange={async (e) => {
                const next = {
                  ...snap,
                  clients: snap.clients.map((c) =>
                    c.id === client.id
                      ? { ...c, proxy_username: e.target.value || undefined }
                      : c,
                  ),
                };
                const res = await onCommit(next);
                if (!res.ok && res.code === "username_taken")
                  onToast("Логин уже занят");
              }}
            >
              <option value="">Не требуется</option>
              {accounts.map((a) => (
                <option key={a.username} value={a.username}>
                  {a.username}
                </option>
              ))}
            </select>
          </Label>
          <div className="help muted-text small">
            Логины прокси настраиваются в разделе «Аккаунты». Пароль в политике
            не хранится.
          </div>
        </div>
      </div>
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
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button size="xs" variant="ghost" onClick={() => onNavigate("log")}>
          Открыть в журнале
        </Button>
      </div>
      <div className="card">
        <div className="card-body">
          <table style={{ width: "100%" }}>
            <thead>
              <tr>
                <th>Время</th>
                <th>Назначение</th>
                <th>Результат</th>
              </tr>
            </thead>
            <tbody>
              {items.slice(0, 20).map((e) => (
                <tr key={e.id}>
                  <td className="num muted-text small">
                    {e.timestamp.slice(11, 19)}
                  </td>
                  <td className="mono">{e.destination}</td>
                  <td>
                    <Badge
                      variant={
                        e.proxy_result.includes("DENIED")
                          ? "destructive"
                          : "secondary"
                      }
                    >
                      {e.proxy_result}
                    </Badge>
                  </td>
                </tr>
              ))}
              {items.length === 0 && (
                <tr>
                  <td colSpan={3} className="muted-text small">
                    Нет запросов
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
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
    <div className="card">
      <div className="card-body">
        {items.map((r) => (
          <div
            key={r.id}
            className="row"
            style={{
              padding: "8px 0",
              borderBottom: "1px solid var(--border)",
              alignItems: "flex-start",
            }}
          >
            <span className="num muted-text small" style={{ width: 120 }}>
              {r.timestamp.replace("T", " ").slice(0, 19)}
            </span>
            <div className="grow">
              <div>{actionLabel(r.action)}</div>
              <div className="small muted-text">{r.actor}</div>
            </div>
            {r.action.includes("draft") && (
              <Badge variant="secondary">в черновике</Badge>
            )}
          </div>
        ))}
        {items.length === 0 && (
          <div className="muted-text small">Истории нет</div>
        )}
      </div>
    </div>
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
    <section className="inspector-pane">
      <header className="inspector-head">
        <div className="row" style={{ gap: 8 }}>
          <h1 className="page-title" style={{ margin: 0 }}>
            Новый клиент
          </h1>
          {from && (
            <Badge variant="secondary">из неописанного источника {from}</Badge>
          )}
          <span className="grow" />
          <Button size="xs" variant="ghost" onClick={onCancel}>
            <X aria-hidden /> Отмена
          </Button>
        </div>
        <div className="muted-text small">
          Клиент попадёт в черновик. Трафик не изменится, пока вы не примените
          изменения.
        </div>
      </header>
      <div className="inspector-body">
        <div className="card">
          <div className="card-head">
            <span className="card-title">Кто</span>
          </div>
          <div className="card-body fgrid">
            <Label>
              Имя
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="например, build-02"
              />
            </Label>
            <Label>
              Тип
              <select className="in" defaultValue="host">
                <option value="host">Устройство</option>
                <option value="network">Сеть</option>
                <option value="group">Группа сетей</option>
              </select>
            </Label>
            <Label>
              Адреса источника
              <Textarea
                className="mono"
                rows={2}
                value={src}
                onChange={(e) => setSrc(e.target.value)}
                placeholder="10.20.0.77 или 10.20.0.0/24, по одному в строке"
              />
            </Label>
            {from && (
              <div
                className="help muted-text small"
                style={{ gridColumn: "1/-1" }}
              >
                За 24 ч с {from} были запросы, все отказаны (источник не
                описан).
              </div>
            )}
          </div>
        </div>
        <div className="card">
          <div className="card-head">
            <span className="card-title">Куда можно</span>
          </div>
          <div className="card-body">
            <div className="opts">
              <RadioOption
                active={mode === "own"}
                title="Свой список доменов"
                hint="Только перечисленные сайты"
                onClick={() => setMode("own")}
              />
              <RadioOption
                active={mode === "all"}
                title="Весь веб"
                hint="Кроме запрещённого для всех"
                onClick={() => setMode("all")}
              />
              <RadioOption
                active={mode === "none"}
                title="Пока без доступа"
                hint="Описать и решить позже"
                onClick={() => setMode("none")}
              />
            </div>
            {mode === "own" && (
              <Label>
                Домены
                <Textarea
                  className="mono"
                  rows={3}
                  value={domains}
                  onChange={(e) => setDomains(e.target.value)}
                />
              </Label>
            )}
          </div>
        </div>
        <div className="row" style={{ justifyContent: "flex-end" }}>
          {err && <span className="err">{err}</span>}
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
      </div>
    </section>
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

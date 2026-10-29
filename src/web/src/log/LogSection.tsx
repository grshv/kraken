import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Ban,
  Info,
  Pause,
  Play,
  Plus,
  Search,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import { PaneResizer } from "../shell/PaneResizer";
import { usePaneWidth } from "../shell/usePaneWidth";
import { mergeDestinations, parseDomainInput } from "../policies/domains";
import { addDomainToClientPolicy } from "../draft/edits";
import type { Destination, Event, Session, Snapshot } from "../model";
import type { NavId } from "../shell/nav";
import type { SectionProps } from "../sections/sectionProps";
import {
  buildEventsParams,
  buildFacetsParams,
  FACET_WINDOWS,
  isDenied,
  parseLogQuery,
  toEventsQuery,
  toFacetsQuery,
  type EventsParams,
  type FacetsResponse,
  type FacetWindow,
} from "./query";

const PAGE_LIMIT = 200;
const REFRESH_MS = 5000;
const DEBOUNCE_MS = 250;

// EventPage mirrors the GET /events response fields the Log screen consumes.
type EventPage = {
  items: Event[];
  next_cursor: string | null;
  gap: boolean;
  observed_at: string;
  ingestion: {
    observed_at: string;
    stale: boolean;
    source_available: boolean;
    backlog: boolean;
    dropped_records: number;
  };
};

type Explain = {
  decision: "allowed" | "denied";
  reason: string;
  client_id: string | null;
  policy_id: string | null;
};

const EXPLAIN_REASON: Record<string, string> = {
  unknown_source: "Источник не входит ни в одного клиента",
  protected_destination: "Внутренний адрес, встроенная защита",
  blocked_for_all: "Домен запрещён для всех",
  client_disabled: "Клиент выключен",
  auth_failed: "Не пройдена аутентификация прокси",
  not_in_allowlist: "Домена нет в политике клиента",
  allowed_by_policy: "Разрешён политикой клиента",
  allowed_all_web: "Разрешён: доступен весь веб",
};

// hostOf strips the port and path from a Squid destination as the prototype
// did (split(':')[0].split('/')[0]) so "example.com:443/p" -> "example.com".
function hostOf(destination: string): string {
  return destination.split(":")[0].split("/")[0];
}

// LogSection: live proxy event stream with search tokens, facets, a per-minute
// histogram and a detail pane that explains denials and edits the draft.
export function LogSection(props: SectionProps) {
  const { request, draft, active, session, onCommit, onToast, onNavigate } =
    props;
  const snap = useMemo<Snapshot | null>(
    () => draft?.snapshot ?? active ?? null,
    [draft, active],
  );

  // Client name -> id resolver sourced from the newest snapshot so newly
  // drafted clients are searchable before they are applied.
  const resolveClient = useCallback(
    (name: string): string | undefined => {
      if (!snap) return undefined;
      const c = snap.clients.find(
        (x) => x.name.toLowerCase() === name.toLowerCase(),
      );
      return c?.id;
    },
    [snap],
  );

  const [queryText, setQueryText] = useState("");
  const parsed = useMemo(() => parseLogQuery(queryText), [queryText]);
  const [window, setWindow] = useState<FacetWindow>(FACET_WINDOWS[0]);
  const [events, setEvents] = useState<Event[]>([]);
  const [facets, setFacets] = useState<FacetsResponse | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [gap, setGap] = useState(false);
  const [observedAt, setObservedAt] = useState("");
  const [stale, setStale] = useState(false);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [paused, setPaused] = useState(false);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const detail = usePaneWidth("detail");

  const query = useMemo(
    () =>
      toEventsQuery(
        buildEventsParams(parsed, resolveClient, { limit: PAGE_LIMIT }),
      ),
    [parsed, resolveClient],
  );

  // Server clock: the events response carries observed_at; the facets window
  // ends there so its buckets align with the server's view rather than wall
  // time. Before the first load falls back to the local clock.
  const now = useMemo(
    () => (observedAt ? new Date(observedAt) : new Date()),
    [observedAt],
  );

  const facetsQuery = useMemo(
    () => toFacetsQuery(buildFacetsParams(parsed, resolveClient, now, window)),
    [parsed, resolveClient, now, window],
  );

  const fetchFirst = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setLoadError(false);
      try {
        const r = await request<EventPage>(
          `/events?${query}`,
          "GET",
          undefined,
          undefined,
          signal,
        );
        setEvents(r.items ?? []);
        setCursor(r.next_cursor);
        setGap(!!r.gap);
        setObservedAt(r.observed_at ?? "");
        setStale(!!r.ingestion?.stale);
      } catch (e) {
        if ((e as Error).name === "AbortError") return;
        setLoadError(true);
      } finally {
        setLoading(false);
      }
    },
    [request, query],
  );

  const fetchFacets = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const r = await request<FacetsResponse>(
          `/events/facets?${facetsQuery}`,
          "GET",
          undefined,
          undefined,
          signal,
        );
        setFacets(r);
      } catch (e) {
        if ((e as Error).name === "AbortError") return;
        // Facets failures are non-fatal: keep the previous counts and let the
        // list's error state surface storage problems.
      }
    },
    [request, facetsQuery],
  );

  // Debounced fetch on query change; aborts the in-flight request when the
  // query changes mid-flight.
  useEffect(() => {
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      void fetchFirst(ctrl.signal);
      void fetchFacets(ctrl.signal);
    }, DEBOUNCE_MS);
    setSelectedID(null);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [fetchFirst, fetchFacets]);

  // Auto-refresh every 5s unless paused. Stops while a load is in-flight so
  // overlapping requests cannot thrash the histogram.
  useEffect(() => {
    if (paused) return;
    let live = true;
    const t = setInterval(() => {
      if (!live) return;
      void fetchFirst();
      void fetchFacets();
    }, REFRESH_MS);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [paused, fetchFirst, fetchFacets]);

  const loadMore = useCallback(async () => {
    if (!cursor) return;
    const p: EventsParams = buildEventsParams(parsed, resolveClient, {
      cursor,
      limit: PAGE_LIMIT,
    });
    try {
      const r = await request<EventPage>(`/events?${toEventsQuery(p)}`);
      setEvents((prev) => [...prev, ...(r.items ?? [])]);
      setCursor(r.next_cursor);
      setGap(!!r.gap);
    } catch {
      onToast("Не удалось подгрузить события");
    }
  }, [request, cursor, parsed, resolveClient, onToast]);

  // Client id -> display name. The facets response carries name per id from
  // the active snapshot; fall back to the draft/active snapshot the screen
  // already holds, then to event labels.
  const clientNameByID = useMemo(() => {
    const m = new Map<string, string>();
    if (facets) {
      for (const c of facets.clients) {
        if (c.id && c.name != null) m.set(c.id, c.name);
      }
    }
    for (const e of events) {
      if (e.client_id && e.client_name != null && !m.has(e.client_id)) {
        m.set(e.client_id, e.client_name);
      }
    }
    if (snap) {
      for (const c of snap.clients) {
        if (!m.has(c.id)) m.set(c.id, c.name);
      }
    }
    return m;
  }, [facets, events, snap]);
  const hist = facets?.histogram ?? [];
  const selected = events.find((e) => e.id === selectedID) ?? null;
  const maxBin = Math.max(1, ...hist.map((b) => b.allowed + b.denied));

  const clientCounts = facets?.clients ?? [];
  const methodCounts = facets?.methods ?? [];
  const allowedCount = facets?.allowed ?? 0;
  const deniedCount = facets?.denied ?? 0;

  // Clicking a client facet turns into a `client:<name>` search token, which
  // parseLogQuery + resolveClient map back to the same client_id for
  // GET /events. Replaces any existing client: token so re-clicking a different
  // client switches the filter instead of stacking.
  const applyClientFacet = useCallback(
    (id: string) => {
      const name = clientNameByID.get(id);
      if (!name) return;
      const rest = queryText.split(/\s+/).filter((t) => !/^client:/i.test(t));
      rest.push(`client:${name}`);
      setQueryText(rest.join(" "));
    },
    [clientNameByID, queryText],
  );

  // Clicking a result facet adds a `result:` token (allowed|denied), replacing
  // any existing one. Russian synonyms are accepted by parseLogQuery.
  const applyResultFacet = useCallback(
    (value: "allowed" | "denied") => {
      const rest = queryText
        .split(/\s+/)
        .filter((t) => !/^(result|результат):/i.test(t));
      rest.push(`result:${value}`);
      setQueryText(rest.join(" "));
    },
    [queryText],
  );

  // Clicking a method facet adds a `method:` token, replacing any existing one.
  const applyMethodFacet = useCallback(
    (value: string) => {
      const rest = queryText.split(/\s+/).filter((t) => !/^method:/i.test(t));
      rest.push(`method:${value}`);
      setQueryText(rest.join(" "));
    },
    [queryText],
  );

  return (
    <section
      className="log-section"
      style={{ ["--detail-w" as string]: `${detail.width}px` }}
    >
      <aside className="log-facets">
        <FacetGroup title="Результат">
          <FacetRow
            label="Разрешено"
            value={allowedCount}
            swatch="allow"
            onClick={() => applyResultFacet("allowed")}
          />
          <FacetRow
            label="Отказ"
            value={deniedCount}
            swatch="deny"
            onClick={() => applyResultFacet("denied")}
          />
        </FacetGroup>
        <FacetGroup title="Клиент">
          {clientCounts.map((c) => {
            const name = c.id ? (clientNameByID.get(c.id) ?? c.id) : "";
            return (
              <FacetRow
                key={c.id || "_unknown"}
                label={name || "Не описан"}
                value={c.count}
                onClick={c.id ? () => applyClientFacet(c.id) : undefined}
              />
            );
          })}
          {clientCounts.length === 0 && (
            <div className="muted-text small">—</div>
          )}
        </FacetGroup>
        <FacetGroup title="Метод">
          {methodCounts.map((m) => (
            <FacetRow
              key={m.method || "_"}
              label={m.method || "—"}
              value={m.count}
              onClick={m.method ? () => applyMethodFacet(m.method) : undefined}
            />
          ))}
          {methodCounts.length === 0 && (
            <div className="muted-text small">—</div>
          )}
        </FacetGroup>
      </aside>

      <div className="log-main">
        <div className="log-head">
          <div className="search-input grow">
            <Search aria-hidden />
            <Input
              aria-label="Поиск по журналу"
              placeholder="client:ci-01 host:example.com результат:отказ"
              value={queryText}
              onChange={(e) => setQueryText(e.target.value)}
            />
          </div>
          <div
            className="log-window-toggle"
            role="group"
            aria-label="Окно журнала"
          >
            {FACET_WINDOWS.map((w) => (
              <Button
                key={w.label}
                size="xs"
                variant={w.label === window.label ? "secondary" : "ghost"}
                onClick={() => setWindow(w)}
              >
                {w.label}
              </Button>
            ))}
          </div>
          {paused ? (
            <Button size="xs" onClick={() => setPaused(false)}>
              <Play aria-hidden /> Продолжить
            </Button>
          ) : (
            <Button size="xs" variant="ghost" onClick={() => setPaused(true)}>
              <Pause aria-hidden /> Пауза
            </Button>
          )}
          {!paused && (
            <Badge variant="secondary">
              <span className="status-dot up" /> обновляется
            </Badge>
          )}
          {stale && (
            <Badge variant="secondary">
              <TriangleAlert aria-hidden /> журнал не свежий
            </Badge>
          )}
          {gap && (
            <Badge variant="secondary">
              <TriangleAlert aria-hidden /> есть пропуски
            </Badge>
          )}
        </div>

        <div className="log-chart">
          <div className="legend">
            <span>
              <i style={{ background: "var(--s-allow)" }} /> разрешено
            </span>
            <span>
              <i style={{ background: "var(--s-deny)" }} /> отказ
            </span>
            <span className="muted-text">
              {window.step === "1m"
                ? "по минутам"
                : window.step === "15m"
                  ? "по 15 минут"
                  : "по часам"}
            </span>
          </div>
          <div
            className="log-bars"
            role="img"
            aria-label="Гистограмма запросов"
          >
            {hist.map((b) => {
              const total = b.allowed + b.denied;
              const h = total === 0 ? 0 : (total / maxBin) * 100;
              const denyH = total === 0 ? 0 : (b.denied / total) * h;
              const label =
                window.step === "1h"
                  ? b.start.slice(0, 16)
                  : b.start.slice(11, 16);
              return (
                <div
                  key={b.start}
                  className="log-bar-col"
                  style={{ height: `${h}%` }}
                  title={`${label}: ${b.allowed} разрешено, ${b.denied} отказ`}
                >
                  <div
                    className="log-bar-denied"
                    style={{ height: `${denyH}%` }}
                  />
                </div>
              );
            })}
          </div>
        </div>

        <div className="log-table-wrap">
          {loadError ? (
            <div className="inspector-body">
              <ErrorState
                title="Журнал недоступен"
                detail="Не удалось загрузить события. Повторяем запрос…"
                onRetry={() => void fetchFirst()}
              />
            </div>
          ) : events.length === 0 ? (
            <div className="inspector-body">
              <EmptyState icon={<Search />} title="Запросов нет">
                По текущему фильтру нет событий. Как только машины обратятся к
                прокси, записи появятся здесь.
              </EmptyState>
            </div>
          ) : (
            <table className="log-table">
              <thead>
                <tr>
                  <th>Время</th>
                  <th>Клиент</th>
                  <th>Назначение</th>
                  <th>Результат</th>
                </tr>
              </thead>
              <tbody>
                {events.map((e) => {
                  const denied = isDenied(e);
                  return (
                    <tr
                      key={e.id}
                      className={selectedID === e.id ? "selected" : undefined}
                      onClick={() => setSelectedID(e.id)}
                    >
                      <td className="num muted-text small">
                        {e.timestamp.slice(11, 19)}
                      </td>
                      <td>
                        <div style={{ fontWeight: 500 }}>
                          {e.client_name ?? "не описан"}
                        </div>
                        <div className="mono muted-text small">
                          {e.source_ip}
                        </div>
                      </td>
                      <td className="mono log-dest">
                        {e.method !== "CONNECT" && (
                          <span className="muted-text">{e.method} </span>
                        )}
                        {e.destination}
                      </td>
                      <td>
                        <Badge variant={denied ? "destructive" : "secondary"}>
                          {e.proxy_result}
                          {denied ? " отказ" : ""}
                        </Badge>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
          {cursor && !loadError && (
            <div
              className="row"
              style={{ justifyContent: "center", padding: 12 }}
            >
              <Button size="xs" variant="ghost" onClick={loadMore}>
                Ещё
              </Button>
            </div>
          )}
        </div>
      </div>

      <aside className="log-detail-pane">
        <PaneResizer
          kind="detail"
          direction="left"
          onDrag={detail.onDrag}
          onCommit={detail.onCommit}
          onReset={detail.reset}
        />
        {selected ? (
          <LogDetail
            event={selected}
            snap={snap}
            session={session}
            request={request}
            onCommit={onCommit}
            onToast={onToast}
            onNavigate={onNavigate}
          />
        ) : (
          <div className="muted-text small">
            Выберите запрос, чтобы увидеть детали и действия.
          </div>
        )}
      </aside>
    </section>
  );
}

function FacetGroup({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="facet-group">
      <h4>{title}</h4>
      {children}
    </div>
  );
}

function FacetRow({
  label,
  value,
  swatch,
  onClick,
}: {
  label: string;
  value: number;
  swatch?: "allow" | "deny";
  // When set, the row becomes a button that applies a search filter.
  onClick?: () => void;
}) {
  const Comp = onClick ? "button" : "div";
  return (
    <Comp
      className="facet-item"
      onClick={onClick}
      type={onClick ? "button" : undefined}
      aria-label={onClick ? `Фильтровать по «${label}»` : undefined}
    >
      {swatch === "allow" && (
        <i className="facet-swatch" style={{ background: "var(--s-allow)" }} />
      )}
      {swatch === "deny" && (
        <i className="facet-swatch" style={{ background: "var(--s-deny)" }} />
      )}
      <span className="grow ellipsis">{label}</span>
      <span className="num muted-text">{value}</span>
    </Comp>
  );
}

// LogDetail loads an explanation for denials and offers the same draft edits
// the prototype's allow/new-from/block-dom handlers did.
function LogDetail({
  event,
  snap,
  session,
  request,
  onCommit,
  onToast,
  onNavigate,
}: {
  event: Event;
  snap: Snapshot | null;
  session: Session;
  request: SectionProps["request"];
  onCommit: (
    next: Snapshot,
  ) => Promise<{ ok: boolean; code?: string; message?: string }>;
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
}) {
  const denied = isDenied(event);
  const [explain, setExplain] = useState<Explain | null>(null);
  const [explainFailed, setExplainFailed] = useState(false);

  useEffect(() => {
    if (!denied) {
      setExplain(null);
      setExplainFailed(false);
      return;
    }
    let live = true;
    setExplain(null);
    setExplainFailed(false);
    request<Explain>(
      `/inspector/events/${encodeURIComponent(event.id)}/explain`,
    )
      .then((r) => {
        if (live) setExplain(r);
      })
      .catch(() => {
        if (live) setExplainFailed(true);
      });
    return () => {
      live = false;
    };
  }, [event.id, denied, request]);

  const host = hostOf(event.destination);
  const canConfigure = session.permissions?.includes("configure") ?? false;
  const allowDomain =
    denied &&
    !!event.client_id &&
    explain?.reason === "not_in_allowlist" &&
    !!snap;
  const describeSource = denied && !event.client_id;
  const blockForAll =
    !denied && canConfigure && !!snap && parseDomainInput(host) !== null;

  async function allowHost() {
    if (!snap || !event.client_id) return;
    const res = await onCommit(
      addDomainToClientPolicy(snap, event.client_id, host),
    );
    if (res.ok) onToast(`Домен ${host} добавлен в политику`);
    else if (res.code !== "revision_conflict")
      onToast(res.message ?? "Не удалось изменить черновик");
  }

  async function blockHost() {
    if (!snap) return;
    const entry = parseDomainInput(host);
    if (!entry) {
      onToast(`«${host}» не похож на домен`);
      return;
    }
    const blocked: Destination[] = snap.settings?.blocked_domains ?? [];
    const next: Snapshot = {
      ...snap,
      settings: {
        ...(snap.settings ?? {
          http_port: 3128,
          tls_port: 3129,
          safe_ports: [80, 443],
          connect_ports: [443],
          cache_enabled: false,
          cache_memory_mb: 64,
          maximum_object_kb: 512,
          dns_nameservers: [],
          positive_dns_ttl_seconds: 21600,
          negative_dns_ttl_seconds: 60,
          connect_timeout_seconds: 60,
          read_timeout_seconds: 900,
          request_timeout_seconds: 300,
          max_request_body_mb: 0,
          log_rotations: 3,
          allow_private_destinations: false,
        }),
        blocked_domains: mergeDestinations(blocked, [
          { ...entry, include_subdomains: true },
        ]),
      },
    };
    const res = await onCommit(next);
    if (res.ok) onToast(`Домен ${host} запрещён для всех в черновике`);
    else if (res.code !== "revision_conflict")
      onToast(res.message ?? "Не удалось изменить черновик");
  }

  return (
    <>
      <div style={{ fontWeight: 600 }}>Запрос</div>
      <dl className="kv-row">
        <dt>Время</dt>
        <dd className="num">
          {event.timestamp.replace("T", " ").slice(0, 19)}
        </dd>
        <dt>Источник</dt>
        <dd>
          {event.client_name && (
            <>
              <button className="link" onClick={() => onNavigate("clients")}>
                {event.client_name}
              </button>{" "}
            </>
          )}
          <span className="mono">{event.source_ip}</span>
        </dd>
        <dt>Запрос</dt>
        <dd className="mono">
          {event.method} {event.destination}
        </dd>
        <dt>Результат</dt>
        <dd>
          <Badge variant={denied ? "destructive" : "secondary"}>
            {event.proxy_result}
            {denied ? " отказ" : ""}
          </Badge>
        </dd>
        {event.revision_id && (
          <>
            <dt>Ревизия</dt>
            <dd className="mono">{event.revision_id.slice(0, 8)}</dd>
          </>
        )}
      </dl>

      {denied ? (
        <div className="callout callout-bad">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <span style={{ fontWeight: 500 }}>Почему отказано</span>
            <span className="muted-text small">по текущим правилам</span>
          </div>
          {explainFailed ? (
            <div className="small muted-text">
              Не удалось получить объяснение. Возможно, событие уже устарело.
            </div>
          ) : explain ? (
            <div className="small">
              {EXPLAIN_REASON[explain.reason] ?? explain.reason}
            </div>
          ) : (
            <div className="small muted-text">Загружаем…</div>
          )}
          <div className="callout-actions">
            {allowDomain && (
              <Button size="xs" onClick={allowHost}>
                <Plus aria-hidden /> Разрешить домен
              </Button>
            )}
            {describeSource && (
              <Button
                size="xs"
                onClick={() => {
                  onNavigate("clients");
                  onToast(
                    `Выберите ${event.source_ip} в списке неописанных источников`,
                  );
                }}
              >
                <Plus aria-hidden /> Описать IP как клиента
              </Button>
            )}
            {!allowDomain &&
              !describeSource &&
              explain?.reason !== "blocked_for_all" && (
                <span className="muted-text small">
                  Это встроенный запрет, разрешить нельзя
                </span>
              )}
            {explain?.reason === "blocked_for_all" && (
              <span className="muted-text small">
                Домен уже запрещён для всех
              </span>
            )}
          </div>
        </div>
      ) : (
        <div className="callout callout-info">
          <Info aria-hidden />
          <div className="grow small">
            Запрос разрешён действующей политикой.
          </div>
          {blockForAll && (
            <Button size="xs" variant="ghost" onClick={blockHost}>
              <Ban aria-hidden /> Запретить домен для всех
            </Button>
          )}
        </div>
      )}
    </>
  );
}

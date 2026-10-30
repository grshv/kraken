import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Ban,
  Check,
  Info,
  Pause,
  Play,
  Plus,
  Search,
  TriangleAlert,
  X,
} from "lucide-react";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Callout, KeyValue } from "../components/kraken/blocks";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import { PaneResizer } from "../shell/PaneResizer";
import { usePaneWidth } from "../shell/usePaneWidth";
import { explainSteps, type Explain } from "./explain";
import { mergeDestinations, parseDomainInput } from "../policies/domains";
import { addDomainToClientPolicy } from "../draft/edits";
import type { Destination, Event, Session, Snapshot } from "../model";
import { localTime } from "../model";
import type { NavId } from "../shell/nav";
import type { SectionProps } from "../sections/sectionProps";
import {
  buildEventsParams,
  buildFacetsParams,
  DENY_REASONS,
  denyReasonLabel,
  SITE_ERRORS,
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

// hostOf strips the port and path from a Squid destination as the prototype
// did (split(':')[0].split('/')[0]) so "example.com:443/p" -> "example.com".
function hostOf(destination: string): string {
  return destination.split(":")[0].split("/")[0];
}

// resultCode extracts the HTTP status code shown in the events table: prefer
// the structured proxy_http_status field, fall back to the trailing number in
// proxy_result ("TCP_DENIED/403" -> "403"). The full proxy_result is still
// shown in the request detail pane.
function resultCode(e: Event): string {
  if (e.proxy_http_status != null) return String(e.proxy_http_status);
  const m = (e.proxy_result || "").match(/(\d{3})\b/);
  return m ? m[1] : e.proxy_result || "—";
}

// LogSection: live proxy event stream with search tokens, facets, a per-minute
// histogram and a detail pane that explains denials and edits the draft.
export function LogSection(props: SectionProps) {
  const {
    request,
    draft,
    active,
    session,
    onCommit,
    onToast,
    onNavigate,
    onOpen,
  } = props;
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

  // The list starts where the facet window starts, fixed when the window or
  // the query changes so polling does not refetch on every tick.
  const from = useMemo(
    () => new Date(Date.now() - window.minutes * 60000).toISOString(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [window, parsed],
  );
  const query = useMemo(
    () =>
      toEventsQuery(
        buildEventsParams(parsed, resolveClient, { limit: PAGE_LIMIT, from }),
      ),
    [parsed, resolveClient, from],
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

  // Debounced fetch on query or window change; aborts the in-flight request
  // when they change mid-flight. The latest fetchers are read through a ref:
  // fetchFacets changes with the server clock on every poll, which must not
  // trigger a second load.
  const fetchers = useRef({ fetchFirst, fetchFacets });
  fetchers.current = { fetchFirst, fetchFacets };
  useEffect(() => {
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      void fetchers.current.fetchFirst(ctrl.signal);
      void fetchers.current.fetchFacets(ctrl.signal);
    }, DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [query, window]);

  // A new query or window starts a new selection; polling (which moves the
  // facet window with the server clock) keeps the selected row.
  useEffect(() => {
    setSelectedID(null);
  }, [query]);

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
      from,
    });
    try {
      const r = await request<EventPage>(`/events?${toEventsQuery(p)}`);
      setEvents((prev) => [...prev, ...(r.items ?? [])]);
      setCursor(r.next_cursor);
      setGap(!!r.gap);
    } catch {
      onToast("Не удалось подгрузить события");
    }
  }, [request, cursor, parsed, resolveClient, from, onToast]);

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

  // Clicking a client facet toggles a `client:<name>` search token, which
  // parseLogQuery + resolveClient map back to the same client_id for
  // GET /events. A single client: token is kept, so re-clicking a different
  // client switches the filter; re-clicking the active one clears it.
  const applyClientFacet = useCallback(
    (id: string) => {
      const name = clientNameByID.get(id);
      if (!name) return;
      const rest = queryText.split(/\s+/).filter((t) => !/^client:/i.test(t));
      if (parsed.client?.toLowerCase() !== name.toLowerCase())
        rest.push(`client:${name}`);
      setQueryText(rest.join(" "));
    },
    [clientNameByID, queryText, parsed.client],
  );

  // Clicking a result facet toggles a `result:` token (allowed|denied).
  // Russian synonyms are accepted by parseLogQuery.
  const applyResultFacet = useCallback(
    (value: "allowed" | "denied") => {
      const rest = queryText
        .split(/\s+/)
        .filter((t) => !/^(result|результат):/i.test(t));
      if (parsed.result !== value) rest.push(`result:${value}`);
      setQueryText(rest.join(" "));
    },
    [queryText, parsed.result],
  );

  // Clicking a method facet toggles a `method:` token.
  const applyReasonFacet = useCallback(
    (value: string) => {
      const rest = queryText
        .split(/\s+/)
        .filter((t) => !/^(reason|причина):/i.test(t));
      if (parsed.reason !== value) rest.push(`reason:${value}`);
      setQueryText(rest.join(" "));
    },
    [queryText, parsed.reason],
  );

  const applyMethodFacet = useCallback(
    (value: string) => {
      const rest = queryText.split(/\s+/).filter((t) => !/^method:/i.test(t));
      if (parsed.method?.toLowerCase() !== value.toLowerCase())
        rest.push(`method:${value}`);
      setQueryText(rest.join(" "));
    },
    [queryText, parsed.method],
  );

  // Keep the first row selected when the list refreshes with nothing picked, so
  // the detail pane is populated on first load and after every query change
  // (the debounced-fetch effect clears selectedID). Matches the prototype.
  // Closing the pane keeps it closed until a row is picked again.
  const [detailClosed, setDetailClosed] = useState(false);
  useEffect(() => {
    if (selectedID === null && events.length > 0 && !detailClosed) {
      setSelectedID(events[0].id);
    }
  }, [events, selectedID, detailClosed]);

  return (
    <section className="flex min-h-0 w-full flex-1 flex-col">
      <div className="flex h-[52px] flex-none items-center gap-2 border-b px-3.5">
        <span className="font-semibold">Журнал</span>
        {!paused && (
          <Badge variant="ok">
            <span className="size-1.5 rounded-full bg-current" /> обновляется
          </Badge>
        )}
        <div className="relative flex min-w-0 max-w-[520px] flex-1 items-center">
          <Search
            aria-hidden
            className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
          />
          <Input
            aria-label="Поиск по журналу"
            className="h-8 pl-[30px]"
            placeholder="client:ci-01 host:example.com результат:отказ"
            value={queryText}
            onChange={(e) => setQueryText(e.target.value)}
          />
        </div>
        <ToggleGroup
          type="single"
          variant="seg"
          size="sm"
          spacing={0.5}
          className="rounded-lg bg-muted p-[3px]"
          value={window.label}
          aria-label="Окно журнала"
          onValueChange={(v) => {
            const w = FACET_WINDOWS.find((x) => x.label === v);
            if (w) setWindow(w);
          }}
        >
          {FACET_WINDOWS.map((w) => (
            <ToggleGroupItem key={w.label} value={w.label}>
              {w.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <span className="grow" />
        {paused ? (
          <Button variant="outline" size="sm" onClick={() => setPaused(false)}>
            <Play aria-hidden /> Продолжить
          </Button>
        ) : (
          <Button variant="outline" size="sm" onClick={() => setPaused(true)}>
            <Pause aria-hidden /> Пауза
          </Button>
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
      <div
        className="grid min-h-0 w-full flex-1 [grid-template-columns:190px_minmax(0,1fr)_var(--detail-w,400px)] max-[1180px]:[grid-template-columns:minmax(0,1fr)_min(var(--detail-w,400px),320px)] max-[820px]:grid-cols-[1fr] max-[820px]:grid-rows-[auto_auto_auto]"
        style={{
          ["--detail-w" as string]: `${detailClosed ? 0 : detail.width}px`,
        }}
      >
        <aside className="flex w-[190px] min-h-0 flex-col gap-3.5 overflow-y-auto border-r bg-card p-3 max-[1180px]:hidden">
          <FacetGroup title="Результат">
            <FacetRow
              label="Разрешено"
              value={allowedCount}
              checked={parsed.result === "allowed"}
              onClick={() => applyResultFacet("allowed")}
            />
            <FacetRow
              label="Отказ"
              value={deniedCount}
              checked={parsed.result === "denied"}
              onClick={() => applyResultFacet("denied")}
            />
          </FacetGroup>
          <FacetGroup title="Клиент">
            {clientCounts.map((c) => {
              const name = c.id ? (clientNameByID.get(c.id) ?? c.id) : "";
              const on = c.id ? () => applyClientFacet(c.id) : undefined;
              return (
                <FacetRow
                  key={c.id || "_unknown"}
                  label={name || "Не описан"}
                  value={c.count}
                  checked={
                    !!name &&
                    parsed.client?.toLowerCase() === name.toLowerCase()
                  }
                  onClick={on}
                />
              );
            })}
            {clientCounts.length === 0 && (
              <div className="text-sm text-muted-foreground">—</div>
            )}
          </FacetGroup>
          <FacetGroup title="Метод">
            {methodCounts.map((m) => {
              const on = m.method
                ? () => applyMethodFacet(m.method)
                : undefined;
              return (
                <FacetRow
                  key={m.method || "_"}
                  label={m.method || "—"}
                  value={m.count}
                  checked={
                    !!m.method &&
                    parsed.method?.toLowerCase() === m.method.toLowerCase()
                  }
                  onClick={on}
                />
              );
            })}
            {methodCounts.length === 0 && (
              <div className="text-sm text-muted-foreground">—</div>
            )}
          </FacetGroup>
          <FacetGroup title="Причина отказа">
            {(facets?.deny_reasons ?? []).map((r) => (
              <FacetRow
                key={r.reason}
                label={denyReasonLabel(r.reason)}
                value={r.count}
                checked={parsed.reason === r.reason}
                onClick={
                  r.reason in DENY_REASONS
                    ? () => applyReasonFacet(r.reason)
                    : undefined
                }
              />
            ))}
            {!facets?.deny_reasons?.length && (
              <div className="text-sm text-muted-foreground">—</div>
            )}
          </FacetGroup>
          <FacetGroup title="Ошибка сайта">
            {(facets?.site_errors ?? []).map((r) => (
              <FacetRow
                key={r.class}
                label={SITE_ERRORS[r.class] ?? r.class}
                value={r.count}
                checked={false}
              />
            ))}
            {!facets?.site_errors?.length && (
              <div className="text-sm text-muted-foreground">—</div>
            )}
          </FacetGroup>
        </aside>

        <div className="flex min-h-0 min-w-0 flex-col">
          <div className="flex-none border-b p-2.5">
            <div className="mb-1.5 flex gap-3 text-sm text-muted-foreground">
              <span className="flex items-center gap-1">
                <span
                  className="size-2.5 rounded-[2px] bg-s-allow"
                  aria-hidden
                />
                разрешено
              </span>
              <span className="flex items-center gap-1">
                <span
                  className="size-2.5 rounded-[2px] bg-s-deny"
                  aria-hidden
                />
                отказ
              </span>
              <span>
                {window.step === "1m"
                  ? "по минутам"
                  : window.step === "15m"
                    ? "по 15 минут"
                    : "по часам"}
              </span>
            </div>
            <div
              className="flex h-12 items-end gap-px"
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
                    className="relative min-w-px flex-1 rounded-t-sm bg-s-allow/70"
                    style={{ height: `${h}%` }}
                    title={`${label}: ${b.allowed} разрешено, ${b.denied} отказ`}
                  >
                    <div
                      className="absolute inset-x-0 top-0 rounded-t-sm bg-s-deny/95"
                      style={{ height: `${denyH}%` }}
                    />
                  </div>
                );
              })}
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-auto">
            {loadError ? (
              <div className="flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-5">
                <ErrorState
                  title="Журнал недоступен"
                  detail="Не удалось загрузить события. Повторяем запрос…"
                  onRetry={() => void fetchFirst()}
                />
              </div>
            ) : events.length === 0 ? (
              <div className="flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-5">
                <EmptyState icon={<Search />} title="Запросов нет">
                  За {window.label} по текущему фильтру событий нет. Расширьте
                  окно справа от поиска или дождитесь новых запросов.
                </EmptyState>
              </div>
            ) : (
              <Table className="text-base">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="sticky top-0 h-auto bg-card px-3 py-2 text-sm text-muted-foreground">
                      Время
                    </TableHead>
                    <TableHead className="sticky top-0 h-auto bg-card px-3 py-2 text-sm text-muted-foreground">
                      Клиент
                    </TableHead>
                    <TableHead className="sticky top-0 h-auto bg-card px-3 py-2 text-sm text-muted-foreground">
                      Назначение
                    </TableHead>
                    <TableHead className="sticky top-0 h-auto bg-card px-3 py-2 text-sm text-muted-foreground">
                      Результат
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {events.map((e) => {
                    const denied = isDenied(e);
                    return (
                      <TableRow
                        key={e.id}
                        data-state={
                          selectedID === e.id ? "selected" : undefined
                        }
                        className="cursor-pointer data-[state=selected]:bg-sel data-[state=selected]:shadow-[inset_2px_0_0_var(--accent)]"
                        onClick={() => {
                          setDetailClosed(false);
                          setSelectedID(e.id);
                        }}
                      >
                        <TableCell className="px-3 py-2 tabular-nums text-sm text-muted-foreground">
                          {localTime(e.timestamp)}
                        </TableCell>
                        <TableCell className="px-3 py-2">
                          <div className="font-medium">
                            {e.client_name ?? "не описан"}
                          </div>
                          <div className="font-mono text-sm text-muted-foreground">
                            {e.source_ip}
                          </div>
                        </TableCell>
                        <TableCell className="px-3 py-2 font-mono [overflow-wrap:anywhere]">
                          {e.method !== "CONNECT" && (
                            <span className="text-muted-foreground">
                              {e.method}{" "}
                            </span>
                          )}
                          {e.destination}
                        </TableCell>
                        <TableCell className="px-3 py-2">
                          <Badge variant={denied ? "bad" : "ok"}>
                            {resultCode(e)}
                            {denied ? " отказ" : ""}
                          </Badge>
                          {e.count && e.count > 1 ? (
                            <span className="ml-1 tabular-nums text-sm text-muted-foreground">
                              ×{e.count}
                            </span>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
            {cursor && !loadError && (
              <div className="flex justify-center p-3">
                <Button size="xs" variant="ghost" onClick={loadMore}>
                  Ещё
                </Button>
              </div>
            )}
          </div>
        </div>

        <aside className={cn("log-detail-pane", detailClosed && "hidden")}>
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
              onOpen={onOpen}
              onClose={() => {
                setDetailClosed(true);
                setSelectedID(null);
              }}
            />
          ) : (
            <div className="text-sm text-muted-foreground">
              Выберите запрос, чтобы увидеть детали и действия.
            </div>
          )}
        </aside>
      </div>
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
    <div className="flex flex-col gap-1">
      <h4 className="mb-1 text-sm font-medium text-muted-foreground">
        {title}
      </h4>
      {children}
    </div>
  );
}

function FacetRow({
  label,
  value,
  checked,
  onClick,
}: {
  label: string;
  value: number;
  checked?: boolean;
  // When set, the row becomes a toggle that applies/clears a search filter.
  onClick?: () => void;
}) {
  const interactive = !!onClick;
  return (
    <div
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      className={cn(
        "flex w-full items-center gap-2 py-px text-left text-base",
        interactive && "cursor-pointer",
      )}
      onClick={onClick}
      aria-label={interactive ? `Фильтровать по «${label}»` : undefined}
      aria-pressed={interactive ? checked : undefined}
    >
      <Checkbox
        checked={checked}
        aria-hidden
        tabIndex={-1}
        className="pointer-events-none"
      />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      <span className="tabular-nums text-sm text-muted-foreground">
        {value}
      </span>
    </div>
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
  onOpen,
  onClose,
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
  onOpen?: SectionProps["onOpen"];
  onClose?: () => void;
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

  const kvItems: [React.ReactNode, React.ReactNode][] = [
    [
      "Время",
      <span className="tabular-nums">
        {new Date(event.timestamp).toLocaleString("ru-RU", {
          day: "2-digit",
          month: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        })}
      </span>,
    ],
    [
      "Источник",
      <span>
        {event.client_name && (
          <>
            <button
              type="button"
              className="font-medium text-accent hover:underline"
              onClick={() => onNavigate("clients")}
            >
              {event.client_name}
            </button>{" "}
          </>
        )}
        <span className="font-mono">{event.source_ip}</span>
      </span>,
    ],
    [
      "Запрос",
      <span className="font-mono">
        {event.method} {event.destination}
      </span>,
    ],
    [
      "Результат",
      <Badge variant={denied ? "bad" : "secondary"}>
        {event.proxy_result}
        {denied ? " отказ" : ""}
      </Badge>,
    ],
  ];
  if (event.count != null && event.count > 1) {
    kvItems.push([
      "Повторов",
      <span className="tabular-nums">{event.count}</span>,
    ]);
  }
  if (event.revision_id) {
    kvItems.push([
      "Ревизия",
      <span className="font-mono">{event.revision_id.slice(0, 8)}</span>,
    ]);
  }

  return (
    <>
      <div className="flex items-center justify-between">
        <span className="font-semibold">Запрос</span>
        {onClose && (
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Закрыть панель"
            onClick={onClose}
          >
            <X aria-hidden />
          </Button>
        )}
      </div>
      <KeyValue items={kvItems} />

      {denied ? (
        <>
          <div className="rounded-lg border bg-card px-3.5 py-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <span className="font-medium">Почему отказано</span>
              <span className="text-sm text-muted-foreground">
                по текущим правилам
              </span>
            </div>
            {explainFailed ? (
              <div className="text-sm text-muted-foreground">
                Не удалось получить объяснение. Возможно, событие уже устарело.
              </div>
            ) : explain ? (
              <>
                <ul className="flex flex-col gap-1.5">
                  {explainSteps(explain, event, snap).map((st, i) => (
                    <li
                      key={i}
                      className="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2"
                    >
                      {st.ok ? (
                        <Check
                          aria-label="пройдено"
                          className="mt-0.5 size-3.5 text-ok"
                        />
                      ) : (
                        <X
                          aria-label="отказ"
                          className="mt-0.5 size-3.5 text-bad"
                        />
                      )}
                      <span className="[overflow-wrap:anywhere]">
                        {st.text}
                      </span>
                    </li>
                  ))}
                </ul>
                {explain.decision === "allowed" && (
                  <div className="mt-2.5 text-sm text-muted-foreground">
                    Сейчас такой запрос разрешён. Отказ был по правилам на
                    момент запроса или из-за адреса назначения: если DNS вернул
                    внутренний адрес, его закрывает встроенная защита.
                  </div>
                )}
              </>
            ) : (
              <div className="text-sm text-muted-foreground">Загружаем…</div>
            )}
          </div>
          {allowDomain && (
            <div className="flex flex-col gap-1.5">
              <Button onClick={allowHost}>
                <Plus aria-hidden /> Разрешить домен
              </Button>
              <span className="text-sm text-muted-foreground">
                Добавится в политику «
                {snap?.policies.find((p) => p.id === explain?.policy_id)
                  ?.name ?? "клиента"}
                »
              </span>
            </div>
          )}
          {describeSource && (
            <Button
              onClick={() =>
                onOpen
                  ? onOpen("clients", `source:${event.source_ip}`)
                  : onNavigate("clients")
              }
            >
              <Plus aria-hidden /> Описать IP как клиента
            </Button>
          )}
          {explain?.reason === "blocked_for_all" && (
            <span className="text-sm text-muted-foreground">
              Домен запрещён для всех, это меняется в «Настройках»
            </span>
          )}
        </>
      ) : (
        <Callout
          tone="info"
          icon={<Info aria-hidden />}
          actions={
            blockForAll ? (
              <Button size="xs" variant="ghost" onClick={blockHost}>
                <Ban aria-hidden /> Запретить домен для всех
              </Button>
            ) : undefined
          }
        >
          <span className="text-sm">
            Запрос разрешён действующей политикой.
          </span>
        </Callout>
      )}
    </>
  );
}

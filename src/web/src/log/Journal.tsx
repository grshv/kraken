import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ArrowRight,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Pause,
  Play,
  Plus,
  Search,
  X,
} from "lucide-react";
import { useDefaultLayout } from "react-resizable-panels";
import { cn } from "cn";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { KeyValue } from "@/components/kraken/blocks";
import {
  DetailPane,
  InspectorPane,
  ListPane,
  ListPaneBody,
  ListPaneHeader,
} from "@/components/kraken/layout";
import { Timestamp } from "@/components/kraken/timestamp";
import { ErrorState } from "../shell/ErrorState";
import {
  addDomainToClient,
  addDomainsToPolicy,
  blockDomainsForAll,
  clientAllows,
} from "../draft/edits";
import type { Client, Event, Snapshot } from "../model";
import { compactPeriod, dateTime, pluralRu, revisionLabel } from "../model";
import { managerScope, type SectionProps } from "../sections/sectionProps";
import { DateRangePicker } from "./DateRangePicker";
import { TrafficSummary } from "./TrafficSummary";
import {
  formatBytes,
  formatCount,
  hostOf,
  portOf,
  useQueries,
  type DomainRow,
  type DomainsReport,
  type EventPage,
} from "./data";
import { whyText, type Explain } from "./explain";
import {
  PERIODS,
  defaultFilters,
  hashFor,
  isLive,
  logIntent,
  periodWindow,
  shiftWindow,
  stateFromHash,
  toQuery,
  type FilterKey,
  type Filters,
  type JournalState,
  type View,
} from "./filters";
import {
  DENY_REASONS,
  denyReasonLabel,
  isDenied,
  type FacetsResponse,
} from "./query";

const PAGE_LIMIT = 200;
const REFRESH_MS = 5000;
const DEBOUNCE_MS = 250;

type JournalProps = Pick<
  SectionProps,
  | "request"
  | "active"
  | "session"
  | "onCommit"
  | "onToast"
  | "onOpen"
  | "onNavigate"
> & {
  // The newest rules: the draft, else the active snapshot.
  snap: Snapshot | null;
  // A client's tab: the journal narrowed to one client, without the filter
  // pane and the summary, with a link to the full journal.
  clientId?: string;
  // Opening state: an intent from another section or the client tab's view.
  initial?: JournalState;
};

// Journal: requests of the period by request or grouped by domain, with
// facets, a summary and a detail pane. Filters live in the URL hash so a view
// can be reloaded or shared.
export function Journal(props: JournalProps) {
  const { request, snap, active, session, clientId, initial, onOpen } = props;
  const [state, setState] = useState<JournalState>(() => {
    if (initial) return initial;
    if (!clientId) {
      const fromHash = stateFromHash(location.hash);
      if (fromHash) return fromHash;
    }
    return { view: "events", filters: defaultFilters() };
  });
  const { view, filters: f } = state;
  const [selected, setSelected] = useState<Event | null>(null);
  const [host, setHost] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [clientSearch, setClientSearch] = useState("");
  const [tick, setTick] = useState(0);
  const [now, setNow] = useState(() => new Date());
  const [more, setMore] = useState<{ items: Event[]; cursor: string | null }>({
    items: [],
    cursor: null,
  });

  // A new opening state (intent, client tab link) replaces the current one.
  useEffect(() => {
    if (!initial) return;
    setState(initial);
    setSelected(null);
    setHost(null);
  }, [initial]);

  // The journal keeps its state in the hash; the client tab does not.
  useEffect(() => {
    if (clientId) return;
    try {
      history.replaceState(null, "", hashFor(state));
    } catch {
      // history may be unavailable
    }
  }, [state, clientId]);
  useEffect(() => {
    if (clientId) return;
    return () => {
      try {
        history.replaceState(null, "", location.pathname + location.search);
      } catch {
        // history may be unavailable
      }
    };
  }, [clientId]);

  // Typing in the search box settles before it reaches the server.
  const [queried, setQueried] = useState(f);
  useEffect(() => {
    const t = setTimeout(() => {
      setQueried(f);
      setNow(new Date());
    }, DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [f]);

  // Presets slide with the clock: every refresh moves "now".
  useEffect(() => {
    if (paused) return;
    const t = setInterval(() => {
      setNow(new Date());
      setTick((n) => n + 1);
    }, REFRESH_MS);
    return () => clearInterval(t);
  }, [paused]);

  const q = (opts: { except?: FilterKey; step?: boolean } = {}) =>
    toQuery(queried, now, { ...opts, clientID: clientId }).toString();
  const journal = !clientId;
  const slots: Record<string, string | null> = {
    events: view === "events" ? `/events?${q()}&limit=${PAGE_LIMIT}` : null,
    domains: view === "domains" ? `/events/domains?${q()}` : null,
    summary: journal ? `/events/domains?${q({ except: "result" })}` : null,
  };
  const facetSlots: Record<string, string | null> = journal
    ? {
        result: `/events/facets?${q({ except: "result", step: true })}`,
        clients: `/events/facets?${q({ except: "clients", step: true })}`,
        methods: `/events/facets?${q({ except: "methods", step: true })}`,
        reasons: `/events/facets?${q({ except: "reasons", step: true })}`,
      }
    : {};
  const main = useQueries<EventPage | DomainsReport>(request, slots, tick);
  const facets = useQueries<FacetsResponse>(request, facetSlots, tick);
  const page = main.data.events as EventPage | undefined;
  const report = main.data.domains as DomainsReport | undefined;
  const summary = main.data.summary as DomainsReport | undefined;

  // Extra pages belong to one query; a new query starts from the first page.
  const pageQuery = slots.events?.replace(/&?(from|to)=[^&]*/g, "");
  useEffect(() => {
    setMore({ items: [], cursor: null });
  }, [pageQuery]);
  const items = useMemo(() => {
    const first = page?.items ?? [];
    const seen = new Set(first.map((e) => e.id));
    return [...first, ...more.items.filter((e) => !seen.has(e.id))];
  }, [page, more.items]);
  const cursor = more.items.length ? more.cursor : (page?.next_cursor ?? null);
  const loadMore = async () => {
    if (!cursor || !slots.events) return;
    try {
      const r = await request<EventPage>(
        `${slots.events}&cursor=${encodeURIComponent(cursor)}`,
      );
      setMore((m) => ({
        items: [...m.items, ...r.items],
        cursor: r.next_cursor,
      }));
    } catch {
      props.onToast("Не удалось подгрузить запросы");
    }
  };

  const group = report?.items.find((d) => d.host === host) ?? null;

  const names = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of snap?.clients ?? []) m.set(c.id, c.name);
    for (const c of facets.data.clients?.clients ?? [])
      if (c.id && c.name && !m.has(c.id)) m.set(c.id, c.name);
    return m;
  }, [snap, facets.data.clients]);
  const clientName = useCallback(
    (id: string | null) => (id ? (names.get(id) ?? id) : "Не описан"),
    [names],
  );

  const patch = (p: Partial<Filters>) => {
    setState((s) => ({ ...s, filters: { ...s.filters, ...p } }));
    setSelected(null);
    setHost(null);
  };
  const setView = (v: View) => {
    setState((s) => ({ ...s, view: v }));
    setSelected(null);
    setHost(null);
  };
  const toggle = (key: "clients" | "methods" | "reasons", value: string) =>
    patch({
      [key]: f[key].includes(value)
        ? f[key].filter((x) => x !== value)
        : [...f[key], value],
    });
  const clear = () => {
    patch({
      search: "",
      host: undefined,
      clients: [],
      methods: [],
      reasons: [],
      result: "all",
    });
    setClientSearch("");
  };

  const allow = async (client: Client, domain: string) => {
    if (!snap) return;
    const res = await props.onCommit(
      addDomainToClient(snap, client.id, domain),
    );
    if (res.ok)
      props.onToast(`${domain} разрешён для ${client.name} в черновике`);
    else if (res.code !== "revision_conflict")
      props.onToast(res.message ?? "Не удалось изменить черновик");
  };
  const block = async (domain: string) => {
    if (!snap) return;
    const res = await props.onCommit(blockDomainsForAll(snap, [domain]));
    if (res.ok) props.onToast(`${domain} запрещён для всех в черновике`);
    else if (res.code !== "revision_conflict")
      props.onToast(res.message ?? "Не удалось изменить черновик");
  };

  // Keyboard triage of the requests table: j/k move, a allows for the client,
  // Esc closes the detail.
  const onKeys = (k: React.KeyboardEvent) => {
    if (
      view !== "events" ||
      k.metaKey ||
      k.ctrlKey ||
      k.altKey ||
      (k.target as HTMLElement).closest("input,textarea")
    )
      return;
    const i = selected ? items.findIndex((e) => e.id === selected.id) : -1;
    if (k.key === "j" || k.key === "k") {
      k.preventDefault();
      const next =
        items[
          Math.max(0, Math.min(items.length - 1, i + (k.key === "j" ? 1 : -1)))
        ];
      if (next) {
        setSelected(next);
        setHost(null);
        document
          .querySelector<HTMLElement>(`[data-event="${next.id}"]`)
          ?.focus();
      }
    } else if (k.key === "a" && selected && isDenied(selected) && snap) {
      const c = snap.clients.find((x) => x.id === selected.client_id);
      const domain = hostOf(selected.destination);
      if (c && c.access === "list" && !clientAllows(snap, c, domain)) {
        k.preventDefault();
        void allow(c, domain);
      }
    } else if (k.key === "Escape" && selected) {
      k.preventDefault();
      setSelected(null);
    }
  };

  const range = periodWindow(f, now);
  const sameDay =
    dateTime(range.from.toISOString(), false).slice(0, 10) ===
    dateTime(range.to.toISOString(), false).slice(0, 10);

  const rangeControls = (
    <div className="grid w-full max-w-[420px] grid-cols-[minmax(0,1fr)_34px] items-center gap-2">
      <ToggleGroup
        type="single"
        variant="seg"
        size="sm"
        spacing={0}
        aria-label="Период"
        value={f.period === "custom" ? "" : f.period}
        onValueChange={(v) =>
          v && patch({ period: v as Filters["period"], range: undefined })
        }
        className="w-full min-w-0 overflow-hidden rounded-md border"
      >
        {PERIODS.map((p) => (
          <ToggleGroupItem
            key={p}
            value={p}
            className="min-w-0 flex-1 px-0.5 text-center"
          >
            {p}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <DateRangePicker
        value={{ from: range.from.toISOString(), to: range.to.toISOString() }}
        onChange={(r) => patch({ period: "custom", range: r })}
      />
    </div>
  );

  const viewControl = (
    <ToggleGroup
      type="single"
      variant="seg"
      size="sm"
      spacing={0}
      aria-label="Представление запросов"
      value={view}
      onValueChange={(v) => v && setView(v as View)}
      className="overflow-hidden rounded-md border"
    >
      <ToggleGroupItem value="events" className="px-3">
        Запросы
      </ToggleGroupItem>
      <ToggleGroupItem value="domains" className="px-3">
        Домены
      </ToggleGroupItem>
    </ToggleGroup>
  );

  const facetGroup = (
    title: string,
    key: "clients" | "methods" | "reasons",
    options: [string, string, number][],
  ) => (
    <AccordionItem value={key}>
      <AccordionTrigger className="py-3 text-base font-semibold">
        {title}
      </AccordionTrigger>
      <AccordionContent className="space-y-1 pb-3">
        {key === "clients" && (
          <Input
            placeholder="Найти клиента"
            aria-label="Найти клиента в фильтрах"
            value={clientSearch}
            onChange={(e) => setClientSearch(e.target.value)}
            className="mb-2"
          />
        )}
        {options
          .filter(
            ([, label]) =>
              key !== "clients" ||
              !clientSearch ||
              label.toLowerCase().includes(clientSearch.toLowerCase()),
          )
          .map(([id, label, count]) => (
            <Label
              key={id || "unmatched"}
              className="flex cursor-pointer items-center gap-2 py-1.5 font-normal"
            >
              <Checkbox
                checked={f[key].includes(id)}
                onCheckedChange={() => toggle(key, id)}
                aria-label={label}
              />
              <span className="grow truncate">{label}</span>
              <span className="text-sm text-muted-foreground tabular-nums">
                {formatCount(count)}
              </span>
            </Label>
          ))}
        {options.length === 0 && (
          <span className="text-sm text-muted-foreground">
            Нет запросов за период
          </span>
        )}
      </AccordionContent>
    </AccordionItem>
  );
  // Options: values seen in the facet (without its own selection) plus the
  // selected ones, so a checked value never disappears.
  const options = (
    seen: [string, string, number][],
    picked: string[],
    label: (id: string) => string,
  ): [string, string, number][] => [
    ...seen,
    ...picked
      .filter((id) => !seen.some(([x]) => x === id))
      .map((id): [string, string, number] => [id, label(id), 0]),
  ];
  const resultFacets = facets.data.result;
  const filterPane = journal && (
    <ListPane>
      <div className="flex h-[60px] flex-none items-center border-b p-3 [&>div]:w-full [&_button]:flex-1">
        {viewControl}
      </div>
      <ListPaneHeader
        title="Фильтры"
        action={
          <Button size="xs" variant="ghost" onClick={clear}>
            Сбросить
          </Button>
        }
      >
        {rangeControls}
      </ListPaneHeader>
      <ListPaneBody className="px-4">
        <RadioGroup
          aria-label="Результат"
          value={f.result}
          onValueChange={(v) => patch({ result: v as Filters["result"] })}
          className="gap-0 border-b py-3"
        >
          {(
            [
              [
                "all",
                "Все события",
                (resultFacets?.allowed ?? 0) + (resultFacets?.denied ?? 0),
              ],
              ["denied", "Отказы", resultFacets?.denied ?? 0],
              ["allowed", "Разрешено", resultFacets?.allowed ?? 0],
            ] as const
          ).map(([id, label, count]) => (
            <Label
              key={id}
              className="flex items-center gap-2 py-2 font-normal"
            >
              <RadioGroupItem value={id} />
              <span className="grow">{label}</span>
              <span className="text-sm text-muted-foreground tabular-nums">
                {resultFacets ? formatCount(count) : "—"}
              </span>
            </Label>
          ))}
        </RadioGroup>
        <Accordion type="multiple" defaultValue={["clients", "reasons"]}>
          {facetGroup(
            "Клиенты",
            "clients",
            options(
              (facets.data.clients?.clients ?? []).map((c) => [
                c.id,
                c.id ? (c.name ?? clientName(c.id)) : "Не описан",
                c.count,
              ]),
              f.clients,
              (id) => clientName(id || null),
            ),
          )}
          {facetGroup(
            "Причина отказа",
            "reasons",
            options(
              (facets.data.reasons?.deny_reasons ?? [])
                .filter((r) => r.reason in DENY_REASONS)
                .map((r) => [r.reason, denyReasonLabel(r.reason), r.count]),
              f.reasons,
              denyReasonLabel,
            ),
          )}
          {facetGroup(
            "Метод",
            "methods",
            options(
              (facets.data.methods?.methods ?? []).map((m) => [
                m.method,
                m.method || "—",
                m.count,
              ]),
              f.methods,
              (m) => m,
            ),
          )}
        </Accordion>
      </ListPaneBody>
    </ListPane>
  );

  const chip = (key: string, label: React.ReactNode, onClick: () => void) => (
    <Button key={key} size="xs" variant="outline" onClick={onClick}>
      {label}
      <X />
    </Button>
  );
  const activeChips = (
    <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto [scrollbar-width:none] [&>button]:shrink-0">
      {f.host &&
        chip("host", `Домен: ${f.host}`, () => patch({ host: undefined }))}
      {f.clients.map((id) =>
        chip(`c:${id}`, clientName(id || null), () => toggle("clients", id)),
      )}
      {f.result !== "all" &&
        chip("result", f.result === "denied" ? "Отказы" : "Разрешено", () =>
          patch({ result: "all" }),
        )}
      {f.methods.map((m) => chip(`m:${m}`, m, () => toggle("methods", m)))}
      {f.reasons.map((r) =>
        chip(`r:${r}`, denyReasonLabel(r), () => toggle("reasons", r)),
      )}
    </div>
  );

  const total =
    view === "domains"
      ? pluralRu(report?.items.length ?? 0, "домен", "домена", "доменов")
      : pluralRu(
          items.reduce((n, e) => n + (e.count || 1), 0),
          "запрос",
          "запроса",
          "запросов",
        );
  const lagging = page?.gap || page?.ingestion?.stale;
  const periodFull = `${dateTime(range.from.toISOString(), false)} — ${dateTime(range.to.toISOString(), false)} МСК`;
  const body = (
    <>
      <div className="flex min-h-[60px] flex-none flex-wrap items-center gap-3 border-b bg-card px-4 py-3">
        {journal && <h2 className="mr-2 text-xl font-semibold">Журнал</h2>}
        {!journal && viewControl}
        <div className="relative ml-auto w-64">
          <Search
            aria-hidden
            className="absolute top-2 left-2 size-4 text-muted-foreground"
          />
          <Input
            className="pl-8"
            placeholder="Домен или IP"
            aria-label="Поиск по запросам"
            value={f.search}
            onChange={(e) =>
              setState((s) => ({
                ...s,
                filters: { ...s.filters, search: e.target.value },
              }))
            }
          />
        </div>
        {journal ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setPaused((x) => !x)}
          >
            {paused ? <Play /> : <Pause />}
            {paused ? "Продолжить" : "Пауза"}
          </Button>
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              onOpen?.(
                "log",
                logIntent({
                  view,
                  filters: { ...f, clients: [clientId!] },
                }),
              )
            }
          >
            Открыть в журнале
            <ArrowRight />
          </Button>
        )}
      </div>
      {!journal && (
        <div className="flex flex-wrap items-center gap-2 border-b bg-background px-4 py-3">
          {rangeControls}
          <ToggleGroup
            type="single"
            variant="seg"
            size="sm"
            spacing={0}
            aria-label="Результат клиента"
            value={f.result}
            onValueChange={(v) =>
              v && patch({ result: v as Filters["result"] })
            }
            className="overflow-hidden rounded-md border"
          >
            <ToggleGroupItem value="all" className="px-3">
              Все
            </ToggleGroupItem>
            <ToggleGroupItem value="denied" className="px-3">
              Отказы
            </ToggleGroupItem>
            <ToggleGroupItem value="allowed" className="px-3">
              Разрешено
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
      )}
      {journal && (
        <TrafficSummary
          onZoom={(r) => patch({ period: "custom", range: r })}
          report={summary ?? null}
          facets={resultFacets ?? null}
          f={f}
          patch={patch}
          clientName={clientName}
        />
      )}
      <div
        data-slot="request-summary"
        className="flex h-10 flex-none items-center gap-4 border-b bg-surface-inset px-4 text-sm text-muted-foreground"
      >
        <span className="shrink-0 font-medium whitespace-nowrap text-foreground">
          {total}
          {view === "domains" &&
            report?.truncated &&
            " · показаны самые частые"}
          {lagging && " · сбор журнала отстаёт"}
        </span>
        {(journal || f.host) && activeChips}
        <span className="ml-auto flex min-w-0 max-w-[45%] shrink-0 items-center gap-1">
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Предыдущее окно"
            title="Предыдущее окно"
            onClick={() => patch(shiftWindow(f, now, -1))}
          >
            <ChevronLeft />
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                className="min-w-0 truncate tabular-nums"
                aria-label={periodFull}
                tabIndex={0}
              >
                {compactPeriod(
                  range.from.toISOString(),
                  range.to.toISOString(),
                )}
              </span>
            </TooltipTrigger>
            <TooltipContent>{periodFull}</TooltipContent>
          </Tooltip>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Следующее окно"
            title="Следующее окно"
            disabled={isLive(f)}
            onClick={() => patch(shiftWindow(f, now, 1))}
          >
            <ChevronRight />
          </Button>
          {!isLive(f) && (
            <Button
              size="xs"
              variant="ghost"
              onClick={() => patch({ period: "24 ч", range: undefined })}
            >
              Сейчас
            </Button>
          )}
        </span>
      </div>
    </>
  );

  const failed = main.failed && !page && !report;
  const table = failed ? (
    <ErrorState
      title="Журнал недоступен"
      detail="Хранилище не ответило. Повторим при следующем обновлении."
      onRetry={() => setTick((n) => n + 1)}
    />
  ) : (
    <>
      <RequestTable
        view={view}
        items={items}
        domains={report?.items ?? []}
        loading={!main.loaded}
        client={!!clientId}
        sameDay={sameDay}
        wide={!selected && !group}
        clientName={clientName}
        selectedEvent={selected?.id}
        selectedHost={host ?? undefined}
        onEvent={(e) => {
          setSelected(e);
          setHost(null);
        }}
        onDomain={(d) => {
          setHost(d.host);
          setSelected(null);
        }}
      />
      {view === "events" && cursor && (
        <div className="flex justify-center p-3">
          <Button variant="outline" size="sm" onClick={loadMore}>
            Показать ещё
          </Button>
        </div>
      )}
    </>
  );

  const detailLayout = useDefaultLayout({
    id: "kraken-request-panels",
    panelIds:
      selected || group
        ? ["request-table", "request-detail"]
        : ["request-table"],
  });
  const details = (
    <>
      {(selected || group) && (
        <ResizableHandle
          aria-label="Ширина панели запроса"
          className="focus-visible:ring-0 focus-visible:ring-offset-0"
        />
      )}
      {selected && (
        <EventDetails
          key={selected.id}
          event={selected}
          snap={snap}
          active={active}
          session={session}
          request={request}
          onOpen={onOpen}
          onAllow={allow}
          onBlock={block}
          onCommit={props.onCommit}
          onToast={props.onToast}
          onClose={() => setSelected(null)}
        />
      )}
      {group && (
        <DomainDetails
          group={group}
          snap={snap}
          session={session}
          clientName={clientName}
          journal={journal}
          onAllow={allow}
          onBlock={block}
          onClose={() => setHost(null)}
          onDrill={() => {
            setState((s) => ({
              view: "events",
              filters: { ...s.filters, host: group.host, search: "" },
            }));
            setHost(null);
          }}
        />
      )}
    </>
  );
  const panels = (content: React.ReactNode, minSize: number) => (
    <ResizablePanelGroup
      orientation="horizontal"
      defaultLayout={detailLayout.defaultLayout}
      onLayoutChanged={detailLayout.onLayoutChanged}
      className="min-h-0 flex-1"
    >
      <ResizablePanel id="request-table" minSize={minSize}>
        {content}
      </ResizablePanel>
      {details}
    </ResizablePanelGroup>
  );

  // In a client tab the detail sits beside the table; in the journal it spans
  // the full height beside the header, summary and table.
  return journal ? (
    <>
      {filterPane}
      <InspectorPane>
        {panels(
          <div className="flex h-full min-h-0 min-w-0 flex-col">
            {body}
            <div
              className="min-h-0 flex-1 overflow-auto bg-surface-inset"
              onKeyDown={onKeys}
            >
              {table}
            </div>
          </div>,
          400,
        )}
      </InspectorPane>
    </>
  ) : (
    <div
      data-slot="client-requests"
      className="flex min-h-[540px] flex-col overflow-hidden rounded-lg border bg-surface-inset"
    >
      {body}
      {panels(
        <div
          className="h-full min-w-0 overflow-auto bg-surface-inset"
          onKeyDown={onKeys}
        >
          {table}
        </div>,
        200,
      )}
    </div>
  );
}

function Outcome({ event }: { event: Event }) {
  const denied = isDenied(event);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-sm",
        denied ? "text-bad" : "text-muted-foreground",
      )}
    >
      <span
        className={cn("size-1.5 rounded-full", denied ? "bg-bad" : "bg-ok")}
        aria-hidden
      />
      {denied ? "Отказ" : "Разрешено"}
      {event.proxy_http_status != null && (
        <span className="text-xs text-muted-foreground">
          {event.proxy_http_status}
        </span>
      )}
      {event.count > 1 && (
        <span
          className="text-sm text-muted-foreground tabular-nums"
          title={`Повторов в пределах секунды: ${event.count}`}
        >
          ×{event.count}
        </span>
      )}
    </span>
  );
}

// RequestTable: one Table composition for both views and both contexts.
function RequestTable({
  view,
  items,
  domains,
  loading,
  client,
  sameDay,
  wide,
  clientName,
  selectedEvent,
  selectedHost,
  onEvent,
  onDomain,
}: {
  view: View;
  items: Event[];
  domains: DomainRow[];
  loading: boolean;
  client: boolean;
  sameDay: boolean;
  wide: boolean;
  clientName: (id: string | null) => string;
  selectedEvent?: string;
  selectedHost?: string;
  onEvent: (e: Event) => void;
  onDomain: (d: DomainRow) => void;
}) {
  const activate =
    (fn: () => void) => (k: React.KeyboardEvent<HTMLTableRowElement>) => {
      if (k.key === "Enter" || k.key === " ") {
        k.preventDefault();
        fn();
      }
    };
  const heads =
    view === "events"
      ? [
          "Время",
          client ? "IP-адрес" : "Источник",
          "Назначение",
          "Метод",
          ...(wide ? ["Объём"] : []),
          "Результат",
        ]
      : [
          "Домен",
          "Запросов",
          "Разрешено",
          "Отказов",
          ...(!client ? ["Источников"] : []),
          "Объём",
          "Последний запрос",
        ];
  const empty = view === "events" ? !items.length : !domains.length;
  return (
    <Table className="min-w-[640px] bg-surface-inset">
      <TableHeader className="bg-muted">
        <TableRow>
          {heads.map((h, i) => (
            <TableHead
              key={h}
              className={
                view === "domains" && i > 0
                  ? "text-right"
                  : h === "Объём"
                    ? "text-right"
                    : h === "Результат"
                      ? "sticky right-0 bg-muted"
                      : ""
              }
            >
              {h}
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {empty ? (
          <TableRow>
            <TableCell colSpan={heads.length}>
              <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
                {loading ? (
                  "Загружаем…"
                ) : (
                  <>
                    <span className="text-lg font-medium text-foreground">
                      Запросов не найдено
                    </span>
                    <span>Измените период или снимите фильтры</span>
                  </>
                )}
              </div>
            </TableCell>
          </TableRow>
        ) : view === "events" ? (
          items.map((e) => {
            const denied = isDenied(e);
            return (
              <TableRow
                key={e.id}
                data-event={e.id}
                data-state={selectedEvent === e.id ? "selected" : undefined}
                aria-selected={selectedEvent === e.id}
                tabIndex={0}
                aria-label={`Запрос ${dateTime(e.timestamp)} ${e.destination}`}
                onClick={() => onEvent(e)}
                onKeyDown={activate(() => onEvent(e))}
                className="group cursor-pointer hover:bg-hover data-[state=selected]:bg-sel"
              >
                <TableCell className="whitespace-nowrap tabular-nums">
                  <Timestamp
                    value={e.timestamp}
                    mode={sameDay ? "time" : "datetime"}
                    compact
                  />
                </TableCell>
                <TableCell className="w-40">
                  <div className="font-medium">
                    {client
                      ? e.source_ip
                      : (e.client_name ?? clientName(e.client_id))}
                  </div>
                  {!client && (
                    <div className="font-mono text-xs text-muted-foreground">
                      {e.source_ip}
                    </div>
                  )}
                </TableCell>
                <TableCell
                  className="max-w-[340px] truncate"
                  title={e.destination}
                >
                  {hostOf(e.destination)}
                  {portOf(e.destination) && (
                    <span className="text-sm text-muted-foreground">
                      :{portOf(e.destination)}
                    </span>
                  )}
                </TableCell>
                <TableCell className="w-24 text-sm text-muted-foreground">
                  {e.method}
                </TableCell>
                {wide && (
                  <TableCell className="w-20 text-right text-sm text-muted-foreground tabular-nums">
                    {e.bytes_observed ? formatBytes(e.bytes_observed) : "—"}
                  </TableCell>
                )}
                <TableCell className="sticky right-0 w-44 bg-surface-inset group-hover:bg-hover group-data-[state=selected]:bg-sel">
                  <Outcome event={e} />
                  {denied && e.deny_reason && (
                    <div className="mt-0.5 text-sm font-medium whitespace-nowrap text-bad">
                      {denyReasonLabel(e.deny_reason)}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            );
          })
        ) : (
          domains.map((d) => (
            <TableRow
              key={d.host}
              data-state={selectedHost === d.host ? "selected" : undefined}
              aria-selected={selectedHost === d.host}
              tabIndex={0}
              aria-label={`Домен ${d.host}`}
              onClick={() => onDomain(d)}
              onKeyDown={activate(() => onDomain(d))}
              className="group cursor-pointer hover:bg-hover data-[state=selected]:bg-sel"
            >
              <TableCell
                className="max-w-[310px] truncate font-medium"
                title={d.host}
              >
                {d.host}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {formatCount(d.requests)}
              </TableCell>
              <TableCell className="text-right text-muted-foreground tabular-nums">
                {formatCount(d.requests - d.denied)}
              </TableCell>
              <TableCell
                className={cn(
                  "text-right tabular-nums",
                  d.denied ? "text-bad" : "text-muted-foreground",
                )}
              >
                {formatCount(d.denied)}
              </TableCell>
              {!client && (
                <TableCell className="text-right tabular-nums">
                  {d.source_count}
                </TableCell>
              )}
              <TableCell className="text-right text-muted-foreground">
                {formatBytes(d.bytes)}
              </TableCell>
              <TableCell className="text-right text-sm text-muted-foreground tabular-nums">
                <Timestamp
                  value={d.last_seen}
                  mode={sameDay ? "time" : "datetime"}
                  compact
                />
              </TableCell>
            </TableRow>
          ))
        )}
      </TableBody>
    </Table>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-3 border-b py-3 last:border-b-0">
      <h3 className="font-semibold">{title}</h3>
      {children}
    </section>
  );
}

function isBlocked(snap: Snapshot | null, domain: string): boolean {
  return (snap?.settings?.blocked_domains ?? []).some(
    (b) =>
      b.domain === domain ||
      (b.include_subdomains && domain.endsWith("." + b.domain)),
  );
}

function EventDetails({
  event: e,
  snap,
  active,
  session,
  request,
  onOpen,
  onAllow,
  onBlock,
  onCommit,
  onToast,
  onClose,
}: {
  event: Event;
  snap: Snapshot | null;
  active: Snapshot | null;
  session: SectionProps["session"];
  request: SectionProps["request"];
  onOpen?: SectionProps["onOpen"];
  onAllow: (c: Client, domain: string) => Promise<void>;
  onBlock: (domain: string) => Promise<void>;
  onCommit: SectionProps["onCommit"];
  onToast: SectionProps["onToast"];
  onClose: () => void;
}) {
  const denied = isDenied(e);
  const domain = hostOf(e.destination);
  const [explain, setExplain] = useState<Explain | null>(null);
  const [explainFailed, setExplainFailed] = useState(false);
  useEffect(() => {
    if (!denied) return;
    let live = true;
    request<Explain>(`/inspector/events/${encodeURIComponent(e.id)}/explain`)
      .then((r) => live && setExplain(r))
      .catch(() => live && setExplainFailed(true));
    return () => {
      live = false;
    };
  }, [e.id, denied, request]);
  const client = snap?.clients.find((c) => c.id === e.client_id) ?? null;
  const live = active?.clients.find((c) => c.id === e.client_id) ?? null;
  const allowedNow = !!live && !!active && clientAllows(active, live, domain);
  const pending =
    !!client && !!snap && clientAllows(snap, client, domain) && !allowedNow;
  const scope = managerScope(session);
  const isAdmin = session.role !== "manager";
  const canAllow =
    denied &&
    !!client &&
    client.access === "list" &&
    explain?.reason === "not_in_allowlist" &&
    !pending;
  const policies = canAllow
    ? (snap?.policies ?? []).filter((p) => client!.policy_ids.includes(p.id))
    : [];
  const allowInPolicy = async (policyID: string, name: string) => {
    if (!snap) return;
    const res = await onCommit(addDomainsToPolicy(snap, policyID, [domain]));
    if (res.ok) onToast(`${domain} добавлен в политику «${name}» в черновике`);
    else if (res.code !== "revision_conflict")
      onToast(res.message ?? "Не удалось изменить черновик");
  };
  const reason = e.deny_reason;
  return (
    <DetailPane>
      <div className="flex items-start justify-between">
        <div>
          <h3 className="text-lg font-semibold tabular-nums">
            <Timestamp value={e.timestamp} mode="time" />
          </h3>
          <p className="text-sm text-muted-foreground">
            {dateTime(e.timestamp, false).slice(0, 10)}
            {e.revision_id && ` · ${revisionLabel(e.revision_id)}`}
          </p>
        </div>
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Закрыть запрос"
          onClick={onClose}
        >
          <X />
        </Button>
      </div>
      <Section title="Результат">
        <KeyValue
          items={[
            ["Действие", <Outcome event={e} />],
            ["Метод", e.method],
          ]}
        />
      </Section>
      <Section title="Источник">
        <KeyValue
          items={[
            [
              "Клиент",
              e.client_id ? (
                <Button
                  variant="link"
                  size="xs"
                  className="h-auto p-0"
                  onClick={() => onOpen?.("clients", `client:${e.client_id}`)}
                >
                  {e.client_name ?? client?.name ?? e.client_id}
                </Button>
              ) : (
                "Не описан"
              ),
            ],
            ["IP-адрес", e.source_ip],
          ]}
        />
      </Section>
      <Section title="Назначение">
        <KeyValue
          items={[
            ["Домен", domain],
            ["Порт", portOf(e.destination) || "—"],
            [
              "Объём",
              e.bytes_observed != null ? formatBytes(e.bytes_observed) : "—",
            ],
          ]}
        />
      </Section>
      {denied && (
        <Section title="Причина отказа">
          <p className="text-sm leading-relaxed">
            {explain
              ? whyText(explain, e, active)
              : explainFailed
                ? "Не удалось получить объяснение. Возможно, запрос уже устарел."
                : "Загружаем…"}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">
            По действующим правилам ·{" "}
            <Button
              variant="link"
              size="xs"
              className="h-auto p-0"
              onClick={() => onOpen?.("help", "help:scenario-denied")}
            >
              Как разбирать отказы
            </Button>
          </p>
          {pending && (
            <p className="mt-2 text-sm text-info">
              Разрешение в черновике; начнёт действовать после применения.
            </p>
          )}
        </Section>
      )}
      {canAllow && client && (
        <Section title="Разрешить">
          <div className="space-y-1">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onAllow(client, domain)}
            >
              <Plus />
              Только для {client.name}
            </Button>
            <p className="text-sm text-muted-foreground">
              В свои домены клиента. Политики не меняются.
            </p>
          </div>
          {policies.map((p) => {
            const users = (snap?.clients ?? []).filter((x) =>
              x.policy_ids.includes(p.id),
            );
            const locked =
              !!scope && users.some((x) => !scope.clientIDs.includes(x.id));
            return (
              <div key={p.id} className="space-y-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={locked}
                  onClick={() => allowInPolicy(p.id, p.name)}
                >
                  <Plus />В политику «{p.name}»
                </Button>
                <p className="text-sm text-muted-foreground">
                  {locked
                    ? "Политику правит администратор."
                    : users.length === 1
                      ? `Затронет только ${users[0].name}`
                      : `Затронет ${pluralRu(users.length, "клиента", "клиентов", "клиентов")}: ${users.map((x) => x.name).join(", ")}`}
                </p>
              </div>
            );
          })}
        </Section>
      )}
      {denied && !e.client_id && (
        <Button
          size="sm"
          variant="outline"
          onClick={() => onOpen?.("clients", `source:${e.source_ip}`)}
        >
          Описать IP как клиента
        </Button>
      )}
      {isAdmin &&
        !!snap &&
        !isBlocked(snap, domain) &&
        reason !== "port" &&
        reason !== "connect_port" &&
        reason !== "protected" &&
        reason !== "private" && (
          <div className="space-y-1">
            <Button
              variant="ghost"
              size="sm"
              className="text-bad"
              onClick={() => onBlock(domain)}
            >
              Запретить для всех
            </Button>
            <p className="text-sm text-muted-foreground">
              Отказ всем клиентам, включая «весь веб».
            </p>
          </div>
        )}
      <Collapsible>
        <CollapsibleTrigger asChild>
          <Button variant="ghost" size="xs">
            Технические данные
            <ChevronDown />
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="pt-2">
          <KeyValue
            items={[
              [
                "Squid",
                `${e.proxy_result}${e.proxy_http_status != null ? "/" + e.proxy_http_status : ""}`,
              ],
              ["Повторов", e.count > 1 ? String(e.count) : "1"],
              [
                "Ревизия события",
                e.revision_id ? revisionLabel(e.revision_id) : "—",
              ],
            ]}
          />
        </CollapsibleContent>
      </Collapsible>
    </DetailPane>
  );
}

function DomainDetails({
  group: g,
  snap,
  session,
  clientName,
  journal,
  onAllow,
  onBlock,
  onClose,
  onDrill,
}: {
  group: DomainRow;
  snap: Snapshot | null;
  session: SectionProps["session"];
  clientName: (id: string | null) => string;
  journal: boolean;
  onAllow: (c: Client, domain: string) => Promise<void>;
  onBlock: (domain: string) => Promise<void>;
  onClose: () => void;
  onDrill: () => void;
}) {
  // Clients denied this domain whose draft lists still miss it.
  const denyClients = (snap?.clients ?? []).filter(
    (c) =>
      c.access === "list" &&
      g.sources.some((s) => s.client_id === c.id && s.denied > 0) &&
      !clientAllows(snap!, c, g.host),
  );
  const canBlock =
    session.role !== "manager" && !!snap && !isBlocked(snap, g.host);
  return (
    <DetailPane>
      <div className="flex items-start gap-2">
        <h3 className="min-w-0 grow text-lg font-semibold break-words">
          {g.host}
        </h3>
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Закрыть домен"
          onClick={onClose}
        >
          <X />
        </Button>
      </div>
      <KeyValue
        items={[
          ["Запросов", formatCount(g.requests)],
          ["Разрешено", formatCount(g.requests - g.denied)],
          ["Отказов", formatCount(g.denied)],
          ["Объём", formatBytes(g.bytes)],
          ["Порты", g.ports.join(", ") || "—"],
          ["Последний запрос", <Timestamp value={g.last_seen} compact />],
        ]}
      />
      <Section title={journal ? "Источники" : "Адреса"}>
        {g.sources.map((s) => (
          <div
            className="flex justify-between gap-2"
            key={`${s.client_id ?? ""}|${s.source_ip}`}
          >
            <span className="min-w-0">
              {s.client_name ?? clientName(s.client_id)}
              <span className="block text-sm text-muted-foreground">
                {s.source_ip}
              </span>
            </span>
            <span className="tabular-nums">{formatCount(s.requests)}</span>
          </div>
        ))}
        {g.source_count > g.sources.length && (
          <p className="text-sm text-muted-foreground">
            и ещё{" "}
            {pluralRu(
              g.source_count - g.sources.length,
              "адрес",
              "адреса",
              "адресов",
            )}
          </p>
        )}
      </Section>
      <Button variant="outline" size="sm" onClick={onDrill}>
        Показать запросы
        <ArrowRight />
      </Button>
      {(denyClients.length > 0 || canBlock) && (
        <Section title="Действия">
          {denyClients.map((c) => (
            <div
              key={c.id}
              className="flex items-baseline justify-between gap-2"
            >
              <span className="text-sm">
                {c.name}
                <span className="block text-muted-foreground">
                  получил отказ
                </span>
              </span>
              <Button
                size="xs"
                variant="outline"
                onClick={() => onAllow(c, g.host)}
              >
                <Plus />
                Разрешить
              </Button>
            </div>
          ))}
          {canBlock && (
            <div className="space-y-1">
              <Button
                variant="ghost"
                size="sm"
                className="text-bad"
                onClick={() => onBlock(g.host)}
              >
                Запретить для всех
              </Button>
              <p className="text-sm text-muted-foreground">
                Отказ всем клиентам, включая «весь веб».
              </p>
            </div>
          )}
        </Section>
      )}
    </DetailPane>
  );
}

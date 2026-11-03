import { useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ReferenceArea,
  XAxis,
  YAxis,
} from "recharts";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { dateTime, localTime } from "../model";
import type { Filters, Range } from "./filters";
import { formatBytes, formatCount, type DomainsReport } from "./data";
import type { FacetsResponse } from "./query";

// Fixed chart id: see TRAFFIC_CHART_ID.
export const SUMMARY_CHART_ID = "summary";
export const summaryChartConfig = {
  allowed: { label: "Разрешено", color: "var(--s-allow)" },
  denied: { label: "Отказано", color: "var(--s-deny)" },
} satisfies ChartConfig;

const KEY = "kraken-log-summary";
function remembered() {
  try {
    return localStorage.getItem(KEY) !== "collapsed";
  } catch {
    return true;
  }
}
function remember(open: boolean) {
  try {
    localStorage.setItem(KEY, open ? "open" : "collapsed");
  } catch {
    // storage is best-effort
  }
}

// TrafficSummary: the whole picture for the journal's period and filters
// (except the result filter) — totals, allowed/denied over time, top domains
// and top clients. Every figure narrows the journal to the requests it counts;
// dragging across the chart (or clicking a bar) zooms into that time.
export function TrafficSummary({
  report,
  facets,
  f,
  patch,
  clientName,
  onZoom,
}: {
  report: DomainsReport | null;
  facets: FacetsResponse | null;
  f: Filters;
  patch: (p: Partial<Filters>) => void;
  clientName: (id: string | null) => string;
  onZoom: (range: Range) => void;
}) {
  const [open, setOpen] = useState(remembered);
  // Drag selection on the chart: bucket starts under the pointer.
  const [pick, setPick] = useState<{ a: string; b: string } | null>(null);
  const sum = report?.summary;
  const series = (facets?.histogram ?? []).map((b) => ({
    start: b.start,
    allowed: b.allowed,
    denied: b.denied,
  }));
  const multiDay =
    series.length > 1 &&
    series[0].start.slice(0, 10) !==
      series[series.length - 1].start.slice(0, 10);
  const step =
    series.length > 1
      ? Date.parse(series[1].start) - Date.parse(series[0].start)
      : 60_000;
  const zoom = () => {
    if (!pick) return;
    setPick(null);
    const [a, b] = [Date.parse(pick.a), Date.parse(pick.b)].sort(
      (x, y) => x - y,
    );
    if (!Number.isFinite(a) || !Number.isFinite(b)) return;
    onZoom({
      from: new Date(a).toISOString(),
      to: new Date(b + step).toISOString(),
    });
  };
  const tick = (v: unknown) =>
    multiDay ? dateTime(String(v), false, true) : localTime(String(v), false);
  const period = f.period === "custom" ? "выбранный период" : f.period;
  const toggle = () =>
    setOpen((v) => {
      remember(!v);
      return !v;
    });
  const kpi = (
    label: string,
    value: string,
    onClick: (() => void) | null,
    active = false,
    bad = false,
  ) => (
    <Button
      variant="ghost"
      disabled={!onClick}
      onClick={onClick ?? undefined}
      aria-pressed={onClick ? active : undefined}
      className={cn(
        "h-auto min-w-0 flex-col items-start gap-0 px-2.5 py-1.5 text-left font-normal disabled:opacity-100",
        active && "bg-sel",
      )}
    >
      <span className="text-sm text-muted-foreground">{label}</span>
      <span
        className={cn(
          "text-xl font-semibold tabular-nums",
          bad && value !== "0" && "text-bad",
        )}
      >
        {value}
      </span>
    </Button>
  );
  const row = (
    key: string,
    label: React.ReactNode,
    count: number,
    denied: number,
    onClick: () => void,
    active: boolean,
  ) => (
    <li key={key}>
      <Button
        variant="ghost"
        onClick={onClick}
        aria-pressed={active}
        className={cn(
          "grid h-auto w-full grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3 px-2 py-1 text-left text-base font-normal",
          active && "bg-sel",
        )}
      >
        <span className="truncate">{label}</span>
        <span className="text-sm tabular-nums">
          {formatCount(count)}
          {denied > 0 && (
            <span className="text-bad"> · {formatCount(denied)} отк.</span>
          )}
        </span>
      </Button>
    </li>
  );
  const unknownOn = f.clients.includes("");
  return (
    <section
      aria-label={`Сводка трафика за ${period}`}
      className="@container border-b px-2 py-2"
    >
      <div className="flex flex-wrap items-end gap-1">
        {kpi(
          "Запросы",
          sum ? formatCount(sum.requests) : "—",
          f.result !== "all" ? () => patch({ result: "all" }) : null,
        )}
        {kpi(
          "Отказы",
          sum ? formatCount(sum.denied) : "—",
          () => patch({ result: f.result === "denied" ? "all" : "denied" }),
          f.result === "denied",
          true,
        )}
        {kpi("Объём", sum ? formatBytes(sum.bytes) : "—", null)}
        {kpi(
          "Неописанные источники",
          sum ? formatCount(sum.unmatched_sources) : "—",
          sum?.unmatched_sources || unknownOn
            ? () =>
                patch({
                  clients: unknownOn
                    ? f.clients.filter((x) => x !== "")
                    : [...f.clients, ""],
                })
            : null,
          unknownOn,
        )}
        <span className="grow" />
        <span className="self-center pr-1 text-sm text-muted-foreground">
          за {period}
        </span>
        <Button
          size="xs"
          variant="ghost"
          className="self-center"
          aria-expanded={open}
          onClick={toggle}
        >
          {open ? <ChevronUp /> : <ChevronDown />}
          {open ? "Свернуть" : "Подробнее"}
        </Button>
      </div>
      {open && (
        <div className="grid grid-cols-1 gap-x-6 gap-y-3 px-2 pt-2 pb-1 @[600px]:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,1fr)]">
          <div className="min-w-0">
            <h3 className="mb-1 text-sm font-medium text-muted-foreground">
              Запросы по времени
            </h3>
            <ChartContainer
              id={SUMMARY_CHART_ID}
              config={summaryChartConfig}
              className="aspect-auto h-[92px] w-full"
            >
              <BarChart
                data={series}
                margin={{ top: 2, right: 0, bottom: 0, left: 0 }}
                className="cursor-crosshair select-none"
                onMouseDown={(s) =>
                  s?.activeLabel != null &&
                  setPick({
                    a: String(s.activeLabel),
                    b: String(s.activeLabel),
                  })
                }
                onMouseMove={(s) =>
                  pick &&
                  s?.activeLabel != null &&
                  setPick({ ...pick, b: String(s.activeLabel) })
                }
                onMouseUp={zoom}
                onMouseLeave={() => setPick(null)}
              >
                <CartesianGrid vertical={false} strokeDasharray="3 3" />
                <XAxis
                  dataKey="start"
                  tickFormatter={tick}
                  tick={{ fill: "var(--muted-foreground)" }}
                  tickLine={false}
                  axisLine={false}
                  minTickGap={40}
                />
                <YAxis
                  width={28}
                  tickCount={3}
                  allowDecimals={false}
                  tick={{ fill: "var(--muted-foreground)" }}
                  tickLine={false}
                  axisLine={false}
                />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={tick} />}
                />
                <Bar
                  dataKey="allowed"
                  stackId="r"
                  fill="var(--color-allowed)"
                  isAnimationActive={false}
                />
                <Bar
                  dataKey="denied"
                  stackId="r"
                  fill="var(--color-denied)"
                  isAnimationActive={false}
                />
                {pick && (
                  <ReferenceArea
                    x1={pick.a}
                    x2={pick.b}
                    fill="var(--primary)"
                    fillOpacity={0.12}
                  />
                )}
              </BarChart>
            </ChartContainer>
          </div>
          <div className="min-w-0">
            <h3 className="mb-1 text-sm font-medium text-muted-foreground">
              Домены
            </h3>
            <ul>
              {(report?.items ?? [])
                .slice(0, 5)
                .map((d) =>
                  row(
                    d.host,
                    d.host,
                    d.requests,
                    d.denied,
                    () =>
                      patch({ host: f.host === d.host ? undefined : d.host }),
                    f.host === d.host,
                  ),
                )}
            </ul>
          </div>
          <div className="min-w-0">
            <h3 className="mb-1 text-sm font-medium text-muted-foreground">
              Клиенты
            </h3>
            <ul>
              {(sum?.clients ?? []).slice(0, 5).map((c) => {
                const id = c.client_id ?? "";
                return row(
                  id || "unmatched",
                  c.client_name ?? clientName(c.client_id),
                  c.requests,
                  c.denied,
                  () =>
                    patch({
                      clients: f.clients.includes(id)
                        ? f.clients.filter((x) => x !== id)
                        : [id],
                    }),
                  f.clients.length === 1 && f.clients[0] === id,
                );
              })}
            </ul>
          </div>
        </div>
      )}
    </section>
  );
}

import { localTime } from "../model";
import { useId } from "react";
import {
  Area,
  ComposedChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ReferenceLine,
  XAxis,
  YAxis,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartLegendContent,
  type ChartConfig,
} from "@/components/ui/chart";
export type Bucket = {
  start: string;
  requests: number;
  denied: number;
  bytes: number;
};
// The chart id is fixed so its colour <style> is the same text everywhere and
// the CSP can allow it by hash (csp-style-hashes.json).
export const TRAFFIC_CHART_ID = "traffic";
export const trafficChartConfig = {
  allowed: { label: "Разрешено", color: "var(--s-allow)" },
  denied: { label: "Отказы", color: "var(--s-deny)" },
  requests: { label: "Запросы", color: "var(--s-allow)" },
} satisfies ChartConfig;
const hhmm = (value: string | number) => localTime(String(value), false);
// Одна временная шкала для разрешённых запросов и отказов.
export function TrafficChart({
  series,
  height = 128,
}: {
  series: Bucket[];
  height?: number;
}) {
  const syncId = useId();
  if (series.length < 2)
    return <p className="text-sm text-muted-foreground">Нет данных</p>;
  const data = series.map((b) => ({
    ...b,
    allowed: Math.max(0, b.requests - b.denied),
  }));
  const ticks = [0, 16, 32, 48, 64, 80, series.length - 1]
    .filter((i) => i < series.length)
    .map((i) => series[i].start);
  return (
    <ChartContainer
      id={TRAFFIC_CHART_ID}
      config={trafficChartConfig}
      className="aspect-auto w-full"
      style={{ height: height + 28 }}
    >
      <ComposedChart
        data={data}
        syncId={syncId}
        margin={{ top: 4, right: 4, bottom: 0, left: 0 }}
      >
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis
          tick={{ fill: "var(--muted-foreground)" }}
          dataKey="start"
          ticks={ticks}
          tickFormatter={hhmm}
          tickLine={false}
          axisLine={false}
          minTickGap={12}
        />
        <YAxis
          tick={{ fill: "var(--muted-foreground)" }}
          width={28}
          tickCount={3}
          allowDecimals={false}
          tickLine={false}
          axisLine={false}
        />
        <ChartLegend
          verticalAlign="top"
          content={<ChartLegendContent className="justify-start pb-3" />}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(label) => hhmm(String(label))}
            />
          }
        />
        <Area
          dataKey="allowed"
          type="linear"
          fill="var(--color-allowed)"
          fillOpacity={0.1}
          stroke="var(--color-allowed)"
          strokeWidth={1.5}
          isAnimationActive={false}
        />
        <Bar
          dataKey="denied"
          fill="var(--color-denied)"
          radius={[2, 2, 0, 0]}
          isAnimationActive={false}
        />
      </ComposedChart>
    </ChartContainer>
  );
}
export function Sparkline({ series = [] }: { series?: Bucket[] }) {
  const hours = Array.from(
    { length: Math.ceil(series.length / 4) },
    (_, i) => ({
      hour: i,
      requests: series
        .slice(i * 4, i * 4 + 4)
        .reduce((n, b) => n + b.requests, 0),
    }),
  );
  return (
    <ChartContainer
      id={TRAFFIC_CHART_ID}
      config={trafficChartConfig}
      className="h-[18px] w-14 min-h-0 aspect-auto"
      aria-hidden
    >
      <BarChart data={hours} margin={{ top: 1, right: 0, bottom: 0, left: 0 }}>
        <ReferenceLine y={0} stroke="var(--border)" />
        <Bar
          dataKey="requests"
          fill="var(--color-requests)"
          radius={[1, 1, 0, 0]}
          isAnimationActive={false}
        >
          {hours.map((_, i) => (
            <Cell key={i} opacity={i === hours.length - 1 ? 1 : 0.55} />
          ))}
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}

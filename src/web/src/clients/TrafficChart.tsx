import { useState } from "react";

// 15-minute buckets from /inspector/clients/{id}/stats.
export type Bucket = {
  start: string;
  requests: number;
  denied: number;
  bytes: number;
};

// niceMax rounds an axis maximum up to 1, 2 or 5 × 10^k.
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

const fmt = (n: number) => n.toLocaleString("ru-RU");
const hhmm = (iso: string) =>
  new Date(iso).toLocaleTimeString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
  });

function Grid({ max }: { max: number }) {
  return (
    <>
      {[0, 0.5, 1].map((f) => (
        <div
          key={f}
          className="absolute inset-x-0 border-t border-dashed border-border"
          style={{ top: `${(1 - f) * 100}%` }}
        >
          <span className="absolute -left-11 w-9 -translate-y-1/2 text-right text-sm text-muted-foreground tabular-nums">
            {(max * f).toLocaleString("ru-RU", { maximumFractionDigits: 1 })}
          </span>
        </div>
      ))}
    </>
  );
}

function Legend({ color, children }: { color: string; children: string }) {
  return (
    <div className="mb-1.5 flex items-center gap-1.5 text-sm text-muted-foreground">
      <i className="size-2.5 rounded-[3px]" style={{ background: color }} />
      {children}
    </div>
  );
}

// TrafficChart: allowed requests as an area, denials as bars below it, one
// shared time axis and a hover crosshair with a tooltip for the bucket.
export function TrafficChart({
  series,
  height = 120,
}: {
  series: Bucket[];
  height?: number;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const n = series.length;
  if (n < 2)
    return <div className="text-sm text-muted-foreground">Нет данных</div>;
  const allowed = series.map((s) => Math.max(0, s.requests - s.denied));
  const maxA = niceMax(Math.max(...allowed));
  const maxD = niceMax(Math.max(1, ...series.map((s) => s.denied)));
  const W = 1000;
  const H = 100;
  const x = (i: number) => (i / (n - 1)) * W;
  const line = allowed
    .map((a, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${(H - (a / maxA) * H).toFixed(1)}`)
    .join("");
  const bw = W / n;
  const ticks = [0, 16, 32, 48, 64, 80, n - 1].filter((i) => i < n);
  const at = (i: number) => `${(i / (n - 1)) * 100}%`;
  const s = hover !== null ? series[hover] : null;
  return (
    <div
      className="relative pt-0.5 pr-0.5 pb-[22px] pl-11"
      onMouseMove={(e) => {
        const plot = e.currentTarget.querySelector("[data-plot]");
        if (!plot) return;
        const r = plot.getBoundingClientRect();
        const f = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
        setHover(Math.round(f * (n - 1)));
      }}
      onMouseLeave={() => setHover(null)}
    >
      <Legend color="var(--s-allow)">Разрешено, запросов за 15 мин</Legend>
      <div data-plot className="relative" style={{ height }}>
        <Grid max={maxA} />
        <svg
          className="absolute inset-0 size-full overflow-visible"
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          aria-hidden
        >
          <path d={`${line}L${W},${H}L0,${H}Z`} fill="var(--s-allow-soft)" />
          <path
            d={line}
            fill="none"
            stroke="var(--s-allow)"
            strokeWidth={2}
            vectorEffect="non-scaling-stroke"
          />
        </svg>
        {hover !== null && (
          <div
            className="absolute inset-y-0 w-px bg-muted-foreground/50"
            style={{ left: at(hover) }}
          />
        )}
      </div>
      <div className="mt-3">
        <Legend color="var(--s-deny)">Отказы, за 15 мин</Legend>
      </div>
      <div className="relative h-10">
        <Grid max={maxD} />
        <svg
          className="absolute inset-0 size-full"
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          aria-hidden
        >
          {series.map((b, i) => {
            const h = (b.denied / maxD) * H;
            return h ? (
              <rect
                key={i}
                x={i * bw + 1}
                y={H - h}
                width={Math.max(1, bw - 2)}
                height={h}
                rx={1.5}
                fill="var(--s-deny)"
              />
            ) : null;
          })}
        </svg>
        {hover !== null && (
          <div
            className="absolute inset-y-0 w-px bg-muted-foreground/50"
            style={{ left: at(hover) }}
          />
        )}
        {ticks.map((i) => (
          <span
            key={i}
            className="absolute top-full mt-1 text-sm text-muted-foreground tabular-nums"
            style={{
              left: at(i),
              transform:
                i === 0 ? "none" : i === n - 1 ? "translateX(-100%)" : "translateX(-50%)",
            }}
          >
            {hhmm(series[i].start)}
          </span>
        ))}
      </div>
      {s && hover !== null && (
        <div
          className="pointer-events-none absolute top-6 z-10 min-w-[160px] rounded-md border bg-popover px-2.5 py-2 text-sm shadow-md"
          style={
            hover > n / 2
              ? { right: `calc(${100 - (hover / (n - 1)) * 100}% + 12px)` }
              : { left: `calc(${at(hover)} + 56px)` }
          }
        >
          <div className="mb-1 text-muted-foreground">
            {hhmm(s.start)}–{hhmm(series[Math.min(n - 1, hover + 1)].start)}
          </div>
          <div className="flex items-center gap-1.5">
            <i className="size-2.5 rounded-[3px] bg-s-allow" />
            Разрешено
            <b className="ml-auto pl-3 tabular-nums">{fmt(allowed[hover])}</b>
          </div>
          <div className="flex items-center gap-1.5">
            <i className="size-2.5 rounded-[3px] bg-s-deny" />
            Отказы
            <b className="ml-auto pl-3 tabular-nums">{fmt(s.denied)}</b>
          </div>
        </div>
      )}
    </div>
  );
}

// Sparkline: last 24 hours as hourly bars; the latest hour is fully opaque.
// An all-zero series is a flat baseline.
export function Sparkline({ series }: { series?: Bucket[] }) {
  const w = 56;
  const h = 18;
  const hours: number[] = [];
  for (let i = 0; i < (series?.length ?? 0); i += 4)
    hours.push(
      series!.slice(i, i + 4).reduce((sum, b) => sum + b.requests, 0),
    );
  if (hours.every((v) => !v))
    return (
      <svg className="block" width={w} height={h} aria-hidden>
        <line
          x1={0}
          y1={h - 1}
          x2={w}
          y2={h - 1}
          stroke="var(--border)"
          strokeWidth={2}
        />
      </svg>
    );
  const m = Math.max(1, ...hours);
  const bw = w / hours.length;
  return (
    <svg className="block" width={w} height={h} aria-hidden>
      {hours.map((v, i) => {
        const bh = Math.max(1.5, (v / m) * (h - 2));
        return (
          <rect
            key={i}
            x={i * bw + 0.4}
            y={h - bh}
            width={Math.max(0.6, bw - 1.2)}
            height={bh}
            rx={1}
            fill="var(--s-allow)"
            opacity={i === hours.length - 1 ? 1 : 0.55}
          />
        );
      })}
    </svg>
  );
}

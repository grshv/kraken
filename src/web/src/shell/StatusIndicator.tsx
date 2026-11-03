import { useEffect, useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { KeyValue } from "@/components/kraken/blocks";
import { Timestamp } from "@/components/kraken/timestamp";
import { cn } from "cn";
import type { Request } from "../api";
import type { Settings, Status } from "../model";
import { revisionLabel } from "../model";

// Parsed Squid manager figures from /squid/metrics (ADR 0010); raw output
// never reaches the browser.
type SquidMetrics = {
  available: boolean;
  squid_version?: string;
  uptime_seconds?: number;
};

// Header status indicator. The popover shows Squid state with
// the check time, then instance, applied revision, Squid version and uptime,
// ports, log ingestion and data volume; links to the apply history and (admin)
// instance settings. Metrics load when the popover opens.
export function StatusIndicator({
  status,
  settings,
  request,
  onOpenAudit,
  onOpenSettings,
}: {
  status: Status | null;
  settings?: Settings;
  request: Request;
  onOpenAudit: () => void;
  onOpenSettings?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [metrics, setMetrics] = useState<SquidMetrics | null>(null);
  useEffect(() => {
    if (!open) return;
    let live = true;
    request<SquidMetrics>("/squid/metrics")
      .then((m) => {
        if (live) setMetrics(m);
      })
      .catch(() => {
        if (live) setMetrics(null);
      });
    return () => {
      live = false;
    };
  }, [open, request]);
  const state = status?.squid.state ?? "unknown";
  const label =
    state === "up"
      ? "Работает"
      : state === "down"
        ? "Недоступен"
        : state === "degraded"
          ? "Ограничен"
          : "Нет данных";
  const dot = (
    <span
      className={cn(
        "size-2 shrink-0 rounded-full",
        state === "up"
          ? "bg-ok"
          : state === "down" || state === "degraded"
            ? "bg-bad"
            : "bg-muted-foreground",
      )}
      aria-hidden
    />
  );
  const links: [string, () => void][] = [["История применений", onOpenAudit]];
  if (onOpenSettings) links.push(["Настройки экземпляра", onOpenSettings]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-8 gap-2 px-2.5 text-base"
          aria-label={`Состояние сервера: ${label}`}
        >
          {dot}
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        collisionPadding={12}
        sideOffset={6}
        className="w-[340px] p-0"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <div className="flex items-baseline justify-between gap-3 border-b px-4 py-3">
          <span className="flex items-center gap-2 font-semibold">
            {dot}
            {state === "up"
              ? "Squid работает"
              : state === "unknown"
                ? "Нет данных о Squid"
                : "Squid не отвечает"}
          </span>
          {status?.squid.observed_at && (
            <span className="text-sm text-muted-foreground">
              проверено {relative(status.squid.observed_at)}
            </span>
          )}
        </div>
        <KeyValue
          className="px-4 py-3"
          items={statusItems(status, settings, metrics)}
        />
        <div className="flex flex-col border-t p-1">
          {links.map(([text, go]) => (
            <Button
              key={text}
              variant="ghost"
              size="sm"
              className="justify-between font-normal"
              onClick={() => {
                setOpen(false);
                go();
              }}
            >
              {text}
              <ChevronRight className="text-muted-foreground" />
            </Button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// statusItems lists the popover rows; a row without data shows a dash or is
// left out.
function statusItems(
  status: Status | null,
  settings: Settings | undefined,
  metrics: SquidMetrics | null,
): [ReactNode, ReactNode][] {
  const squid = status?.squid;
  const inst = status?.instance;
  const rev = status?.applied_revision ?? null;
  const m = metrics?.available ? metrics : null;
  const up = squid?.state === "up";
  const items: [ReactNode, ReactNode][] = [
    ["Экземпляр", inst?.name || "—"],
    [
      "Ревизия",
      rev ? (
        <>
          {revisionLabel(rev, status?.applied_revision_number)}
          {inst?.last_applied_at && (
            <>
              , применена{" "}
              <Timestamp
                value={inst.last_applied_at}
                mode="time"
                seconds={false}
              />
            </>
          )}
        </>
      ) : (
        "—"
      ),
    ],
    [
      "Squid",
      up ? (
        [
          m?.squid_version,
          m?.uptime_seconds != null
            ? `работает ${duration(m.uptime_seconds)}`
            : "работает",
        ]
          .filter(Boolean)
          .join(", ")
      ) : (
        <span className="text-bad">не отвечает</span>
      ),
    ],
  ];
  if (settings)
    items.push(["Порты", `${settings.http_port}, ${settings.tls_port} TLS`]);
  items.push([
    "Журнал",
    inst?.journal_checkpoint_at
      ? `${status?.ingestion?.state === "ok" ? "без пропусков, " : ""}${relative(inst.journal_checkpoint_at)}`
      : "—",
  ]);
  if (inst?.data_total_bytes)
    items.push([
      "Диск",
      `${gigabytes(inst.data_total_bytes - (inst.data_free_bytes ?? 0))} из ${gigabytes(inst.data_total_bytes)} ГБ`,
    ]);
  if (squid?.stale) items.push(["Данные", "устаревшие"]);
  return items;
}

const gigabytes = (n: number) =>
  (n / 1024 ** 3).toLocaleString("ru-RU", { maximumFractionDigits: 1 });

function duration(secs: number): string {
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const min = Math.floor((secs % 3600) / 60);
  if (d) return `${d} д ${h} ч`;
  if (h) return `${h} ч ${min} мин`;
  return `${min} мин`;
}

function relative(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "—";
  const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (secs < 60) return `${secs} с назад`;
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} мин назад`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} ч назад`;
  return `${Math.round(hours / 24)} д назад`;
}

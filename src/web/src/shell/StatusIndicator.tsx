import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { KeyValue } from "@/components/kraken/blocks";
import { cn } from "cn";
import type { Request } from "../api";
import type { Settings, Status } from "../model";
import { revisionLabel } from "../model";

// Parsed Squid manager figures from /squid/metrics (ADR 0013); raw output
// never reaches the browser.
type SquidMetrics = {
  available: boolean;
  squid_version?: string;
  uptime_seconds?: number;
  active_requests?: number;
  origin_requests_per_second?: number;
  cpu_usage_5m_percent?: number;
  memory_peak_bytes?: number;
};

// Header status indicator. The popover follows the prototype: instance,
// applied revision, Squid version and uptime, ports, current load, resources,
// log ingestion and data volume. Metrics load when the popover opens.
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
  const squidUp = status?.squid.state === "up";
  const state = status?.squid.state ?? "unknown";
  const label =
    state === "up"
      ? "Работает"
      : state === "down"
        ? "Недоступен"
        : state === "degraded"
          ? "Ограничен"
          : "Нет данных";
  const dotClass =
    state === "up"
      ? "bg-ok"
      : state === "down" || state === "degraded"
        ? "bg-bad"
        : "bg-muted-foreground";

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-8 gap-2 px-2.5 text-base"
          aria-label={`Состояние сервера: ${label}`}
        >
          <span className={cn("size-2 rounded-full", dotClass)} aria-hidden />
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        sideOffset={4}
        className="w-[340px] px-3.5 py-3"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <StatusPopoverBody
          status={status}
          settings={settings}
          metrics={metrics}
          squidUp={squidUp}
        />
        <div className="mt-2.5 flex justify-between gap-2 border-t pt-2.5">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setOpen(false);
              onOpenAudit();
            }}
          >
            История применений
          </Button>
          {onOpenSettings && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setOpen(false);
                onOpenSettings();
              }}
            >
              Настройки экземпляра
            </Button>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function StatusPopoverBody({
  status,
  settings,
  metrics,
  squidUp,
}: {
  status: Status | null;
  settings?: Settings;
  metrics: SquidMetrics | null;
  squidUp: boolean;
}) {
  const squid = status?.squid;
  const inst = status?.instance;
  const rev = status?.applied_revision ?? null;
  const m = metrics?.available ? metrics : null;
  const items: [ReactNode, ReactNode][] = [
    ["Экземпляр", inst?.name || "—"],
    [
      "Ревизия",
      rev ? (
        <>
          <span className="font-mono text-sm">
            {revisionLabel(rev, status?.applied_revision_number)}
          </span>
          {inst?.last_applied_at &&
            ` · применена ${clock(inst.last_applied_at)}`}
        </>
      ) : (
        "—"
      ),
    ],
    [
      "Squid",
      m?.squid_version
        ? `${m.squid_version}${m.uptime_seconds != null ? ` · аптайм ${duration(m.uptime_seconds)}` : ""}`
        : squidUp
          ? "работает"
          : "недоступен",
    ],
  ];
  if (settings)
    items.push([
      "Порты",
      <span className="font-mono text-sm">
        :{settings.http_port} · :{settings.tls_port} TLS
      </span>,
    ]);
  if (m)
    items.push(
      [
        "Сейчас",
        `${m.active_requests ?? 0} активных запросов · ${num(m.origin_requests_per_second ?? 0)} запрос/с`,
      ],
      [
        "Ресурсы",
        `CPU ${num(m.cpu_usage_5m_percent ?? 0)}% · память ${bytes(m.memory_peak_bytes ?? 0)}`,
      ],
    );
  items.push([
    "Журнал",
    inst?.journal_checkpoint_at
      ? `собран ${relative(inst.journal_checkpoint_at)}${status?.ingestion?.state === "ok" ? ", без пропусков" : ""}`
      : "—",
  ]);
  if (inst?.data_total_bytes)
    items.push([
      "Диск /data",
      `${bytes(inst.data_total_bytes - (inst.data_free_bytes ?? 0))} из ${bytes(inst.data_total_bytes)}`,
    ]);
  if (squid?.stale) items.push(["Данные", "устаревшие"]);
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between gap-2.5">
        <Badge variant={squidUp ? "ok" : "bad"}>
          <span className="size-1.5 rounded-full bg-current" aria-hidden />
          {squidUp ? "Squid работает" : "Squid не отвечает"}
        </Badge>
        {squid?.observed_at && (
          <span className="text-sm text-muted-foreground">
            проверено {relative(squid.observed_at)}
          </span>
        )}
      </div>
      <KeyValue items={items} />
    </div>
  );
}

const num = (n: number) =>
  n.toLocaleString("ru-RU", { maximumFractionDigits: 1 });

function bytes(n: number): string {
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${num(n)} ${units[i]}`;
}

function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

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

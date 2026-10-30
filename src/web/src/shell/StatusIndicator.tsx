import { useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { KeyValue } from "@/components/kraken/blocks";
import { cn } from "cn";
import type { Status } from "../model";

// Compact status indicator in the header. Click toggles a popover with the
// fields that /status actually returns: Squid up/down, instance name (from
// capabilities or applied_revision), last apply, log freshness. Missing
// fields render as an em dash, per the prototype.
export function StatusIndicator({
  status,
  onOpenAudit,
}: {
  status: Status | null;
  onOpenAudit: () => void;
}) {
  const [open, setOpen] = useState(false);
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
          className="h-8 gap-2 px-2.5"
          aria-label={`Состояние сервера: ${label}`}
        >
          <span className={cn("size-2 rounded-full", dotClass)} aria-hidden />
          <span className="text-sm">{label}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        sideOffset={4}
        className="w-80 px-3.5 py-3"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <StatusPopoverBody status={status} squidUp={squidUp} />
        <div className="mt-2.5 flex justify-end border-t pt-2.5">
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
        </div>
      </PopoverContent>
    </Popover>
  );
}

function StatusPopoverBody({
  status,
  squidUp,
}: {
  status: Status | null;
  squidUp: boolean;
}) {
  const squid = status?.squid;
  const runtime = status?.runtime;
  const rev = status?.applied_revision ?? null;
  const items: [ReactNode, ReactNode][] = [
    ["Экземпляр", "—"],
    ["Ревизия", rev ? shortId(rev) : "—"],
    ["Squid", squid?.detail || (squidUp ? "работает" : "недоступен")],
    [
      "Свежесть журнала",
      runtime?.observed_at ? relative(runtime.observed_at) : "—",
    ],
  ];
  if (squid?.stale) items.push(["Данные", "устаревшие"]);
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2.5">
        <Badge variant={squidUp ? "ok" : "bad"}>
          <span className="size-1.5 rounded-full bg-current" aria-hidden />
          {squidUp ? "Squid работает" : squid?.detail || "Squid не отвечает"}
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

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
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

// Re-export so the shell can render the audit nav chevron consistently.
export { ChevronRight };

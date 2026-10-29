import { useState } from "react";
import { ChevronRight } from "lucide-react";
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
  const dotClass = `status-dot ${state}`;

  return (
    <div className="status-indicator">
      <button
        type="button"
        className="status-trigger"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={`Состояние сервера: ${label}`}
        onClick={() => setOpen((v) => !v)}
      >
        <span className={dotClass} aria-hidden />
        <span className="status-label">{label}</span>
      </button>
      {open && (
        <>
          <div
            className="popover-overlay"
            aria-hidden
            onClick={() => setOpen(false)}
          />
          <div
            role="dialog"
            aria-label="Состояние сервера"
            className="status-popover"
          >
            <StatusPopoverBody status={status} squidUp={squidUp} />
            <div className="status-popover-footer">
              <button
                type="button"
                className="link-button"
                onClick={() => {
                  setOpen(false);
                  onOpenAudit();
                }}
              >
                История применений
              </button>
            </div>
          </div>
        </>
      )}
    </div>
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
  const observeText = (s: string | null) => s ?? "—";
  return (
    <dl className="status-kv">
      <div className="status-row">
        <span className={"badge " + (squidUp ? "badge-ok" : "badge-bad")}>
          <span className="status-dot" aria-hidden />
          {squidUp ? "Squid работает" : squid?.detail || "Squid не отвечает"}
        </span>
        {squid?.observed_at && (
          <span className="muted-text small">
            проверено {relative(squid.observed_at)}
          </span>
        )}
      </div>
      <KV label="Экземпляр" value="—" />
      <KV label="Ревизия" value={rev ? shortId(rev) : "—"} />
      <KV
        label="Squid"
        value={squid?.detail || (squidUp ? "работает" : "недоступен")}
      />
      <KV
        label="Свежесть журнала"
        value={runtime?.observed_at ? relative(runtime.observed_at) : "—"}
      />
      {squid?.stale && <KV label="Данные" value="устаревшие" />}
    </dl>
  );
}

function KV({ label, value }: { label: string; value: string }) {
  return (
    <div className="kv-row">
      <dt>{label}</dt>
      <dd>{value}</dd>
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

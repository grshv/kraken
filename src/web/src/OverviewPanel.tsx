import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "./StatusBadge";
import type { Status, Event } from "./model";
import type { Statistics, Ingestion } from "./useOverview";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
const number = (value?: number) =>
  value === undefined ? "—" : new Intl.NumberFormat("ru-RU").format(value);
function bytes(value?: number) {
  if (value === undefined) return "—";
  if (value < 1024) return `${value} Б`;
  const units = ["КиБ", "МиБ", "ГиБ", "ТиБ"];
  let n = value / 1024,
    i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(n)} ${units[i]}`;
}
function time(value: string | null | undefined) {
  return value ? new Date(value).toLocaleTimeString("ru-RU") : "—";
}
export function OverviewPanel({
  status,
  statistics,
  ingestion,
  events,
  offline,
  error,
  onEvents,
}: {
  status: Status | null;
  statistics: Statistics | null;
  ingestion: Ingestion | null;
  events: Event[];
  offline: boolean;
  error: string;
  onEvents: () => void;
}) {
  const max = Math.max(1, ...(statistics?.hourly.map((h) => h.requests) || []));
  return (
    <>
      <section className="health-grid" aria-label="Состояние прокси">
        {(
          [
            ["Squid", status?.squid],
            ["Управление", status?.runtime],
          ] as const
        ).map(([title, value]) => (
          <Card key={title} className="health-card">
            <div className="flex items-center justify-between gap-4">
              <h2>{title}</h2>
              <StatusBadge
                value={
                  offline || value?.stale
                    ? "unknown"
                    : value?.state || "unknown"
                }
              />
            </div>
            <p className="subtle text-xs">
              {offline
                ? "Связь с панелью потеряна"
                : value?.stale
                  ? "Состояние не обновилось"
                  : `Проверено ${time(value?.observed_at)}`}
            </p>
          </Card>
        ))}
      </section>
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      <section aria-label="Статистика за 24 часа">
        <dl className="metrics-grid">
          {[
            ["Запросы за 24 часа", number(statistics?.requests)],
            ["Отказы", number(statistics?.denied)],
            ["Наблюдаемые байты", bytes(statistics?.bytes_observed)],
            ["IP-источники", number(statistics?.unique_sources)],
          ].map(([label, value]) => (
            <Card className="metric-card" key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </Card>
          ))}
        </dl>
        {ingestion &&
          (!ingestion.source_available ||
            ingestion.stale ||
            ingestion.backlog) && (
            <p className="notice" role="status">
              {!ingestion.observed_at
                ? "Ожидается первое чтение журнала Squid."
                : !ingestion.source_available
                  ? "Журнал Squid недоступен. Показаны сохранённые данные."
                  : ingestion.stale
                    ? "Данные журнала устарели."
                    : "Журнал ещё обрабатывается. Статистика неполная."}
            </p>
          )}
        {statistics?.retention_limited && (
          <p className="notice">
            Достигнут предел сохранённых записей: статистика суток может быть
            неполной.
          </p>
        )}
        <section className="section">
          <div className="section-head">
            <h2>Запросы по часам</h2>
            <span className="subtle text-xs">Последние 24 часа</span>
          </div>
          {statistics ? (
            <div className="request-chart">
              <svg
                role="img"
                aria-label="Запросы и отказы по часам за последние 24 часа"
                viewBox="0 0 960 160"
              >
                <title>
                  Количество запросов: {statistics.requests}, отказов:{" "}
                  {statistics.denied}
                </title>
                {statistics.hourly.map((h, i) => (
                  <g key={h.from}>
                    <title>
                      {new Date(h.from).toLocaleString("ru-RU")}: {h.requests}{" "}
                      запросов, {h.denied} отказов
                    </title>
                    <rect
                      x={i * 40 + 5}
                      y={140 - (h.requests / max) * 120}
                      width="26"
                      height={
                        h.requests ? Math.max(2, (h.requests / max) * 120) : 0
                      }
                      rx="3"
                      fill="var(--primary)"
                    />
                    <rect
                      x={i * 40 + 5}
                      y={140 - (h.denied / max) * 120}
                      width="26"
                      height={(h.denied / max) * 120}
                      rx="3"
                      fill="var(--destructive)"
                    />
                  </g>
                ))}
              </svg>
              <div className="chart-axis" aria-hidden="true">
                {[0, 6, 12, 18, 23].map((i) => (
                  <span key={i}>
                    {new Date(statistics.hourly[i].from).toLocaleTimeString(
                      "ru-RU",
                      { hour: "2-digit", minute: "2-digit" },
                    )}
                  </span>
                ))}
              </div>
              <div className="chart-legend">
                <span>
                  <i className="requests-key" />
                  Запросы
                </span>
                <span>
                  <i className="denied-key" />
                  Отказы
                </span>
                <span className="ml-auto">
                  Наблюдаемые записи журнала; CONNECT учитывается после
                  закрытия.
                </span>
              </div>
            </div>
          ) : (
            <p className="empty">
              {error || offline
                ? "Статистика временно недоступна"
                : "Загрузка статистики…"}
            </p>
          )}
        </section>
      </section>
      <section className="section">
        <div className="section-head">
          <h2>Последние запросы</h2>
          <Button variant="ghost" onClick={onEvents}>
            Весь журнал
          </Button>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Время</TableHead>
              <TableHead>Источник</TableHead>
              <TableHead>Назначение</TableHead>
              <TableHead>Результат</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {events.slice(0, 6).map((e) => (
              <TableRow key={e.id}>
                <TableCell>{time(e.timestamp)}</TableCell>
                <TableCell className="mono">{e.source_ip}</TableCell>
                <TableCell className="mono destination">
                  {e.destination}
                </TableCell>
                <TableCell
                  className={e.proxy_result.includes("DENIED") ? "denied" : ""}
                >
                  {e.proxy_result}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {!events.length && <p className="empty">Запросов пока нет.</p>}
      </section>
    </>
  );
}

import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "./StatusBadge";
import type { Status, Event } from "./model";
import type { Statistics, Ingestion, SquidMetrics } from "./useOverview";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
function amount(value: number | null | undefined, decimals = 0) {
  return value == null
    ? "—"
    : new Intl.NumberFormat("ru-RU", {
        maximumFractionDigits: decimals,
      }).format(value);
}
function bytes(value: number | null | undefined) {
  if (value == null) return "—";
  const units = ["Б", "КиБ", "МиБ", "ГиБ", "ТиБ"];
  let i = 0,
    n = value;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${amount(n, i ? 1 : 0)} ${units[i]}`;
}
function uptime(value: number | null | undefined) {
  if (value == null) return "—";
  const seconds = Math.floor(value),
    days = Math.floor(seconds / 86400),
    hours = Math.floor(seconds / 3600) % 24,
    minutes = Math.floor(seconds / 60) % 60;
  return days
    ? `${days} д ${hours} ч`
    : hours
      ? `${hours} ч ${minutes} мин`
      : minutes
        ? `${minutes} мин ${seconds % 60} с`
        : `${seconds} с`;
}
function time(value: string | null | undefined) {
  return value ? new Date(value).toLocaleTimeString("ru-RU") : "—";
}
function rate(value: number | null | undefined) {
  return value == null ? "—" : `${bytes(value)}/с`;
}
export function OverviewPanel({
  status,
  statistics,
  metrics,
  failures,
  ingestion,
  offline,
  error,
  onEvents,
}: {
  status: Status | null;
  statistics: Statistics | null;
  metrics: SquidMetrics | null;
  failures: Event[];
  ingestion: Ingestion | null;
  offline: boolean;
  error: string;
  onEvents: (failures?: boolean) => void;
}) {
  const stale = offline || metrics?.stale === true;
  const samples = metrics?.available || metrics?.stale;
  return (
    <>
      <Card
        className="squid-overview"
        role="region"
        aria-label="Состояние прокси"
      >
        <div className="squid-overview-head">
          <div className="actions">
            <h2>
              Squid{metrics?.squid_version ? ` ${metrics.squid_version}` : ""}
            </h2>
            <StatusBadge
              value={
                offline || status?.squid.stale
                  ? "unknown"
                  : status?.squid.state || "unknown"
              }
            />
          </div>
          <span className="subtle">
            В работе: {uptime(metrics?.uptime_seconds)}
          </span>
        </div>
        {samples ? (
          <>
            <dl className="live-metrics" aria-label="Текущая нагрузка Squid">
              <div>
                <dt>Активные запросы</dt>
                <dd>{amount(metrics?.active_requests)}</dd>
              </div>
              <div>
                <dt>
                  {metrics?.cpu_usage_5m_percent != null
                    ? "CPU за 5 минут"
                    : "CPU с запуска"}
                </dt>
                <dd>
                  {metrics?.cpu_usage_5m_percent != null
                    ? `${amount(metrics.cpu_usage_5m_percent, 2)}%`
                    : metrics?.cpu_usage_percent != null
                      ? `${amount(metrics.cpu_usage_percent, 2)}%`
                      : "—"}
                </dd>
              </div>
              <div>
                <dt>Пик памяти</dt>
                <dd>{bytes(metrics?.memory_peak_bytes)}</dd>
              </div>
              <div>
                <dt title="Среднее за интервал Squid до 5 минут">
                  К ресурсам, запросов/с
                </dt>
                <dd>{amount(metrics?.origin_requests_per_second, 2)}</dd>
              </div>
              <div>
                <dt title="Среднее за интервал Squid до 5 минут">
                  Получение от ресурсов
                </dt>
                <dd>{rate(metrics?.origin_bytes_in_per_second)}</dd>
              </div>
              <div>
                <dt title="Среднее за интервал Squid до 5 минут">
                  Отправка ресурсам
                </dt>
                <dd>{rate(metrics?.origin_bytes_out_per_second)}</dd>
              </div>
            </dl>
            <dl className="squid-totals">
              <div>
                <dt>С запуска: запросов к ресурсам</dt>
                <dd>{amount(metrics?.origin_requests_total)}</dd>
              </div>
              <div>
                <dt>Ошибок обращения</dt>
                <dd>{amount(metrics?.origin_errors_total)}</dd>
              </div>
              <div>
                <dt>Получено</dt>
                <dd>{bytes(metrics?.origin_bytes_in_total)}</dd>
              </div>
              <div>
                <dt>Отправлено</dt>
                <dd>{bytes(metrics?.origin_bytes_out_total)}</dd>
              </div>
            </dl>
            <div className="squid-observation">
              <span>
                {stale ? "Последние данные" : "Обновлено"}{" "}
                {time(metrics?.observed_at)}
              </span>
              <span>
                HTTP-кеш:{" "}
                {metrics?.cache_enabled === false
                  ? "выключен"
                  : metrics?.cache_hit_ratio_percent != null
                    ? `${amount(metrics.cache_hit_ratio_percent, 1)}% попаданий за 5 минут`
                    : "нет данных"}
              </span>
            </div>
          </>
        ) : (
          <p className="metrics-unavailable" role="status">
            {!metrics
              ? "Получение показателей Squid…"
              : metrics.reason === "configuration_update_required"
                ? "Для чтения показателей требуется обновление служебной конфигурации Squid."
                : "Не удалось прочитать показатели Squid."}
          </p>
        )}
      </Card>
      {stale && (
        <p className="notice" role="status">
          Показатели Squid не обновились. Время последнего чтения указано выше.
        </p>
      )}
      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}
      <section className="section" aria-label="Статистика за 24 часа">
        <div className="section-head">
          <h2>Доступ за 24 часа</h2>
          <Button variant="ghost" onClick={() => onEvents(false)}>
            История запросов
          </Button>
        </div>
        <dl className="history-summary">
          <div>
            <dt>Запросы за 24 часа</dt>
            <dd>{amount(statistics?.requests)}</dd>
          </div>
          <div>
            <dt>Отказы</dt>
            <dd>{amount(statistics?.denied)}</dd>
          </div>
          <div>
            <dt>IP-источники</dt>
            <dd>{amount(statistics?.unique_sources)}</dd>
          </div>
        </dl>
        {ingestion?.stale && ingestion.observed_at && (
          <p className="inline-note">
            Сбор журнала задерживается. Сводка может быть неполной.
          </p>
        )}
        {statistics?.retention_limited && (
          <p className="inline-note">
            Достигнут предел сохранённых записей; сводка суток неполная.
          </p>
        )}
      </section>
      <section className="section">
        <div className="section-head">
          <h2>Последние ошибки и отказы</h2>
          <Button variant="ghost" onClick={() => onEvents(true)}>
            Все ошибки
          </Button>
        </div>
        {failures.length ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Время</TableHead>
                <TableHead>Клиент / источник</TableHead>
                <TableHead>Назначение</TableHead>
                <TableHead>Результат</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {failures.map((e) => (
                <TableRow key={e.id}>
                  <TableCell>{time(e.timestamp)}</TableCell>
                  <TableCell>{e.client_name || e.source_ip}</TableCell>
                  <TableCell className="mono destination">
                    {e.destination}
                  </TableCell>
                  <TableCell>{e.proxy_result}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="overview-empty">
            {statistics?.requests === 0
              ? "Записей запросов пока нет."
              : "В последних записях ошибок нет."}
          </p>
        )}
      </section>
    </>
  );
}

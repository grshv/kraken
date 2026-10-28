import { CardHeader, CardTitle } from "@/components/ui/card";
import { Alert } from "@/components/ui/alert";
import { Disclosure } from "./components/Disclosure";
import { Panel } from "./components/Panel";
import { Button } from "@/components/ui/button";
import { SelectField } from "./components/SelectField";
import { SelectItem } from "@/components/ui/select";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { StatusBadge } from "./StatusBadge";
import type { Status } from "./model";
import type { ActivityReport, SquidMetrics } from "./useOverview";

export type EventPreset = {
  source_ip?: string;
  destination?: string;
  result?: string;
  from?: string;
  to?: string;
};
function number(value: number | null | undefined, digits = 0) {
  return value == null
    ? "—"
    : new Intl.NumberFormat("ru-RU", { maximumFractionDigits: digits }).format(
        value,
      );
}
function bytes(value: number | null | undefined) {
  if (value == null) return "—";
  let n = value,
    i = 0;
  const units = ["Б", "КиБ", "МиБ", "ГиБ", "ТиБ"];
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${number(n, i ? 1 : 0)} ${units[i]}`;
}
function time(value: string | null | undefined, includeDate = false) {
  if (!value) return "—";
  return includeDate
    ? new Date(value).toLocaleString("ru-RU", {
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
      })
    : new Date(value).toLocaleTimeString("ru-RU");
}
function age(value: number | null | undefined) {
  if (value == null) return "—";
  const hours = Math.floor(value / 3600);
  return hours
    ? `${hours} ч ${Math.floor(value / 60) % 60} мин`
    : `${Math.floor(value / 60)} мин`;
}
function issueLabel(result: string) {
  if (result.includes("/407")) return "Требуется авторизация";
  if (result.includes("DENIED")) return "Доступ отклонён";
  if (result.includes("/404")) return "Ресурс не найден";
  if (result.includes("/503")) return "Ресурс недоступен";
  if (result.includes("ABORTED")) return "Соединение прервано";
  return "Ошибка обращения";
}
export function OverviewPanel({
  status,
  metrics,
  activity,
  minutes,
  onMinutes,
  offline,
  error,
  onEvents,
  onConfigure,
}: {
  status: Status | null;
  metrics: SquidMetrics | null;
  activity: ActivityReport | null;
  minutes: string;
  onMinutes: (value: string) => void;
  offline: boolean;
  error: string;
  onEvents: (failures?: boolean, preset?: EventPreset) => void;
  onConfigure?: () => void;
}) {
  const data = activity?.activity;
  const ingestion = activity?.ingestion;
  const stale = offline || status?.squid.stale || metrics?.stale;
  const historyStale =
    ingestion &&
    (!ingestion.source_available ||
      Date.now() - Date.parse(ingestion.observed_at) > 15000);
  const applied = activity?.applied;
  const sourceName = (source: {
    client_name: string | null;
    source_ip: string;
  }) => (
    <>
      <strong>{source.client_name || source.source_ip}</strong>
      {source.client_name && (
        <small className="row-description mono">{source.source_ip}</small>
      )}
    </>
  );
  const period = { from: data?.from, to: data?.to };
  return (
    <>
      <Panel
        className="section operational-state"
        aria-label="Состояние прокси"
      >
        <CardHeader className="operational-card-header">
          <div className="actions">
            <CardTitle>
              <h2>Состояние прокси</h2>
            </CardTitle>
            <StatusBadge
              value={stale ? "unknown" : status?.squid.state || "unknown"}
            />
          </div>
          <span className="subtle">Обновлено {time(metrics?.observed_at)}</span>
        </CardHeader>
        <div className="operational-status">
          <strong>Squid {metrics?.squid_version ?? ""}</strong>
          <span>В работе {age(metrics?.uptime_seconds)}</span>
          <span>
            Запросов сейчас <strong>{number(metrics?.active_requests)}</strong>
          </span>
        </div>
        <div className="applied-access">
          <div>
            <strong>Применённые правила</strong>
            <p>
              {!applied ? (
                "Не удалось прочитать подтверждённую конфигурацию"
              ) : applied.mode === "expert" ? (
                "Экспертный squid.conf"
              ) : applied.enabled_clients === 0 ? (
                "Нет включённых клиентов — доступ закрыт"
              ) : (
                <>
                  {applied.enabled_clients} включённых · по списку доменов:{" "}
                  {applied.allowlist_clients} · весь web:{" "}
                  {applied.all_web_clients}
                </>
              )}
            </p>
          </div>
          {onConfigure && (
            <Button variant="ghost" onClick={onConfigure}>
              Правила доступа
            </Button>
          )}
        </div>
        {status?.runtime.stale || status?.runtime.state === "degraded" ? (
          <p className="inline-note">
            Состояние применения не подтверждено. Показаны последние сохранённые
            правила.
          </p>
        ) : null}
      </Panel>
      {error && (
        <Alert className="notice" role="alert">
          {error}
        </Alert>
      )}
      <Panel
        className="section operational-activity"
        aria-label="Диагностика доступа"
      >
        <CardHeader className="section-head operational-card-header">
          <CardTitle>
            <h2>Диагностика доступа</h2>
          </CardTitle>
          <div className="actions">
            <SelectField
              aria-label="Период активности"
              value={minutes}
              onValueChange={(value) => onMinutes(value)}
            >
              <SelectItem value="15">За 15 минут</SelectItem>
              <SelectItem value="60">За час</SelectItem>
              <SelectItem value="1440">За день</SelectItem>
              <SelectItem value="10080">За неделю</SelectItem>
              <SelectItem value="43200">За месяц</SelectItem>
            </SelectField>
            <Button variant="ghost" onClick={() => onEvents(true, period)}>
              Журнал
            </Button>
          </div>
        </CardHeader>
        <div className="activity-group-title">
          <h3>Отказы и ошибки</h3>
        </div>

        {!data ? (
          <p className="overview-empty">Загрузка обращений…</p>
        ) : !data.issues.length ? (
          <p className="overview-empty">
            За выбранный период отказы и ошибки не записаны.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Клиент / источник</TableHead>
                <TableHead>Назначение</TableHead>
                <TableHead>Результат</TableHead>
                <TableHead>Повторов</TableHead>
                <TableHead>Последний</TableHead>
                <TableHead>
                  <span className="sr-only">Действия</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.issues.map((item, i) => (
                <TableRow key={i}>
                  <TableCell>
                    {sourceName(item)}
                    {item.proxy_username && (
                      <small className="row-description">
                        Логин: {item.proxy_username}
                      </small>
                    )}
                  </TableCell>
                  <TableCell className="destination mono">
                    {item.destination}
                  </TableCell>
                  <TableCell>
                    {issueLabel(item.result)}
                    <small className="row-description mono">
                      {item.result}
                    </small>
                  </TableCell>
                  <TableCell>{number(item.count)}</TableCell>
                  <TableCell>
                    {time(item.last_seen, Number(minutes) >= 1440)}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      onClick={() =>
                        onEvents(true, {
                          ...period,
                          source_ip: item.source_ip,
                          destination: item.destination,
                          result: item.result,
                        })
                      }
                    >
                      Записи
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {data && data.total_issues > data.issues.length && (
          <p className="inline-note">
            Показаны 10 самых частых сочетаний. Остальные — в журнале.
          </p>
        )}
        <div className="section-head activity-subhead">
          <h3>Кто использует прокси</h3>
          <span className="subtle">Завершённые обращения за тот же период</span>
        </div>
        {!data ? (
          <p className="overview-empty">Загрузка клиентов…</p>
        ) : !data.sources.length ? (
          <p className="overview-empty">
            Завершённых обращений нет. Открытые CONNECT-туннели появятся в
            журнале после закрытия.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Клиент / источник</TableHead>
                <TableHead>Обращения</TableHead>
                <TableHead>Отказы</TableHead>
                <TableHead>Ошибки ответа</TableHead>
                <TableHead>Объём по журналу</TableHead>
                <TableHead>Последний</TableHead>
                <TableHead>
                  <span className="sr-only">Действия</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.sources.map((item, i) => (
                <TableRow key={i}>
                  <TableCell>
                    {sourceName(item)}
                    {item.proxy_username && (
                      <small className="row-description">
                        Логин: {item.proxy_username}
                      </small>
                    )}
                  </TableCell>
                  <TableCell>{number(item.requests)}</TableCell>
                  <TableCell className={item.denied ? "denied" : ""}>
                    {number(item.denied)}
                  </TableCell>
                  <TableCell className={item.errors ? "denied" : ""}>
                    {number(item.errors)}
                  </TableCell>
                  <TableCell>{bytes(item.bytes_observed)}</TableCell>
                  <TableCell>
                    {time(item.last_seen, Number(minutes) >= 1440)}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      onClick={() =>
                        onEvents(false, {
                          ...period,
                          source_ip: item.source_ip,
                        })
                      }
                    >
                      Журнал
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {data && data.total_sources > data.sources.length && (
          <p className="inline-note">
            Показаны 10 из {data.total_sources} источников с наибольшим числом
            обращений.
          </p>
        )}
        {(historyStale || ingestion?.gap || data?.retention_limited) && (
          <p className="inline-note">
            {historyStale ? "Сбор журнала задерживается. " : ""}
            {ingestion?.gap || data?.retention_limited
              ? "История неполная; часть обращений может отсутствовать."
              : ""}
          </p>
        )}
      </Panel>
      <Disclosure
        className="squid-technical"
        title={<> Технические показатели Squid </>}
      >
        <dl className="technical-values">
          <div>
            <dt>CPU, среднее за 5 минут</dt>
            <dd>
              {metrics?.cpu_usage_5m_percent == null
                ? "—"
                : `${number(metrics.cpu_usage_5m_percent, 2)}%`}
            </dd>
          </div>
          <div>
            <dt>Пик памяти</dt>
            <dd>{bytes(metrics?.memory_peak_bytes)}</dd>
          </div>
          <div>
            <dt>Запросы к ресурсам / с</dt>
            <dd>{number(metrics?.origin_requests_per_second, 2)}</dd>
          </div>
          <div>
            <dt>Получение / с</dt>
            <dd>
              {metrics?.origin_bytes_in_per_second == null
                ? "—"
                : `${bytes(metrics.origin_bytes_in_per_second)}/с`}
            </dd>
          </div>
          <div>
            <dt>HTTP-кеш</dt>
            <dd>
              {metrics?.cache_enabled === false
                ? "Выключен"
                : metrics?.cache_hit_ratio_percent == null
                  ? "Нет данных"
                  : `${number(metrics.cache_hit_ratio_percent, 1)}% попаданий`}
            </dd>
          </div>
        </dl>
      </Disclosure>
    </>
  );
}

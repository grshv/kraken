import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Table } from "@/components/ui/table";
import { TableHeader } from "@/components/ui/table";
import { TableBody } from "@/components/ui/table";
import { TableRow } from "@/components/ui/table";
import { TableHead } from "@/components/ui/table";
import { TableCell } from "@/components/ui/table";
import { useEffect, useRef, useState } from "react";
import type { EventPreset } from "./OverviewPanel";
import type { Event } from "./model";

type Audit = {
  id: string;
  actor: string;
  action: string;
  entity_id: string;
  timestamp: string;
};
type Ingestion = {
  observed_at: string;
  stale: boolean;
  source_available: boolean;
  backlog: boolean;
  dropped_records: number;
};
type Page<T> = {
  items: T[];
  next_cursor: string | null;
  gap: boolean;
  observed_at: string;
  ingestion?: Ingestion;
};
type Request = <T>(
  path: string,
  method?: string,
  data?: unknown,
  key?: string,
  signal?: AbortSignal,
) => Promise<T>;
function recordCount(value: number) {
  const form = new Intl.PluralRules("ru-RU").select(value);
  return `${value} ${form === "one" ? "запись" : form === "few" ? "записи" : "записей"}`;
}
function date(value: string) {
  return new Date(value).toLocaleString("ru-RU", { timeZoneName: "short" });
}
const actions: Record<string, string> = {
  create_user: "Создание пользователя",
  update_user: "Изменение пользователя",
  create_proxy_account: "Создание логина прокси",
  update_proxy_account: "Изменение логина прокси",
  reset_proxy_password: "Смена пароля прокси",
  login: "Вход",
  logout: "Выход",
  login_failed: "Ошибка входа",
  password_reset: "Сброс пароля",
  create_revision: "Создание ревизии",
  apply: "Применение",
  validate: "Проверка",
  rollback: "Откат",
  update_draft: "Изменение черновика",
  csrf_failed: "Отклонён CSRF",
  service_update: "Обновление служебной конфигурации",
  rate_limited: "Ограничение входа",
  session_capacity_reached: "Достигнут лимит сессий",
};
export function DiagnosticsPanel({
  kind,
  request,
  initialFailures = false,
  initialFilters = {},
}: {
  kind: "events" | "audit";
  request: Request;
  initialFailures?: boolean;
  initialFilters?: EventPreset;
}) {
  const api = useRef(request);
  api.current = request;
  const [data, setData] = useState<Page<Event | Audit> | null>(null);
  const [client, setClient] = useState("");
  const [failures, setFailures] = useState(initialFailures);
  const [clients, setClients] = useState<{ id: string; name: string }[]>([]);
  const [clientError, setClientError] = useState("");
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [filters, setFilters] = useState({ actor: "", action: "" });
  const emptyEventFilters = {
    source_ip: "",
    destination: "",
    method: "",
    result: "",
    from: "",
    to: "",
  };
  function localDate(value?: string) {
    if (!value) return "";
    const d = new Date(value);
    return new Date(d.getTime() - d.getTimezoneOffset() * 60000)
      .toISOString()
      .slice(0, 23);
  }
  const initialEventFilters = {
    ...emptyEventFilters,
    ...initialFilters,
    from: localDate(initialFilters.from),
    to: localDate(initialFilters.to),
  };
  const [eventInput, setEventInput] = useState(initialEventFilters);
  const [eventFilters, setEventFilters] = useState(initialEventFilters);
  const [limit, setLimit] = useState("50");
  const [paused, setPaused] = useState(false);
  const [previousPages, setPreviousPages] = useState<string[]>([]);
  const [cursor, setCursor] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [receivedAt, setReceivedAt] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    const query = new URLSearchParams({ limit });
    if (cursor) query.set("cursor", cursor);
    if (kind === "events") {
      if (client) query.set("client_id", client);
      query.set("failures_only", String(failures));
      for (const [key, value] of Object.entries(eventFilters)) {
        if (value)
          query.set(
            key,
            key === "from" || key === "to"
              ? new Date(value).toISOString()
              : value.trim(),
          );
      }
    } else {
      if (filters.actor) query.set("actor", filters.actor);
      if (filters.action) query.set("action", filters.action);
    }
    async function load() {
      setLoading(true);
      try {
        const response = await api.current<Page<Event | Audit>>(
          `/${kind}?${query}`,
          "GET",
          undefined,
          undefined,
          controller.signal,
        );
        if (active) {
          setData(response);
          setError("");
          setReceivedAt(date(new Date().toISOString()));
        }
      } catch (e) {
        if (active && !(e instanceof DOMException && e.name === "AbortError"))
          setError((e as Error).message);
      } finally {
        if (active) setLoading(false);
      }
    }
    void load();
    const timer =
      cursor || paused
        ? undefined
        : window.setInterval(() => void load(), 10000);
    return () => {
      active = false;
      controller.abort();
      if (timer) clearInterval(timer);
    };
  }, [
    kind,
    client,
    failures,
    filters,
    eventFilters,
    limit,
    paused,
    cursor,
    refresh,
  ]);
  useEffect(() => {
    if (kind !== "events") return;
    const controller = new AbortController();
    api
      .current<{ items: { id: string; name: string }[] }>(
        "/events/clients",
        "GET",
        undefined,
        undefined,
        controller.signal,
      )
      .then((r) => {
        setClients(r.items);
        setClientError("");
      })
      .catch((e) => {
        if (!(e instanceof DOMException && e.name === "AbortError"))
          setClientError(
            "Список клиентов не обновился. Нажмите «Последние записи», чтобы повторить.",
          );
      });
    return () => controller.abort();
  }, [kind, refresh]);
  function reset() {
    setData(null);
    setCursor("");
    setPreviousPages([]);
  }
  const ingestion = data?.ingestion;
  return (
    <section
      className="section diagnostics"
      aria-label={kind === "events" ? "История запросов" : "Журнал аудита"}
    >
      <div className="section-head">
        <div>
          <h2>
            {kind === "events" ? "История запросов" : "Действия в панели"}
          </h2>
        </div>
        <Button
          variant="outline"
          disabled={loading}
          onClick={() => {
            setCursor("");
            setPreviousPages([]);
            setRefresh((n) => n + 1);
          }}
        >
          Последние записи
        </Button>
      </div>
      {kind === "events" ? (
        <form
          className="diagnostic-filters event-filters"
          onSubmit={(e) => {
            e.preventDefault();
            if (
              eventInput.from &&
              eventInput.to &&
              new Date(eventInput.from) >= new Date(eventInput.to)
            ) {
              setError("Начало периода должно быть раньше окончания");
              return;
            }
            reset();
            setEventFilters({ ...eventInput });
            setRefresh((n) => n + 1);
          }}
        >
          <label>
            Клиент
            <NativeSelect
              aria-label="Фильтр клиента"
              value={client}
              onChange={(e) => {
                reset();
                setClient(e.target.value);
              }}
            >
              <option value="">Все источники</option>
              {clients.map((c, i) => (
                <option key={`${c.id}-${i}`} value={c.id}>
                  {c.name}
                </option>
              ))}
            </NativeSelect>
          </label>
          <label>
            IP источника
            <Input
              value={eventInput.source_ip}
              placeholder="192.0.2.10"
              maxLength={45}
              onChange={(e) =>
                setEventInput({ ...eventInput, source_ip: e.target.value })
              }
            />
          </label>
          <label>
            Домен / назначение
            <Input
              value={eventInput.destination}
              placeholder="Часть имени"
              maxLength={253}
              onChange={(e) =>
                setEventInput({ ...eventInput, destination: e.target.value })
              }
            />
          </label>
          <label>
            Метод
            <NativeSelect
              aria-label="Метод"
              value={eventInput.method}
              onChange={(e) =>
                setEventInput({ ...eventInput, method: e.target.value })
              }
            >
              <option value="">Все методы</option>
              {[
                "GET",
                "HEAD",
                "CONNECT",
                "POST",
                "PUT",
                "DELETE",
                "OPTIONS",
                "PATCH",
              ].map((v) => (
                <option key={v}>{v}</option>
              ))}
            </NativeSelect>
          </label>
          <label>
            Результат прокси
            <Input
              value={eventInput.result}
              placeholder="TCP_DENIED/403"
              maxLength={64}
              onChange={(e) =>
                setEventInput({ ...eventInput, result: e.target.value })
              }
            />
          </label>
          <label>
            С
            <Input
              type="datetime-local"
              step="any"
              value={eventInput.from}
              onChange={(e) =>
                setEventInput({ ...eventInput, from: e.target.value })
              }
            />
          </label>
          <label>
            До
            <Input
              type="datetime-local"
              step="any"
              value={eventInput.to}
              onChange={(e) =>
                setEventInput({ ...eventInput, to: e.target.value })
              }
            />
          </label>
          <div className="event-filter-actions">
            <label className="checkbox">
              <Input
                type="checkbox"
                checked={failures}
                onChange={(e) => {
                  reset();
                  setFailures(e.target.checked);
                }}
              />
              Только отказы
            </label>
            <Button type="submit" disabled={loading}>
              Применить фильтры
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                reset();
                setClient("");
                setFailures(false);
                setEventInput(emptyEventFilters);
                setEventFilters(emptyEventFilters);
              }}
            >
              Сбросить
            </Button>
          </div>
        </form>
      ) : (
        <form
          className="diagnostic-filters"
          onSubmit={(e) => {
            e.preventDefault();
            reset();
            setFilters({ actor, action });
          }}
        >
          <label>
            Участник
            <Input
              aria-label="Участник аудита"
              value={actor}
              maxLength={256}
              onChange={(e) => setActor(e.target.value)}
              placeholder="Точное имя или IP"
            />
          </label>
          <label>
            Действие
            <NativeSelect
              aria-label="Действие аудита"
              value={action}
              onChange={(e) => setAction(e.target.value)}
            >
              <option value="">Все действия</option>
              {Object.entries(actions).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </NativeSelect>
          </label>
          <Button type="submit" variant="outline">
            Применить фильтры
          </Button>
        </form>
      )}
      <div className="diagnostic-state" aria-live="polite">
        {clientError && <p>{clientError}</p>}
        {error ? (
          <p className="error" role="alert">
            {error}. Показанные записи могут быть устаревшими.
          </p>
        ) : (
          <span>
            {loading ? "Обновляем…" : `Обновлено ${receivedAt || "—"}`}
          </span>
        )}
        {ingestion?.stale && (
          <span className="denied">Сбор событий отстаёт</span>
        )}
        {ingestion && !ingestion.source_available && (
          <span className="denied">Источник журнала недоступен</span>
        )}
        {ingestion?.backlog && <span>Читаем накопленные запросы</span>}
        {data?.gap && <span className="denied">В истории есть пропуски</span>}
      </div>
      <div className="table-wrap">
        <Table>
          <TableHeader>
            <TableRow>
              {(kind === "events"
                ? [
                    "Время",
                    "Клиент / источник",
                    "Назначение",
                    "Метод",
                    "Результат прокси",
                    "Длительность / байты",
                  ]
                : ["Время", "Участник", "Действие", "Объект"]
              ).map((t) => (
                <TableHead key={t}>{t}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {data?.items.map((item) =>
              kind === "events"
                ? (() => {
                    const e = item as Event;
                    return (
                      <TableRow key={e.id}>
                        <TableCell>{date(e.timestamp)}</TableCell>
                        <TableCell>
                          {e.client_name && <strong>{e.client_name}</strong>}
                          <div className="mono">{e.source_ip}</div>
                        </TableCell>
                        <TableCell className="mono destination">
                          {e.destination}
                        </TableCell>
                        <TableCell>{e.method}</TableCell>
                        <TableCell
                          className={
                            e.proxy_result.includes("DENIED") ||
                            (e.proxy_http_status || 0) >= 400
                              ? "denied"
                              : ""
                          }
                        >
                          {e.proxy_result}
                        </TableCell>
                        <TableCell>
                          {e.duration_ms ?? "—"} ms / {e.bytes_observed ?? "—"}{" "}
                          B
                        </TableCell>
                      </TableRow>
                    );
                  })()
                : (() => {
                    const a = item as Audit;
                    return (
                      <TableRow key={a.id}>
                        <TableCell>{date(a.timestamp)}</TableCell>
                        <TableCell>{a.actor || "Не указан"}</TableCell>
                        <TableCell>{actions[a.action] || a.action}</TableCell>
                        <TableCell className="mono destination">
                          {a.entity_id || "—"}
                        </TableCell>
                      </TableRow>
                    );
                  })(),
            )}
          </TableBody>
        </Table>
      </div>
      {!loading && data && data.items.length === 0 && (
        <p className="empty">
          {kind === "events"
            ? "Сохранённых запросов по этим фильтрам пока нет."
            : "Действий по этим фильтрам пока нет."}
        </p>
      )}

      <div className="diagnostic-pagination">
        <span>
          {recordCount(data?.items.length ?? 0)} ·{" "}
          {cursor
            ? "Просмотр истории"
            : paused
              ? "Обновление приостановлено"
              : "Обновление каждые 10 секунд"}
        </span>
        <div className="actions">
          <NativeSelect
            aria-label="Записей на странице"
            value={limit}
            onChange={(e) => {
              reset();
              setLimit(e.target.value);
            }}
          >
            {[25, 50, 100, 200].map((n) => (
              <option key={n} value={n}>
                {n} на странице
              </option>
            ))}
          </NativeSelect>
          <Button variant="ghost" onClick={() => setPaused((v) => !v)}>
            {paused ? "Продолжить" : "Пауза"}
          </Button>
          <Button
            variant="outline"
            disabled={loading || !previousPages.length}
            onClick={() => {
              setCursor(previousPages.at(-1) ?? "");
              setPreviousPages((p) => p.slice(0, -1));
              setData(null);
            }}
          >
            Новее
          </Button>
          <Button
            variant="outline"
            disabled={loading || !data?.next_cursor}
            onClick={() => {
              if (data?.next_cursor) {
                setPreviousPages((p) => [...p, cursor]);
                setCursor(data.next_cursor);
                setData(null);
              }
            }}
          >
            Раньше
          </Button>
        </div>
      </div>
    </section>
  );
}

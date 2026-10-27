import { useEffect, useRef, useState } from "react";
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
function date(value: string) {
  return new Date(value).toLocaleString("ru-RU", { timeZoneName: "short" });
}
const actions: Record<string, string> = {
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
  rate_limited: "Ограничение входа",
  session_capacity_reached: "Достигнут лимит сессий",
};
export function DiagnosticsPanel({
  kind,
  request,
}: {
  kind: "events" | "audit";
  request: Request;
}) {
  const api = useRef(request);
  api.current = request;
  const [data, setData] = useState<Page<Event | Audit> | null>(null);
  const [client, setClient] = useState("");
  const [failures, setFailures] = useState(false);
  const [clients, setClients] = useState<{ id: string; name: string }[]>([]);
  const [clientError, setClientError] = useState("");
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [filters, setFilters] = useState({ actor: "", action: "" });
  const [cursor, setCursor] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [receivedAt, setReceivedAt] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    const query = new URLSearchParams({ limit: "50" });
    if (cursor) query.set("cursor", cursor);
    if (kind === "events") {
      if (client) query.set("client_id", client);
      query.set("failures_only", String(failures));
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
    const timer = cursor
      ? undefined
      : window.setInterval(() => void load(), 10000);
    return () => {
      active = false;
      controller.abort();
      if (timer) clearInterval(timer);
    };
  }, [kind, client, failures, filters, cursor, refresh]);
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
          <p>
            {kind === "events"
              ? "До 7 дней и 50 000 записей. HTTPS виден только как CONNECT-туннель."
              : "До 90 дней и 5 000 записей. Аудит фиксирует действия администратора и события входа."}
          </p>
        </div>
        <button
          className="secondary"
          disabled={loading}
          onClick={() => {
            setCursor("");
            setRefresh((n) => n + 1);
          }}
        >
          Последние записи
        </button>
      </div>
      {kind === "events" ? (
        <div className="diagnostic-filters">
          <label>
            Клиент
            <select
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
            </select>
          </label>
          <label className="checkbox">
            <input
              type="checkbox"
              checked={failures}
              onChange={(e) => {
                reset();
                setFailures(e.target.checked);
              }}
            />
            Только отказы
          </label>
        </div>
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
            <input
              aria-label="Участник аудита"
              value={actor}
              maxLength={256}
              onChange={(e) => setActor(e.target.value)}
              placeholder="Точное имя или IP"
            />
          </label>
          <label>
            Действие
            <select
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
            </select>
          </label>
          <button type="submit" className="secondary">
            Применить фильтры
          </button>
        </form>
      )}
      <div className="diagnostic-state" aria-live="polite">
        {clientError && <p>{clientError}</p>}
        {error ? (
          <p className="error" role="alert">
            Не удалось обновить журнал: {error}. Показанные записи могут быть
            устаревшими.
          </p>
        ) : (
          <p>
            {loading
              ? "Обновляем журнал…"
              : `Получено: ${receivedAt || "ожидаем данные"}`}
          </p>
        )}
        {ingestion && (
          <p
            className={
              ingestion.stale || !ingestion.source_available ? "denied" : ""
            }
          >
            {ingestion.stale ? "Сбор событий устарел. " : ""}
            {!ingestion.source_available
              ? "Файл журнала пока недоступен. "
              : ""}
            {ingestion.backlog ? "Дочитываем накопленные запросы. " : ""}
            Последнее чтение:{" "}
            {ingestion.observed_at
              ? date(ingestion.observed_at)
              : "ещё не выполнялось"}
            .
          </p>
        )}
        {data?.gap && (
          <p className="denied">
            В истории есть пропуски или более ранние записи удалены по сроку
            хранения.
            {ingestion && ingestion.dropped_records > 0
              ? ` Пропущено некорректных записей: ${ingestion.dropped_records}.`
              : ""}
          </p>
        )}
      </div>
      {kind === "events" && (
        <p className="diagnostic-note">
          Имя клиента сопоставлено по IP при сборе. Это не доказательство
          исторического правила ACL. Завершённые CONNECT появляются после
          закрытия туннеля.
        </p>
      )}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
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
                <th key={t}>{t}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data?.items.map((item) =>
              kind === "events"
                ? (() => {
                    const e = item as Event;
                    return (
                      <tr key={e.id}>
                        <td>{date(e.timestamp)}</td>
                        <td>
                          {e.client_name && <strong>{e.client_name}</strong>}
                          <div className="mono">{e.source_ip}</div>
                        </td>
                        <td className="mono destination">{e.destination}</td>
                        <td>{e.method}</td>
                        <td
                          className={
                            e.proxy_result.includes("DENIED") ||
                            (e.proxy_http_status || 0) >= 400
                              ? "denied"
                              : ""
                          }
                        >
                          {e.proxy_result}
                        </td>
                        <td>
                          {e.duration_ms ?? "—"} ms / {e.bytes_observed ?? "—"}{" "}
                          B
                        </td>
                      </tr>
                    );
                  })()
                : (() => {
                    const a = item as Audit;
                    return (
                      <tr key={a.id}>
                        <td>{date(a.timestamp)}</td>
                        <td>{a.actor || "Не указан"}</td>
                        <td>
                          {actions[a.action] || a.action}
                          <div className="mono">{a.action}</div>
                        </td>
                        <td className="mono destination">
                          {a.entity_id || "—"}
                        </td>
                      </tr>
                    );
                  })(),
            )}
          </tbody>
        </table>
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
          {cursor
            ? "Просмотр истории; автоматическое обновление приостановлено."
            : "Последние записи обновляются каждые 10 секунд."}
        </span>
        <button
          className="secondary"
          disabled={loading || !data?.next_cursor}
          onClick={() => {
            if (data?.next_cursor) {
              setCursor(data.next_cursor);
              setData(null);
            }
          }}
        >
          Раньше
        </button>
      </div>
    </section>
  );
}

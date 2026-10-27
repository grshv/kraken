import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

type Destination = { domain: string; include_subdomains: boolean };
type Client = {
  id: string;
  name: string;
  source_networks: string[];
  enabled: boolean;
  policy_id: string;
};
type Policy = {
  id: string;
  name: string;
  mode: "all_web" | "allowlist";
  destinations: Destination[];
  tls_profile_id: string;
};
type Snapshot = {
  schema_version: number;
  clients: Client[];
  policies: Policy[];
  tls_profiles: {
    id: string;
    name: string;
    mode: string;
    proxy_transport: string;
    cert_ref: string | null;
    ca_ref: string | null;
    bypass_domains: Destination[];
  }[];
};
type Draft = { version: number; snapshot: Snapshot; updated_at: string };
type Session = {
  authenticated: boolean;
  username: string | null;
  csrf_token: string;
};
type Component = {
  state: string;
  detail: string;
  observed_at: string;
  stale: boolean;
};
type Status = {
  web: Component;
  runtime: Component;
  squid: Component;
  applied_revision: string | null;
  capabilities: string[];
};
type Preview = {
  revision_id: string;
  policy_hash: string;
  generated_config: string;
  warnings: string[];
  expected_applied_revision: string | null;
};
type Operation = {
  id: string;
  action: string;
  revision_id: string;
  status: string;
  findings: string[];
};
type Event = {
  timestamp: string;
  source_ip: string;
  proxy_result: string;
  method: string;
  destination: string;
};
const labels: Record<string, string> = {
  up: "Работает",
  down: "Недоступен",
  unknown: "Нет данных",
  degraded: "Нужна проверка",
  pending: "Принято",
  validating: "Проверка",
  validated: "Проверено",
  reloading: "Применение",
  succeeded: "Применено",
  rejected: "Отклонено",
  rolled_back: "Восстановлена предыдущая версия",
};
let csrf = "";
async function api<T>(
  path: string,
  method = "GET",
  data?: unknown,
  key?: string,
): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-CSRF-Token"] = csrf;
  }
  if (key) headers["Idempotency-Key"] = key;
  const r = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  if (r.status === 204) return undefined as T;
  const result = await r.json();
  if (!r.ok) throw new Error(result.message || `Ошибка ${r.status}`);
  return result;
}
function Pill({ value }: { value: string }) {
  return (
    <span
      className={`pill ${value === "up" || value === "succeeded" || value === "validated" ? "good" : value === "down" || value === "rejected" || value === "degraded" ? "bad" : "neutral"}`}
    >
      <i />
      {labels[value] || value}
    </span>
  );
}
function App() {
  const [session, setSession] = useState<Session | null>(null),
    [fatal, setFatal] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null),
    [status, setStatus] = useState<Status | null>(null),
    [page, setPage] = useState("Обзор"),
    [events, setEvents] = useState<Event[]>([]),
    [failOnly, setFailOnly] = useState(false);
  const [preview, setPreview] = useState<Preview | null>(null),
    [operation, setOperation] = useState<Operation | null>(null),
    [validated, setValidated] = useState("");
  const [editing, setEditing] = useState<string | null>(null),
    [showForm, setShowForm] = useState(false);
  const [name, setName] = useState(""),
    [networks, setNetworks] = useState(""),
    [mode, setMode] = useState<"all_web" | "allowlist">("allowlist"),
    [domains, setDomains] = useState(""),
    [transport, setTransport] = useState("http");
  async function load() {
    const [d, s] = await Promise.all([
      api<Draft>("/configuration/draft"),
      api<Status>("/status"),
    ]);
    setDraft(d);
    setStatus(s);
  }
  useEffect(() => {
    api<Session>("/session")
      .then((s) => {
        csrf = s.csrf_token;
        setSession(s);
        if (s.authenticated) load().catch((e) => setError(e.message));
      })
      .catch((e) => setFatal(e.message));
  }, []);
  useEffect(() => {
    if (!session?.authenticated) return;
    let active = true;
    const refresh = () =>
      api<{ items: Event[] }>(`/events?failures_only=${failOnly}`)
        .then((r) => {
          if (active) setEvents(r.items);
        })
        .catch((e) => {
          if (active) setError(e.message);
        });
    refresh();
    const timer = setInterval(() => {
      refresh();
      api<Status>("/status")
        .then((s) => {
          if (active) setStatus(s);
        })
        .catch(() => {});
    }, 10000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [session?.authenticated, failOnly]);
  async function work(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function login(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    await work(async () => {
      const s = await api<Session>("/session", "POST", {
        username: form.get("username"),
        password: form.get("password"),
      });
      csrf = s.csrf_token;
      setSession(s);
      await load();
    });
  }
  async function logout() {
    await work(async () => {
      await api("/session", "DELETE");
      const s = await api<Session>("/session");
      csrf = s.csrf_token;
      setSession(s);
      setDraft(null);
      setPreview(null);
      setOperation(null);
      setValidated("");
    });
  }
  async function save(snapshot: Snapshot) {
    if (!draft) return;
    const d = await api<Draft>("/configuration/draft", "PUT", {
      expected_version: draft.version,
      snapshot,
    });
    setDraft(d);
    setPreview(null);
    setValidated("");
    setOperation(null);
  }
  function openClient(c?: Client) {
    if (!draft) return;
    const p = draft.snapshot.policies.find((p) => p.id === c?.policy_id);
    const t = draft.snapshot.tls_profiles.find(
      (t) => t.id === p?.tls_profile_id,
    );
    setEditing(c?.id || null);
    setName(c?.name || "");
    setNetworks(c?.source_networks.join("\n") || "");
    setMode(p?.mode || "allowlist");
    setDomains(
      p?.destinations
        .map((d) => (d.include_subdomains ? "." : "") + d.domain)
        .join("\n") || "",
    );
    setTransport(t?.proxy_transport || "http");
    setShowForm(true);
  }
  async function submitClient(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!draft) return;
    await work(async () => {
      const snap = structuredClone(draft.snapshot);
      const client = snap.clients.find((c) => c.id === editing);
      const id = editing || crypto.randomUUID();
      const policyID = client?.policy_id || crypto.randomUUID();
      let profile = snap.tls_profiles.find(
        (t) => t.proxy_transport === transport && t.mode === "tunnel",
      );
      if (!profile) {
        profile = {
          id: crypto.randomUUID(),
          name: "TLS proxy / CONNECT",
          mode: "tunnel",
          proxy_transport: transport,
          cert_ref: "proxy-server",
          ca_ref: null,
          bypass_domains: [],
        };
        snap.tls_profiles.push(profile);
      }
      const destinations =
        mode === "all_web"
          ? []
          : domains
              .split(/\n/)
              .map((s) => s.trim())
              .filter(Boolean)
              .map((s) => ({
                domain: s.replace(/^\./, "").toLowerCase(),
                include_subdomains: s.startsWith("."),
              }));
      const p: Policy = {
        id: policyID,
        name: name.trim(),
        mode,
        destinations,
        tls_profile_id: profile.id,
      };
      snap.policies = snap.policies.filter((p) => p.id !== policyID).concat(p);
      const c: Client = {
        id,
        name: name.trim(),
        source_networks: networks
          .split(/[\n,]/)
          .map((s) => s.trim())
          .filter(Boolean),
        enabled: client?.enabled ?? true,
        policy_id: policyID,
      };
      snap.clients = snap.clients.filter((c) => c.id !== id).concat(c);
      await save(snap);
      setShowForm(false);
      setPage("Клиенты");
    });
  }
  async function freeze() {
    if (!draft) return;
    await work(async () => {
      const r = await api<{ id: string }>("/revisions", "POST", {
        expected_draft_version: draft.version,
      });
      const p = await api<Preview>(`/revisions/${r.id}/preview`);
      setPreview(p);
      setOperation(null);
      setValidated("");
      setPage("Изменения");
    });
  }
  async function run(action: "validate" | "apply") {
    if (!preview) return;
    await work(async () => {
      const input = {
        action,
        revision_id: preview.revision_id,
        expected_applied_revision: preview.expected_applied_revision,
        ...(action === "apply" ? { validation_operation_id: validated } : {}),
      };
      let op = await api<Operation>(
        "/operations",
        "POST",
        input,
        crypto.randomUUID(),
      );
      setOperation(op);
      for (
        let i = 0;
        i < 100 && ["pending", "validating", "reloading"].includes(op.status);
        i++
      ) {
        await new Promise((r) => setTimeout(r, 500));
        op = await api<Operation>(`/operations/${op.id}`);
        setOperation(op);
      }
      if (op.status === "validated") setValidated(op.id);
      if (op.status === "succeeded") {
        await load();
        setPreview(null);
        setValidated("");
      }
      if (["rejected", "degraded", "rolled_back"].includes(op.status))
        setError(op.findings.join(" "));
    });
  }
  if (fatal)
    return (
      <main className="login-wrap">
        <section className="login">
          <h1>Панель недоступна</h1>
          <p role="alert">{fatal}</p>
          <button onClick={() => location.reload()}>Повторить</button>
        </section>
      </main>
    );
  if (!session)
    return (
      <main className="login-wrap">
        <p>Подключение к панели…</p>
      </main>
    );
  if (!session.authenticated)
    return (
      <main className="login-wrap">
        <section className="login">
          <div className="brand-mark">S</div>
          <h1>Squid Control</h1>
          <p>Доступ к инфраструктуре начинается здесь.</p>
          <form onSubmit={login}>
            <label>
              Имя пользователя
              <input
                name="username"
                autoComplete="username"
                required
                autoFocus
              />
            </label>
            <label>
              Пароль
              <input
                name="password"
                type="password"
                autoComplete="current-password"
                required
              />
            </label>
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            <button disabled={busy}>{busy ? "Вход…" : "Войти в панель"}</button>
          </form>
          <small>
            Нет учётной записи? Создайте администратора локальной командой
            bootstrap.
          </small>
        </section>
      </main>
    );
  const snap = draft?.snapshot;
  const stateLabel = status?.squid.state || "unknown";
  return (
    <div className="shell">
      <aside>
        <a className="brand" href="/" aria-label="Squid Control">
          <span className="brand-mark">S</span>
          <span>
            Squid
            <br />
            <b>Control</b>
          </span>
        </a>
        <div className="aside-label">Управление доступом</div>
        <nav>
          {["Обзор", "Клиенты", "Изменения", "События"].map((p, i) => (
            <button
              key={p}
              className={page === p ? "selected" : ""}
              onClick={() => setPage(p)}
            >
              <span aria-hidden="true">{["◉", "▤", "⇄", "≡"][i]}</span>
              {p}
            </button>
          ))}
        </nav>
        <div className="aside-bottom">
          <span>Локальная инсталляция</span>
          <span>HTTP / HTTPS proxy</span>
          <span className="build-label">Первый рабочий срез</span>
        </div>
      </aside>
      <div className="workspace">
        <header>
          <span className="breadcrumb">
            Инфраструктура <span>/</span> {page}
          </span>
          <div className="user">
            <span>{session.username}</span>
            <button className="text-button" onClick={logout} disabled={busy}>
              Выйти
            </button>
          </div>
        </header>
        <main>
          <div className="page-title">
            <div>
              <p className="context">Сетевой доступ</p>
              <h1>{page === "Обзор" ? "Прокси под контролем" : page}</h1>
              <p>
                {page === "Обзор"
                  ? "Состояние соединения и правила ваших машин."
                  : page === "Клиенты"
                    ? "Каждой машине — свой доступ к ресурсам."
                    : page === "Изменения"
                      ? "Проверьте новую конфигурацию перед применением."
                      : "Наблюдаемые запросы и отказы Squid."}
              </p>
            </div>
            <button
              className="secondary"
              onClick={() => work(load)}
              disabled={busy}
            >
              Обновить состояние
            </button>
          </div>
          {error && (
            <div className="error" role="alert">
              {error}
              <button className="text-button" onClick={() => setError("")}>
                Закрыть
              </button>
            </div>
          )}
          {(page === "Обзор" || page === "Клиенты") && (
            <>
              {page === "Обзор" && (
                <section
                  className="connection"
                  aria-label="Состояние соединения"
                >
                  <div className="connection-node">
                    <span className="node-icon">▤</span>
                    <div>
                      <strong>Клиентские VM</strong>
                      <small>Индивидуальные правила</small>
                    </div>
                  </div>
                  <span className="connection-line" />
                  <div className="connection-node">
                    <span
                      className={`node-icon ${stateLabel === "up" ? "live" : ""}`}
                    >
                      S
                    </span>
                    <div>
                      <strong>Squid</strong>
                      <Pill value={stateLabel} />
                    </div>
                  </div>
                  <span className="connection-line" />
                  <div className="connection-node">
                    <span className="node-icon">◎</span>
                    <div>
                      <strong>Интернет</strong>
                      <small>Доступность не измерена</small>
                    </div>
                  </div>
                  <div className="connection-note">
                    {status?.squid.observed_at
                      ? "Проверено " +
                        new Date(status.squid.observed_at).toLocaleTimeString(
                          "ru-RU",
                        )
                      : "Ожидание проверки"}
                  </div>
                </section>
              )}
              <section className="section">
                <div className="section-head">
                  <div>
                    <h2>Клиенты</h2>
                    <p>
                      Сохранение меняет черновик. Для изменения доступа
                      примените конфигурацию.
                    </p>
                  </div>
                  <button
                    onClick={() => openClient()}
                    disabled={busy || !draft}
                  >
                    Добавить VM
                  </button>
                </div>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Машина</th>
                        <th>Адрес / сеть</th>
                        <th>Доступ</th>
                        <th>Состояние</th>
                        <th>
                          <span className="sr-only">Действия</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {snap?.clients.map((c) => {
                        const p = snap.policies.find(
                          (p) => p.id === c.policy_id,
                        );
                        return (
                          <tr key={c.id}>
                            <td>
                              <strong>{c.name}</strong>
                            </td>
                            <td className="mono">
                              {c.source_networks.join(", ")}
                            </td>
                            <td>
                              {p?.mode === "all_web"
                                ? "Весь web"
                                : `По списку · ${p?.destinations.length || 0} доменов`}
                            </td>
                            <td>
                              <span
                                className={`client-state ${c.enabled ? "" : "disabled"}`}
                              >
                                {c.enabled
                                  ? "Включена в черновике"
                                  : "Отключена в черновике"}
                              </span>
                            </td>
                            <td className="row-actions">
                              <button
                                className="text-button"
                                onClick={() => openClient(c)}
                              >
                                Изменить
                              </button>
                              <button
                                className="text-button"
                                disabled={busy}
                                onClick={() =>
                                  work(async () => {
                                    const next = structuredClone(snap);
                                    next.clients = next.clients.map((v) =>
                                      v.id === c.id
                                        ? { ...v, enabled: !v.enabled }
                                        : v,
                                    );
                                    await save(next);
                                  })
                                }
                              >
                                {c.enabled ? "Отключить" : "Включить"}
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                  {!snap?.clients.length && (
                    <div className="empty">
                      <span>▤</span>
                      <h3>Добавьте первую машину</h3>
                      <p>
                        Укажите IP и выберите: весь web или разрешённые домены.
                        <br />
                        Неизвестные источники остаются без доступа.
                      </p>
                      <button
                        className="secondary"
                        onClick={() => openClient()}
                      >
                        Добавить VM
                      </button>
                    </div>
                  )}
                </div>
                <div className="section-footer">
                  <span>
                    Черновик v{draft?.version || "…"} ·{" "}
                    {status?.applied_revision
                      ? "Есть применённая ревизия"
                      : "Действует исходный запрет доступа"}
                  </span>
                  <button
                    className="secondary"
                    disabled={busy || !draft}
                    onClick={freeze}
                  >
                    Посмотреть изменения
                  </button>
                </div>
              </section>
              {page === "Обзор" && (
                <div className="details-row">
                  <section>
                    <h3>HTTPS через CONNECT</h3>
                    <p>
                      Squid видит домен и порт. Содержимое туннеля не
                      расшифровывается.
                    </p>
                  </section>
                  <section>
                    <h3>Защитные ограничения</h3>
                    <p>
                      Внутренние адреса, metadata и опасные порты закрыты для
                      всех режимов.
                    </p>
                  </section>
                </div>
              )}
            </>
          )}
          {page === "Изменения" && (
            <section className="section">
              <div className="section-head">
                <div>
                  <h2>Конфигурация доступа</h2>
                  <p>
                    Проверка выполняется локальным Squid из этого же образа.
                  </p>
                </div>
                <button
                  className="secondary"
                  onClick={freeze}
                  disabled={busy || !draft}
                >
                  Подготовить из черновика
                </button>
              </div>
              {preview ? (
                <>
                  <div className="revision-info">
                    <span>
                      Ревизия <code>{preview.revision_id.slice(0, 8)}</code>
                    </span>
                    <span>
                      SHA-256 <code>{preview.policy_hash.slice(0, 16)}…</code>
                    </span>
                  </div>
                  {preview.warnings.map((w) => (
                    <p className="notice" key={w}>
                      {w}
                    </p>
                  ))}
                  <details>
                    <summary>Сгенерированная конфигурация Squid</summary>
                    <pre>{preview.generated_config}</pre>
                  </details>
                  <div className="section-footer">
                    <span>
                      {validated
                        ? "Эта ревизия прошла проверку"
                        : "Требуется проверка конфигурации"}
                    </span>
                    <div className="actions">
                      <button
                        className="secondary"
                        disabled={busy}
                        onClick={() => run("validate")}
                      >
                        Проверить
                      </button>
                      <button
                        disabled={busy || !validated}
                        onClick={() => run("apply")}
                      >
                        Применить
                      </button>
                    </div>
                  </div>
                </>
              ) : (
                <div className="empty">
                  <h3>Подготовьте конфигурацию</h3>
                  <p>
                    Черновик будет сохранён отдельной ревизией для просмотра и
                    проверки.
                  </p>
                </div>
              )}
              {operation && (
                <div className="operation">
                  <Pill value={operation.status} />
                  <span>
                    Операция <code>{operation.id.slice(0, 8)}</code>
                  </span>
                  {operation.findings.map((f) => (
                    <p key={f}>{f}</p>
                  ))}
                </div>
              )}
            </section>
          )}
          {(page === "События" || page === "Обзор") && (
            <section className="section events">
              <div className="section-head">
                <div>
                  <h2>
                    {page === "Обзор" ? "Последние события" : "Журнал запросов"}
                  </h2>
                  <p>
                    Ограниченный хвост журнала. Возможны пропуски; история не
                    является полной.
                  </p>
                </div>
                <label className="checkbox">
                  <input
                    type="checkbox"
                    checked={failOnly}
                    onChange={(e) => setFailOnly(e.target.checked)}
                  />
                  Только отказы
                </label>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Время</th>
                      <th>Источник</th>
                      <th>Назначение</th>
                      <th>Метод</th>
                      <th>Результат прокси</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.slice(0, page === "Обзор" ? 6 : 100).map((e, i) => (
                      <tr key={i}>
                        <td>
                          {new Date(e.timestamp).toLocaleTimeString("ru-RU")}
                        </td>
                        <td className="mono">{e.source_ip}</td>
                        <td className="mono destination">{e.destination}</td>
                        <td>{e.method}</td>
                        <td>
                          <span
                            className={
                              e.proxy_result.includes("DENIED") ? "denied" : ""
                            }
                          >
                            {e.proxy_result}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {!events.length && (
                  <div className="empty small">
                    <h3>Событий пока нет</h3>
                    <p>
                      Здесь появятся запросы к прокси. Журнал обновляется каждые
                      10 секунд.
                    </p>
                  </div>
                )}
              </div>
            </section>
          )}
          <footer>
            Squid Control{" "}
            <span>
              Парольная защита · TLS панели · Доступ закрыт по умолчанию
            </span>
          </footer>
        </main>
      </div>
      {showForm && (
        <div
          className="modal-backdrop"
          onClick={(e) => {
            if (e.target === e.currentTarget && !busy) setShowForm(false);
          }}
        >
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="client-title"
            className="modal"
          >
            <div className="section-head">
              <h2 id="client-title">
                {editing ? "Настройки машины" : "Добавить VM"}
              </h2>
              <button
                className="text-button"
                onClick={() => setShowForm(false)}
                aria-label="Закрыть"
              >
                ✕
              </button>
            </div>
            <form onSubmit={submitClient}>
              <label>
                Название
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  maxLength={128}
                  autoFocus
                  placeholder="Например, build-server"
                />
              </label>
              <label>
                IP / CIDR
                <textarea
                  value={networks}
                  onChange={(e) => setNetworks(e.target.value)}
                  required
                  rows={2}
                  placeholder="192.0.2.11/32"
                />
                <small>
                  Адрес, который видит Squid. Каждая сеть с новой строки.
                </small>
              </label>
              <label>
                Режим доступа
                <select
                  value={mode}
                  onChange={(e) => setMode(e.target.value as typeof mode)}
                >
                  <option value="allowlist">Только разрешённые ресурсы</option>
                  <option value="all_web">Весь внешний HTTP/HTTPS</option>
                </select>
              </label>
              {mode === "allowlist" && (
                <label>
                  Разрешённые домены
                  <textarea
                    value={domains}
                    onChange={(e) => setDomains(e.target.value)}
                    rows={4}
                    placeholder={"packages.example.org\n.archive.example.org"}
                  />
                  <small>
                    Домен с новой строки. Точка в начале включает поддомены.
                    Пустой список закрывает доступ.
                  </small>
                </label>
              )}
              <label>
                Подключение к прокси
                <select
                  value={transport}
                  onChange={(e) => setTransport(e.target.value)}
                >
                  <option value="http">HTTP proxy / HTTPS через CONNECT</option>
                  <option
                    value="tls"
                    disabled={
                      !status?.capabilities.includes("tls_proxy_configured")
                    }
                  >
                    TLS к прокси{" "}
                    {status?.capabilities.includes("tls_proxy_configured")
                      ? ""
                      : "— сертификат не настроен"}
                  </option>
                </select>
              </label>
              <p className="notice">
                SSL Bump недоступен в текущей GnuTLS-сборке. HTTPS сайтов
                остаётся сквозным.
              </p>
              {error && (
                <div className="error" role="alert">
                  {error}
                </div>
              )}
              <div className="modal-actions">
                <button
                  type="button"
                  className="secondary"
                  onClick={() => setShowForm(false)}
                  disabled={busy}
                >
                  Отмена
                </button>
                <button disabled={busy}>
                  {busy ? "Сохранение…" : "Сохранить черновик"}
                </button>
              </div>
            </form>
          </section>
        </div>
      )}
    </div>
  );
}
createRoot(document.getElementById("root")!).render(<App />);

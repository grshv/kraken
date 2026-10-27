import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

import type {
  Client,
  Policy,
  Snapshot,
  Draft,
  Session,
  Status,
  Preview,
  Operation,
  Event,
  RevisionSummary,
} from "./model";
import { SettingsPanel } from "./SettingsPanel";

// Labels cover every status value the server or a historical revision summary
// could surface. OpenAPI declares more stages than are emitted today; if the
// server ever adds one without a label here, Pill falls back to the raw token
// and a test should catch it. "draft" is the COALESCE default listRevisions
// uses for revisions that have no operation yet.
const labels: Record<string, string> = {
  up: "Работает",
  down: "Недоступен",
  unknown: "Нет данных",
  degraded: "Нужна проверка",
  pending: "Принято",
  accepted: "Принято",
  staged: "Подготовлено",
  draft: "Черновик",
  validating: "Проверка",
  validated: "Проверено",
  promoting: "Применение",
  reloading: "Применение",
  verifying: "Проверка применения",
  succeeded: "Применено",
  rejected: "Отклонено",
  rolling_back: "Откат",
  rolled_back: "Восстановлена предыдущая версия",
};

class SessionExpired extends Error {
  constructor() {
    super("Сессия истекла. Войдите снова");
    this.name = "SessionExpired";
  }
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
  // csrf lives as component state now instead of a module-level mutable let.
  // The previous `let csrf = ""` worked for a single-root SPA but leaked
  // across re-renders of a multi-tab fixture and made session rotation during
  // tests invisible to React. setCsrf on login/logout/session refresh drives
  // every re-render through the same path used to read the value into api().
  const [csrf, setCsrf] = useState("");
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
  const [history, setHistory] = useState<RevisionSummary[]>([]);
  const [rollbackTarget, setRollbackTarget] = useState(false);
  const [kind, setKind] = useState<"host" | "network" | "group">("host");
  const [editing, setEditing] = useState<string | null>(null),
    [showForm, setShowForm] = useState(false);
  const [name, setName] = useState(""),
    [description, setDescription] = useState(""),
    [networks, setNetworks] = useState(""),
    [mode, setMode] = useState<"all_web" | "allowlist">("allowlist"),
    [domains, setDomains] = useState(""),
    [transport, setTransport] = useState("http");
  const modalRef = useRef<HTMLElement | null>(null);
  // api reads csrf/index from this closure, so every re-render produces a new
  // function bound to the current token. Benefits: 401 clearing naturally
  // rerenders consumers (no hidden state); tests that re-render App with a
  // different csrf see the new value immediately.
  async function api<T>(
    path: string,
    method = "GET",
    data?: unknown,
    key?: string,
    signal?: AbortSignal,
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
      signal,
    });
    if (r.status === 204) return undefined as T;
    // Treat invalid/expired sessions explicitly: a 401 from an authenticated
    // call clears the cached CSRF token and bubbles a typed error so the caller
    // can return the user to the login screen instead of looping on retries.
    if (r.status === 401 && method !== "GET" && path !== "/session") {
      setCsrf("");
      throw new SessionExpired();
    }
    // Reverse-proxies, TLS MITM pages and TCP resets return non-JSON bodies;
    // parsing them as JSON throws a misleading SyntaxError that hides the real
    // status. Inspect content-type first. Also avoids surfacing the raw fetch
    // AbortError when a poll is cancelled mid-flight (signal.aborted).
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const ct = r.headers.get("content-type") || "";
    if (!ct.toLowerCase().startsWith("application/json")) {
      if (!r.ok) throw new Error(`Сервер недоступен (${r.status})`);
      throw new Error(`Неожиданный ответ сервера (${r.status})`);
    }
    const result = await r.json();
    if (!r.ok) throw new Error(result.message || `Ошибка ${r.status}`);
    return result;
  }
  // Escape closes the client modal unless a save is in flight. Focus is trapped
  // to the dialog while it is open: Tab cycles among focusable children instead
  // of leaking back to the underlying table which becomes inert under the
  // aria-modal backdrop.
  useEffect(() => {
    if (!showForm) return;
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape" || busy) return;
      e.preventDefault();
      setShowForm(false);
    }
    function onTab(e: KeyboardEvent) {
      if (e.key !== "Tab" || !modalRef.current) return;
      const focusable = modalRef.current.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
    document.addEventListener("keydown", onKey);
    document.addEventListener("keydown", onTab);
    // Move focus to the first input when the dialog mounts. autofocus on the
    // name field already exists but is unreliable across browsers when the
    // modal is rendered into an existing tree.
    const t = window.setTimeout(() => {
      const first = modalRef.current?.querySelector<HTMLInputElement>("input,textarea,select");
      first?.focus();
    }, 0);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("keydown", onTab);
      window.clearTimeout(t);
    };
  }, [showForm, busy]);
  async function load() {
    const [d, s, h] = await Promise.all([
      api<Draft>("/configuration/draft"),
      api<Status>("/status"),
      api<{ items: RevisionSummary[] }>("/revisions"),
    ]);
    setDraft(d);
    setStatus(s);
    setHistory(h.items);
  }
  useEffect(() => {
    api<Session>("/session")
      .then((s) => {
        setCsrf(s.csrf_token);
        setSession(s);
        if (s.authenticated) load().catch((e) => setError(e.message));
      })
      .catch((e) => setFatal(e.message));
  }, []);
  useEffect(() => {
    if (!session?.authenticated) return;
    // AbortController lets the cleanup function cancel in-flight fetches when
    // the tab switches or the session expires mid-poll. The previous code
    // relied on an `active` flag to guard setState, but the underlying fetch
    // still ran to completion and could surface as a late 401/timeout after
    // unmount. Sharing one controller between /events and /status polls keeps
    // cancellation logically atomic across both endpoints.
    const controller = new AbortController();
    let active = true;
    const refresh = () =>
      api<{ items: Event[] }>(
        `/events?failures_only=${failOnly}`,
        "GET",
        undefined,
        undefined,
        controller.signal,
      )
        .then((r) => {
          if (active) setEvents(r.items);
        })
        .catch((e) => {
          if (!active) return;
          if (e instanceof SessionExpired) {
            // Session expired mid-page: return the user to login instead of
            // cascading 401s across subsequent polling calls.
            setSession({ authenticated: false, username: null, csrf_token: "", expires_at: null });
            setError(e.message);
          } else if (!(e instanceof DOMException && e.name === "AbortError")) {
            setError(e.message);
          }
        });
    refresh();
    const timer = setInterval(() => {
      refresh();
      api<Status>("/status", "GET", undefined, undefined, controller.signal)
        .then((s) => {
          if (active) setStatus(s);
        })
        .catch((e) => {
          if (active && e instanceof SessionExpired) {
            setSession({ authenticated: false, username: null, csrf_token: "", expires_at: null });
            setError(e.message);
          }
        });
    }, 10000);
    return () => {
      active = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [session?.authenticated, failOnly]);
  async function work(fn: (signal?: AbortSignal) => Promise<void>) {
    setBusy(true);
    setError("");
    // Each work() invocation gets its own controller. Skills like submitClient
    // or run() pass the signal into their fetches so aborting this controller
    // — explicit (component unmount via the useEffect below) or implicit
    // (work finishes and the controller is GC'd) — cancels in-flight polls
    // instead of leaving fetches to settle against an unmounted component.
    const controller = new AbortController();
    workAbortRef.current = controller;
    try {
      await fn(controller.signal);
    } catch (e) {
      if (e instanceof SessionExpired) {
        // Drop the panel state back to the login view; preserve any uncommitted
        // draft snapshot in localStorage-free memory by leaving `draft` in place.
        setError(e.message);
        setSession({ authenticated: false, username: null, csrf_token: "", expires_at: null });
      } else if (e instanceof DOMException && e.name === "AbortError") {
        // Abort from unmount or a new work() call: don't surface as an error,
        // just stop the in-flight sequence.
      } else {
        setError((e as Error).message);
      }
    } finally {
      if (workAbortRef.current === controller) {
        workAbortRef.current = null;
      }
      setBusy(false);
    }
  }
  const workAbortRef = useRef<AbortController | null>(null);
  // On unmount, abort any in-flight work fetches. Lets a user navigate away
  // mid-poll without leaving orphan requests that surface as setState warnings
  // against an unmounted component.
  useEffect(() => {
    return () => workAbortRef.current?.abort();
  }, []);
  async function login(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    await work(async () => {
      const s = await api<Session>("/session", "POST", {
        username: form.get("username"),
        password: form.get("password"),
      });
      setCsrf(s.csrf_token);
      setSession(s);
      await load();
    });
  }
  async function logout() {
    await work(async () => {
      await api("/session", "DELETE");
      const s = await api<Session>("/session");
      setCsrf(s.csrf_token);
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
    setStatus(await api<Status>("/status"));
  }
  function openClient(c?: Client) {
    if (!draft) return;
    const p = draft.snapshot.policies.find((p) => p.id === c?.policy_id);
    const t = draft.snapshot.tls_profiles.find(
      (t) => t.id === p?.tls_profile_id,
    );
    setEditing(c?.id || null);
    setKind(c?.kind || (c && c.source_networks.length > 1 ? "group" : "host"));
    setName(c?.name || "");
    setDescription(c?.description || "");
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
        kind,
        name: name.trim(),
        description: description.trim() || undefined,
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
      setRollbackTarget(false);
      setOperation(null);
      setValidated("");
      setPage("Изменения");
    });
  }
  async function run(action: "validate" | "apply" | "rollback") {
    if (!preview) return;
    await work(async (signal) => {
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
        signal,
      );
      setOperation(op);
      // The server worker keeps an Apply operation alive for up to 90 s, plus up
      // to 45 s of rollback (see manager.go: rollbackCtx budget). Polling for
      // only 50 s would hang the UI in "Применение/Проверка" if reconfigure is
      // slow or the rollback engages. Continue until a terminal status is seen
      // (or the request errors out / is aborted by unmount), with periodic backoff.
      for (
        let i = 0;
        i < 360 && ["pending", "validating", "reloading"].includes(op.status);
        i++
      ) {
        await new Promise((r) => setTimeout(r, 500));
        op = await api<Operation>(`/operations/${op.id}`, "GET", undefined, undefined, signal);
        setOperation(op);
        if (
          i === 200 &&
          ["validating", "reloading"].includes(op.status)
        ) {
          setError(
            "Операция выполняется дольше обычного. Панель продолжает опрос статуса.",
          );
        }
      }
      setHistory((await api<{ items: RevisionSummary[] }>("/revisions", "GET", undefined, undefined, signal)).items);
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
          {["Обзор", "Клиенты", "Настройки", "Изменения", "События"].map(
            (p, i) => (
              <button
                key={p}
                className={page === p ? "selected" : ""}
                onClick={() => setPage(p)}
              >
                <span aria-hidden="true">{["◉", "▤", "⚙", "⇄", "≡"][i]}</span>
                {p}
              </button>
            ),
          )}
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
                  ? "Состояние прокси, настройки и политики клиентов."
                  : page === "Клиенты"
                    ? "Адреса, сети и группы с индивидуальными правилами доступа."
                    : page === "Настройки"
                      ? "Параметры Squid и экспертная конфигурация."
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
          {status?.runtime.state === "degraded" && (
            <p role="alert" className="error">
              Управление требует восстановления: исход предыдущей операции не
              подтверждён. Новые применения заблокированы.
            </p>
          )}
          {status?.has_unapplied_changes && (
            <div className="pending-banner">
              <span>Есть неприменённые изменения</span>
              <button className="text-button" disabled={busy} onClick={freeze}>
                Посмотреть diff
              </button>
            </div>
          )}
          {page === "Настройки" && draft && (
            <SettingsPanel
              snapshot={draft.snapshot}
              busy={busy}
              onSave={(next) => work(() => save(next))}
              onTemplate={async () =>
                (
                  await api<{ expert_config: string }>(
                    "/configuration/expert-template",
                  )
                ).expert_config
              }
            />
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
                      <strong>Клиенты прокси</strong>
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
                {snap?.configuration_mode === "expert" && (
                  <p className="notice">
                    В черновике выбран экспертный режим: правила клиентов ниже
                    сохраняются для визуального режима и не входят в создаваемую
                    конфигурацию. Для изменения трафика примените ревизию.
                  </p>
                )}
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
                    disabled={
                      busy || !draft || snap?.configuration_mode === "expert"
                    }
                  >
                    Добавить клиента
                  </button>
                </div>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Клиент / группа</th>
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
                              {c.description && (
                                <small className="row-description">
                                  {c.description}
                                </small>
                              )}
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
                                  ? "Включён в черновике"
                                  : "Отключён в черновике"}
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
                      <h3>Добавьте первого клиента</h3>
                      <p>
                        Укажите IP и выберите: весь web или разрешённые домены.
                        <br />
                        Неизвестные источники остаются без доступа.
                      </p>
                      <button
                        className="secondary"
                        onClick={() => openClient()}
                      >
                        Добавить клиента
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
                      Loopback и metadata закрыты. Доступ к частным сетям и
                      портам задаётся в настройках применяемой ревизии.
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
                  <ul className="change-summary">
                    {preview.summary.map((item, i) => (
                      <li key={i}>{item}</li>
                    ))}
                  </ul>
                  <details open>
                    <summary>Изменения конфигурации</summary>
                    <pre className="config-diff">
                      {preview.config_diff.map((line, i) => (
                        <span key={i} className={line.kind}>
                          {line.kind === "added"
                            ? "+ "
                            : line.kind === "removed"
                              ? "- "
                              : "  "}
                          {line.text}
                          {"\n"}
                        </span>
                      ))}
                    </pre>
                  </details>
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
                      {rollbackTarget
                        ? "Откат повторно проверит конфигурацию перед применением"
                        : validated
                          ? "Эта ревизия прошла проверку"
                          : "Требуется проверка конфигурации"}
                    </span>
                    <div className="actions">
                      <button
                        className="secondary"
                        disabled={
                          busy ||
                          rollbackTarget ||
                          status?.runtime.state === "degraded"
                        }
                        onClick={() => run("validate")}
                      >
                        Проверить
                      </button>
                      <button
                        disabled={
                          busy ||
                          (!rollbackTarget && !validated) ||
                          status?.runtime.state === "degraded"
                        }
                        onClick={() =>
                          run(rollbackTarget ? "rollback" : "apply")
                        }
                      >
                        {rollbackTarget
                          ? "Откатить к этой ревизии"
                          : "Применить"}
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
                  <ul className="findings">
                    {operation.findings.map((f) => (
                      <li key={f}>{f}</li>
                    ))}
                  </ul>
                </div>
              )}
            </section>
          )}
          {page === "Изменения" && (
            <section className="section">
              <div className="section-head">
                <div>
                  <h2>История ревизий</h2>
                  <p>Откат проверяется и применяется отдельной операцией.</p>
                </div>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Создана</th>
                      <th>Ревизия</th>
                      <th>Статус</th>
                      <th>Действие</th>
                    </tr>
                  </thead>
                  <tbody>
                    {history.map((rev) => (
                      <tr key={rev.id}>
                        <td>
                          {new Date(rev.created_at).toLocaleString("ru-RU")}
                        </td>
                        <td>
                          <code>{rev.id.slice(0, 8)}</code>
                        </td>
                        <td>
                          {rev.is_current ? (
                            <Pill value="succeeded" />
                          ) : (
                            <Pill value={rev.latest_status} />
                          )}
                        </td>
                        <td>
                          <button
                            className="text-button"
                            disabled={busy}
                            onClick={() =>
                              work(async () => {
                                setPreview(
                                  await api<Preview>(
                                    `/revisions/${rev.id}/preview`,
                                  ),
                                );
                                setRollbackTarget(rev.can_rollback);
                                setValidated("");
                                setOperation(null);
                              })
                            }
                          >
                            {rev.can_rollback
                              ? "Сравнить и откатить"
                              : "Посмотреть diff"}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {!history.length && (
                  <p className="empty">
                    Ревизий пока нет. Подготовьте конфигурацию из черновика.
                  </p>
                )}
              </div>
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
            ref={modalRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby="client-title"
            className="modal"
          >
            <div className="section-head">
              <h2 id="client-title">
                {editing ? "Настройки клиента" : "Добавить клиента"}
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
                Описание
                <input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  maxLength={1024}
                  placeholder="Необязательное пояснение для оператора"
                />
                <small>
                  Видно только в этой панели. Backend хранит до 1024 символов.
                </small>
              </label>
              <label>
                Тип клиента
                <select
                  value={kind}
                  onChange={(e) => setKind(e.target.value as typeof kind)}
                >
                  <option value="host">Адрес / устройство</option>
                  <option value="network">Сеть</option>
                  <option value="group">Группа адресов и сетей</option>
                </select>
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
                  IP устройства, сеть CIDR или группа адресов, которые видит
                  Squid. Каждый элемент с новой строки.
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
                HTTPS сайтов проходит через CONNECT. Клиент проверяет исходный
                сертификат сайта.
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

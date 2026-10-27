import { Card } from "@/components/ui/card";
import { AppFrame } from "./AppFrame";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import "./theme.css";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { NativeSelect } from "@/components/ui/native-select";
import { Table } from "@/components/ui/table";
import { TableHeader } from "@/components/ui/table";
import { TableBody } from "@/components/ui/table";
import { TableRow } from "@/components/ui/table";
import { TableHead } from "@/components/ui/table";
import { TableCell } from "@/components/ui/table";
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
import { DiagnosticsPanel } from "./DiagnosticsPanel";
import { AccountsPanel, roleNames } from "./AccountsPanel";

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
    <Badge
      variant="outline"
      className={`pill ${value === "up" || value === "succeeded" || value === "validated" ? "good" : value === "down" || value === "rejected" || value === "degraded" ? "bad" : "neutral"}`}
    >
      <i />
      {labels[value] || value}
    </Badge>
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
    [events, setEvents] = useState<Event[]>([]);
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
    [transport, setTransport] = useState("http"),
    [proxyUsername, setProxyUsername] = useState("");
  const [proxyAccounts, setProxyAccounts] = useState<
    { username: string; enabled: boolean }[]
  >([]);
  const sessionRefreshRef = useRef<Promise<void> | null>(null);
  function refreshSession(): Promise<void> {
    if (!sessionRefreshRef.current) {
      sessionRefreshRef.current = api<Session>("/session")
        .then((s) => {
          setCsrf(s.csrf_token);
          setSession(s);
        })
        .finally(() => {
          sessionRefreshRef.current = null;
        });
    }
    return sessionRefreshRef.current;
  }
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
    // Refresh the cookie and CSRF holder before showing login after any
    // protected 401. /session itself keeps ordinary wrong-password semantics.
    if (r.status === 401 && path !== "/session") {
      await refreshSession();
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
  function allowed(permission: string, who: Session | null = session) {
    return who?.permissions?.includes(permission) ?? false;
  }
  async function load(who: Session | null = session) {
    setStatus(await api<Status>("/status"));
    if (allowed("configure", who)) {
      const [d, h, a] = await Promise.all([
        api<Draft>("/configuration/draft"),
        api<{ items: RevisionSummary[] }>("/revisions"),
        api<{ items: { username: string; enabled: boolean }[] }>(
          "/proxy-accounts",
        ),
      ]);
      setDraft(d);
      setHistory(h.items);
      setProxyAccounts(a.items);
    } else {
      setDraft(null);
      setHistory([]);
      setProxyAccounts([]);
    }
  }
  useEffect(() => {
    api<Session>("/session")
      .then((s) => {
        setCsrf(s.csrf_token);
        setSession(s);
        if (s.authenticated) load(s).catch((e) => setError(e.message));
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
        "/events",
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
            setError(e.message);
          }
        });
    }, 10000);
    return () => {
      active = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [session?.authenticated]);
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
      setPage("Обзор");
      await load(s);
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
    setProxyUsername(c?.proxy_username || "");
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
        proxy_username: proxyUsername || undefined,
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
        op = await api<Operation>(
          `/operations/${op.id}`,
          "GET",
          undefined,
          undefined,
          signal,
        );
        setOperation(op);
        if (i === 200 && ["validating", "reloading"].includes(op.status)) {
          setError(
            "Операция выполняется дольше обычного. Панель продолжает опрос статуса.",
          );
        }
      }
      setHistory(
        (
          await api<{ items: RevisionSummary[] }>(
            "/revisions",
            "GET",
            undefined,
            undefined,
            signal,
          )
        ).items,
      );
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
          <Button onClick={() => location.reload()}>Повторить</Button>
        </section>
      </main>
    );
  if (!session)
    return (
      <main className="login-wrap">
        <p>Подключение…</p>
      </main>
    );
  if (!session.authenticated)
    return (
      <main className="login-wrap">
        <section className="login">
          <h1>Squid Control</h1>
          <form onSubmit={login}>
            <label>
              Имя пользователя
              <Input
                name="username"
                autoComplete="username"
                required
                autoFocus
              />
            </label>
            <label>
              Пароль
              <Input
                name="password"
                type="password"
                autoComplete="current-password"
                required
              />
            </label>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <Button disabled={busy}>{busy ? "Вход…" : "Войти в панель"}</Button>
          </form>
        </section>
      </main>
    );
  const snap = draft?.snapshot;
  const canConfigure = allowed("configure"),
    canApply = allowed("apply");
  const navigation = [
    "Обзор",
    ...(canConfigure ? ["Клиенты", "Настройки", "Изменения"] : []),
    "События",
    ...(allowed("audit") ? ["Аудит"] : []),
    ...(allowed("users") ? ["Пользователи"] : []),
    ...(allowed("proxy_accounts") ? ["Логины прокси"] : []),
  ];
  return (
    <AppFrame
      navigation={navigation}
      page={page}
      onNavigate={setPage}
      username={session.username || ""}
      role={session.role ? roleNames[session.role] : ""}
      onLogout={logout}
      busy={busy}
    >
      <div className="workspace" id="main-content" tabIndex={-1}>
        <div className="page-title">
          <h1>{page}</h1>
          {page === "Обзор" && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => work(() => load())}
            >
              Обновить
            </Button>
          )}
        </div>
        {error && (
          <div className="error" role="alert">
            <span>{error}</span>
            <Button variant="ghost" onClick={() => setError("")}>
              Закрыть
            </Button>
          </div>
        )}
        {status?.runtime.state === "degraded" && (
          <p className="error" role="alert">
            Применение заблокировано: требуется восстановление состояния.
          </p>
        )}
        {canConfigure && status?.has_unapplied_changes && (
          <div className="pending-banner">
            <span>Есть неприменённые изменения</span>
            <Button variant="ghost" disabled={busy} onClick={freeze}>
              Посмотреть diff
            </Button>
          </div>
        )}
        {page === "Обзор" && (
          <>
            <Card
              className="status-panel"
              role="region"
              aria-label="Состояние прокси"
            >
              <div>
                <span>Squid</span>
                <Pill value={status?.squid.state || "unknown"} />
              </div>
              <div>
                <span>Управление</span>
                <Pill value={status?.runtime.state || "unknown"} />
              </div>
              <div className="status-time">
                {status?.squid.observed_at
                  ? `Проверено ${new Date(status.squid.observed_at).toLocaleTimeString("ru-RU")}`
                  : "Ожидаем данные"}
              </div>
            </Card>
            {canConfigure && (
              <Card className="overview-summary">
                <div>
                  <h2>
                    {snap?.configuration_mode === "expert"
                      ? "Экспертный режим"
                      : "Доступ клиентов"}
                  </h2>
                  <p>
                    {snap?.configuration_mode === "expert"
                      ? "Действуют правила squid.conf"
                      : snap?.clients.length
                        ? `${snap.clients.filter((c) => c.enabled).length} включено из ${snap.clients.length}`
                        : "Клиенты ещё не добавлены"}
                  </p>
                </div>
                <Button
                  variant="outline"
                  onClick={() => {
                    if (snap?.configuration_mode === "expert") {
                      setPage("Настройки");
                      return;
                    }
                    setPage("Клиенты");
                    if (!snap?.clients.length) openClient();
                  }}
                >
                  {snap?.configuration_mode === "expert"
                    ? "Открыть настройки"
                    : snap?.clients.length
                      ? "Открыть клиентов"
                      : "Добавить клиента"}
                </Button>
              </Card>
            )}
            <section className="section">
              <div className="section-head">
                <h2>Последние запросы</h2>
                <Button variant="ghost" onClick={() => setPage("События")}>
                  Весь журнал
                </Button>
              </div>
              <div className="table-wrap">
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
                        <TableCell>
                          {new Date(e.timestamp).toLocaleTimeString("ru-RU")}
                        </TableCell>
                        <TableCell className="mono">{e.source_ip}</TableCell>
                        <TableCell className="mono destination">
                          {e.destination}
                        </TableCell>
                        <TableCell
                          className={
                            e.proxy_result.includes("DENIED") ? "denied" : ""
                          }
                        >
                          {e.proxy_result}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {!events.length && <p className="empty">Запросов пока нет.</p>}
            </section>
          </>
        )}
        {page === "Клиенты" && canConfigure && (
          <section className="section">
            <div className="section-head">
              <h2>Правила доступа</h2>
              <Button
                onClick={() => openClient()}
                disabled={
                  busy || !draft || snap?.configuration_mode === "expert"
                }
              >
                Добавить клиента
              </Button>
            </div>
            {snap?.configuration_mode === "expert" && (
              <p className="notice">
                Экспертный режим: визуальные правила клиентов не действуют.
              </p>
            )}
            <div className="table-wrap">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Клиент</TableHead>
                    <TableHead>IP / сеть</TableHead>
                    <TableHead>Доступ</TableHead>
                    <TableHead>Состояние</TableHead>
                    <TableHead>
                      <span className="sr-only">Действия</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {snap?.clients.map((c) => {
                    const policy = snap.policies.find(
                      (p) => p.id === c.policy_id,
                    );
                    return (
                      <TableRow key={c.id}>
                        <TableCell>
                          <strong>{c.name}</strong>
                          {c.description && (
                            <small className="row-description">
                              {c.description}
                            </small>
                          )}
                          {c.proxy_username && (
                            <small className="row-description">
                              Логин: {c.proxy_username}
                            </small>
                          )}
                        </TableCell>
                        <TableCell className="mono">
                          {c.source_networks.join(", ")}
                        </TableCell>
                        <TableCell>
                          {policy?.mode === "all_web"
                            ? "Весь web"
                            : `${policy?.destinations.length || 0} доменов`}
                        </TableCell>
                        <TableCell>
                          <span
                            className={`client-state ${c.enabled ? "" : "disabled"}`}
                          >
                            {c.enabled ? "Включён" : "Отключён"}
                          </span>
                        </TableCell>
                        <TableCell className="row-actions">
                          <Button
                            variant="ghost"
                            disabled={
                              busy || snap.configuration_mode === "expert"
                            }
                            onClick={() => openClient(c)}
                          >
                            Изменить
                          </Button>
                          <Button
                            variant="ghost"
                            disabled={
                              busy || snap.configuration_mode === "expert"
                            }
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
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            {!snap?.clients.length && (
              <p className="empty">
                Добавьте клиента, чтобы разрешить ему доступ.
              </p>
            )}
            <div className="section-footer">
              <span>
                Черновик {draft?.version}. Изменения вступят в силу после
                применения.
              </span>
              <Button
                variant="outline"
                disabled={busy || !draft}
                onClick={freeze}
              >
                Посмотреть изменения
              </Button>
            </div>
          </section>
        )}
        {page === "Настройки" && draft && canConfigure && (
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
        {page === "Изменения" && canConfigure && (
          <>
            <section className="section">
              <div className="section-head">
                <h2>
                  {rollbackTarget
                    ? "Откат конфигурации"
                    : "Изменения конфигурации"}
                </h2>
                <Button
                  variant="outline"
                  onClick={freeze}
                  disabled={busy || !draft}
                >
                  Подготовить из черновика
                </Button>
              </div>
              {preview ? (
                <>
                  <ul className="change-summary">
                    {preview.summary.map((item, i) => (
                      <li key={i}>{item}</li>
                    ))}
                  </ul>
                  <pre
                    className="config-diff"
                    aria-label="Изменения squid.conf"
                  >
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
                  {preview.warnings.map((w) => (
                    <p className="notice" key={w}>
                      {w}
                    </p>
                  ))}
                  <details>
                    <summary>Полный squid.conf</summary>
                    <pre>{preview.generated_config}</pre>
                    <small>
                      Ревизия {preview.revision_id} · SHA-256{" "}
                      {preview.policy_hash}
                    </small>
                  </details>
                  <div className="section-footer">
                    <span>
                      {rollbackTarget
                        ? "Конфигурация будет проверена перед откатом"
                        : validated
                          ? "Эта ревизия прошла проверку"
                          : "Требуется проверка конфигурации"}
                    </span>
                    <div className="actions">
                      <Button
                        variant="outline"
                        disabled={
                          busy ||
                          rollbackTarget ||
                          status?.runtime.state === "degraded"
                        }
                        onClick={() => run("validate")}
                      >
                        Проверить
                      </Button>
                      <Button
                        disabled={
                          busy ||
                          !canApply ||
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
                      </Button>
                    </div>
                  </div>
                </>
              ) : (
                <p className="empty">
                  Подготовьте черновик для проверки и применения.
                </p>
              )}
              {operation && (
                <div className="operation">
                  <Pill value={operation.status} />
                  {operation.findings.length > 0 && (
                    <ul className="findings">
                      {operation.findings.map((f) => (
                        <li key={f}>{f}</li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </section>
            <section className="section">
              <div className="section-head">
                <h2>История ревизий</h2>
              </div>
              <div className="table-wrap">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Создана</TableHead>
                      <TableHead>Ревизия</TableHead>
                      <TableHead>Статус</TableHead>
                      <TableHead>
                        <span className="sr-only">Действия</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {history.map((rev) => (
                      <TableRow key={rev.id}>
                        <TableCell>
                          {new Date(rev.created_at).toLocaleString("ru-RU")}
                        </TableCell>
                        <TableCell className="mono">
                          {rev.id.slice(0, 8)}
                        </TableCell>
                        <TableCell>
                          <Pill
                            value={
                              rev.is_current ? "succeeded" : rev.latest_status
                            }
                          />
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="ghost"
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
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {!history.length && <p className="empty">Ревизий пока нет.</p>}
            </section>
          </>
        )}
        {(page === "События" || page === "Аудит") && (
          <DiagnosticsPanel
            key={page}
            kind={page === "Аудит" ? "audit" : "events"}
            request={api}
          />
        )}
        {page === "Пользователи" && allowed("users") && (
          <AccountsPanel
            kind="users"
            request={api}
            currentUser={session.username}
          />
        )}
        {page === "Логины прокси" && allowed("proxy_accounts") && (
          <AccountsPanel
            kind="proxy-accounts"
            request={api}
            currentUser={null}
          />
        )}
      </div>
      <Dialog
        open={showForm && canConfigure}
        onOpenChange={(open) => {
          if (!busy) setShowForm(open);
        }}
      >
        <DialogContent
          className="client-dialog"
          showCloseButton={false}
          aria-describedby={undefined}
        >
          <DialogHeader>
            <DialogTitle id="client-title">
              {editing ? "Настройки клиента" : "Добавить клиента"}
            </DialogTitle>
          </DialogHeader>
          <form onSubmit={submitClient}>
            <div className="form-grid">
              <label>
                Название
                <Input
                  aria-label="Название"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  maxLength={128}
                  autoFocus
                />
              </label>
              <label>
                Тип клиента
                <NativeSelect
                  value={kind}
                  onChange={(e) => setKind(e.target.value as typeof kind)}
                >
                  <option value="host">Устройство</option>
                  <option value="network">Сеть</option>
                  <option value="group">Группа адресов</option>
                </NativeSelect>
              </label>
            </div>
            <label>
              IP / CIDR
              <Textarea
                aria-label="IP / CIDR"
                value={networks}
                onChange={(e) => setNetworks(e.target.value)}
                required
                rows={2}
                placeholder="192.0.2.11 или 192.0.2.0/24"
              />
            </label>
            <label>
              Режим доступа
              <NativeSelect
                aria-label="Режим доступа"
                value={mode}
                onChange={(e) => setMode(e.target.value as typeof mode)}
              >
                <option value="allowlist">Только разрешённые ресурсы</option>
                <option value="all_web">Весь внешний HTTP/HTTPS</option>
              </NativeSelect>
            </label>
            {mode === "allowlist" && (
              <label>
                Разрешённые домены
                <Textarea
                  aria-label="Разрешённые домены"
                  value={domains}
                  onChange={(e) => setDomains(e.target.value)}
                  rows={4}
                  placeholder={"packages.example.org\n.archive.example.org"}
                />
                <small>
                  По одному на строку. Точка в начале включает поддомены.
                </small>
              </label>
            )}
            <div className="form-grid">
              <label>
                Подключение к прокси
                <NativeSelect
                  aria-label="Подключение к прокси"
                  value={transport}
                  onChange={(e) => {
                    setTransport(e.target.value);
                  }}
                >
                  <option value="http" disabled={!!proxyUsername}>
                    HTTP
                  </option>
                  <option
                    value="tls"
                    disabled={
                      !status?.capabilities.includes("tls_proxy_configured")
                    }
                  >
                    TLS
                    {status?.capabilities.includes("tls_proxy_configured")
                      ? ""
                      : " — сертификат не настроен"}
                  </option>
                </NativeSelect>
              </label>
              <label>
                Логин прокси
                <NativeSelect
                  aria-label="Логин прокси"
                  value={proxyUsername}
                  onChange={(e) => {
                    setProxyUsername(e.target.value);
                    if (e.target.value) setTransport("tls");
                  }}
                  disabled={
                    !status?.capabilities.includes("tls_proxy_configured")
                  }
                >
                  <option value="">Без пароля, по IP</option>
                  {proxyAccounts.map((a) => (
                    <option
                      key={a.username}
                      value={a.username}
                      disabled={!a.enabled}
                    >
                      {a.username}
                      {a.enabled ? "" : " — заблокирован"}
                    </option>
                  ))}
                  {proxyUsername &&
                    !proxyAccounts.some(
                      (a) => a.username === proxyUsername,
                    ) && <option value={proxyUsername}>{proxyUsername}</option>}
                </NativeSelect>
                {!status?.capabilities.includes("tls_proxy_configured") && (
                  <small>Для логина нужен TLS-сертификат прокси.</small>
                )}
              </label>
            </div>
            <details>
              <summary>Описание</summary>
              <label>
                <span className="sr-only">Описание</span>
                <Input
                  aria-label="Описание"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  maxLength={1024}
                />
              </label>
            </details>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <div className="modal-actions">
              <Button
                type="button"
                variant="outline"
                onClick={() => setShowForm(false)}
                disabled={busy}
              >
                Отмена
              </Button>
              <Button disabled={busy}>
                {busy ? "Сохранение…" : "Сохранить черновик"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </AppFrame>
  );
}
createRoot(document.getElementById("root")!).render(<App />);

import { useOperationRecovery, operationPending } from "./useOperationRecovery";
import { DestinationList } from "./DestinationList";
import { ChangesPanel } from "./ChangesPanel";
import { OverviewPanel } from "./OverviewPanel";
import { useOverview } from "./useOverview";
import { ConnectionError, fetchPanel } from "./api";
import { ThemeProvider } from "./ThemeProvider";
import { EntityDialog } from "./components/EntityDialog";
import { AppFrame } from "./AppFrame";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { StatusBadge as Pill } from "./StatusBadge";
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
class SessionExpired extends Error {
  constructor() {
    super("Сессия истекла. Войдите снова");
    this.name = "SessionExpired";
  }
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
  const [draft, setDraft] = useState<Draft | null>(null);
  const [page, setPage] = useState("Обзор");
  const [eventFailures, setEventFailures] = useState(false);
  const [connectionError, setConnectionError] = useState(false);
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [pendingNavigation, setPendingNavigation] = useState<string | null>(
    null,
  );
  const [preview, setPreview] = useState<Preview | null>(null),
    [operation, setOperation] = useState<Operation | null>(null),
    [validated, setValidated] = useState("");
  const [history, setHistory] = useState<RevisionSummary[]>([]);
  const [rollbackTarget, setRollbackTarget] = useState(false);
  const [reviewedRevision, setReviewedRevision] =
    useState<RevisionSummary | null>(null);
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
    let r: Response;
    try {
      r = await fetchPanel(`/api/v1${path}`, {
        method,
        headers,
        body: data === undefined ? undefined : JSON.stringify(data),
        signal,
      });
      if (path === "/status" || path === "/session") setConnectionError(false);
    } catch (e) {
      if (e instanceof ConnectionError) setConnectionError(true);
      throw e;
    }
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
  const {
    status,
    setStatus,
    statistics,
    metrics,
    failures,
    ingestion,
    overviewError,
    refreshOverview,
  } = useOverview(api, session?.authenticated === true);
  function navigate(next: string) {
    if (page === "Настройки" && settingsDirty && next !== page) {
      setPendingNavigation(next);
      return;
    }
    if (next === "События") setEventFailures(false);
    setPage(next);
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
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function connect() {
      try {
        const s = await api<Session>("/session");
        if (!active) return;
        setCsrf(s.csrf_token);
        setSession(s);
        setFatal("");
        if (s.authenticated) await load(s);
      } catch (e) {
        if (!active) return;
        if (!(e instanceof ConnectionError)) setFatal((e as Error).message);
        timer = setTimeout(connect, 5000);
      }
    }
    void connect();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, []);
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
      } else if (!(e instanceof ConnectionError)) {
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
  async function refreshChanges() {
    if (operationPending(operation)) return;
    await work(async () => {
      setPreview(await api<Preview>("/configuration/preview"));
      setRollbackTarget(false);
      setReviewedRevision(null);
      setValidated("");
      setOperation(null);
    });
  }
  function freeze() {
    if (page !== "Изменения") navigate("Изменения");
    else void refreshChanges();
  }
  useEffect(() => {
    if (page === "Изменения" && session?.authenticated && allowed("configure"))
      void refreshChanges();
  }, [page, session?.authenticated]);
  async function run(action: "validate" | "apply" | "rollback") {
    if (!preview) return;
    await work(async (signal) => {
      let target = preview;
      if (!target.revision_id) {
        const revision = await api<{ id: string }>("/revisions", "POST", {
          expected_draft_version: target.draft_version ?? draft?.version,
        });
        target = await api<Preview>(`/revisions/${revision.id}/preview`);
        setPreview(target);
      }
      const input = {
        action,
        revision_id: target.revision_id,
        expected_applied_revision: target.expected_applied_revision,
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
        setPreview(await api<Preview>("/configuration/preview"));
        setRollbackTarget(false);
        setReviewedRevision(null);
        setValidated("");
      }
      if (["rejected", "degraded", "rolled_back"].includes(op.status))
        setError(op.findings.join(" "));
    });
  }
  useOperationRecovery(
    api,
    operation,
    busy,
    session?.authenticated === true,
    async (next) => {
      setOperation(next);
      if (next.status === "validated") setValidated(next.id);
      if (next.status === "succeeded") {
        await load();
        setPreview(await api<Preview>("/configuration/preview"));
        setValidated("");
        setRollbackTarget(false);
        setReviewedRevision(null);
      }
      if (["rejected", "degraded", "rolled_back"].includes(next.status))
        setError(next.findings.join(" "));
    },
  );
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
          <h1>Панель управления</h1>
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
            {connectionError && (
              <p className="error" role="alert">
                Нет связи с панелью. Повторяем подключение…
              </p>
            )}
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
      onNavigate={navigate}
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
              onClick={() => work(() => refreshOverview())}
            >
              Обновить
            </Button>
          )}
        </div>
        {connectionError && (
          <div className="error" role="alert">
            Нет связи с панелью. Повторяем подключение…
          </div>
        )}
        {error && !showForm && (
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
        {canConfigure &&
          page !== "Изменения" &&
          status?.has_unapplied_changes && (
            <div className="pending-banner">
              <span>Есть неприменённые изменения</span>
              <Button variant="ghost" disabled={busy} onClick={freeze}>
                Посмотреть изменения
              </Button>
            </div>
          )}
        {page === "Обзор" && (
          <OverviewPanel
            status={status}
            statistics={statistics}
            metrics={metrics}
            failures={failures}
            ingestion={ingestion}
            offline={connectionError}
            error={overviewError}
            onEvents={(onlyFailures = false) => {
              setEventFailures(onlyFailures);
              setPage("События");
            }}
          />
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
                          {policy?.mode === "all_web" ? (
                            "Весь web"
                          ) : (
                            <DestinationList
                              items={policy?.destinations ?? []}
                            />
                          )}
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
          </section>
        )}
        {page === "Настройки" && draft && canConfigure && (
          <SettingsPanel
            snapshot={draft.snapshot}
            busy={busy}
            onDirtyChange={setSettingsDirty}
            onPreview={async () =>
              (await api<Preview>("/configuration/preview")).generated_config
            }
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
          <ChangesPanel
            preview={preview}
            operation={operation}
            history={history}
            busy={busy || operationPending(operation)}
            validated={!!validated}
            rollback={rollbackTarget}
            reviewedRevision={reviewedRevision}
            canApply={canApply}
            blocked={status?.runtime.state === "degraded"}
            onRefresh={refreshChanges}
            onValidate={() => run("validate")}
            onApply={() => run(rollbackTarget ? "rollback" : "apply")}
            onHistory={(rev) =>
              work(async () => {
                setPreview(await api<Preview>(`/revisions/${rev.id}/preview`));
                setRollbackTarget(rev.can_rollback);
                setReviewedRevision(rev);
                setValidated("");
                setOperation(null);
              })
            }
          />
        )}
        {(page === "События" || page === "Аудит") && (
          <DiagnosticsPanel
            key={page}
            kind={page === "Аудит" ? "audit" : "events"}
            initialFailures={eventFailures}
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
      <EntityDialog
        open={showForm && canConfigure}
        onOpenChange={setShowForm}
        busy={busy}
        title={editing ? "Настройки клиента" : "Добавить клиента"}
        onSubmit={submitClient}
        error={error || (connectionError ? "Нет связи с панелью" : "")}
        submitLabel="Сохранить черновик"
      >
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
              disabled={!status?.capabilities.includes("tls_proxy_configured")}
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
                !proxyAccounts.some((a) => a.username === proxyUsername) && (
                  <option value={proxyUsername}>{proxyUsername}</option>
                )}
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
      </EntityDialog>
      <Dialog
        open={pendingNavigation !== null}
        onOpenChange={(open) => {
          if (!open) setPendingNavigation(null);
        }}
      >
        <DialogContent aria-describedby={undefined}>
          <DialogHeader>
            <DialogTitle>Несохранённые изменения</DialogTitle>
          </DialogHeader>
          <p>Выйти из настроек без сохранения?</p>
          <div className="actions justify-end">
            <Button
              variant="outline"
              onClick={() => setPendingNavigation(null)}
            >
              Остаться
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setSettingsDirty(false);
                setPage(pendingNavigation!);
                setPendingNavigation(null);
              }}
            >
              Не сохранять
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </AppFrame>
  );
}
createRoot(document.getElementById("root")!).render(
  <ThemeProvider>
    <App />
  </ThemeProvider>,
);

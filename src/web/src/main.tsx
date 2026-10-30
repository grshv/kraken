import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { KeyRound, SlidersHorizontal, History, Activity } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Card } from "@/components/ui/card";
import { Field, Callout } from "@/components/kraken/blocks";
import { ConnectionError, fetchPanel, type Request } from "./api";
import { ThemeProvider } from "./ThemeProvider";
import type {
  Draft,
  PanelRole,
  Preview,
  Revision,
  RevisionSummary,
  Session,
  Snapshot,
  Status,
} from "./model";
import { navItems, type NavId } from "./shell/nav";
import { paletteIntent } from "./shell/paletteIntent";
import { InspectorShell, type ShellHandlers } from "./shell/InspectorShell";
import { KrakenMark } from "./shell/KrakenMark";
import type { ConfirmOptions } from "./shell/ConfirmDialog";
import { useApplyFlow } from "./draft/useApplyFlow";
import { HelpView } from "./help/HelpView";
import { ClientsSection } from "./clients/ClientsSection";
import { PoliciesSection } from "./policies/PoliciesSection";
import { LogSection } from "./log/LogSection";
import { AuditSection } from "./audit/AuditSection";
import { AccountsSection } from "./accounts/AccountsSection";
import { SettingsSection } from "./settings/SettingsSection";
import type { CommitResult, SectionIntent } from "./sections/sectionProps";
import { ErrorState } from "./shell/ErrorState";
import "./theme.css";
import "./style.css";

// SessionExpired is signalled by api() on any protected 401; the App drops to
// the login view and lets the next /session refresh establish a fresh token.
class SessionExpired extends Error {
  constructor() {
    super("Сессия истекла. Войдите снова");
    this.name = "SessionExpired";
  }
}

// ApiError carries the stable server code so applyFlow can route 409 -> conflict
// and 403 draft_out_of_scope -> failed-with-code without string matching.
class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message || code);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function App() {
  const [csrf, setCsrf] = useState("");
  const [session, setSession] = useState<Session | null>(null);
  const [fatal, setFatal] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [active, setActive] = useState<Snapshot | null>(null);
  const [status, setStatus] = useState<Status | null>(null);
  const [newNotifications, setNewNotifications] = useState(0);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [history, setHistory] = useState<RevisionSummary[]>([]);
  const [connectionError, setConnectionError] = useState(false);
  const [navId, setNavId] = useState<NavId>("clients");
  const [sectionIntent, setSectionIntent] = useState<SectionIntent | undefined>(
    undefined,
  );
  const openWithIntent = useCallback((nav: NavId, intent: SectionIntent) => {
    setNavId(nav);
    setSectionIntent(intent);
  }, []);
  const [toast, setToast] = useState("");
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null);
  const [pendingAction, setPendingAction] = useState<string | null>(null);
  const sessionRefreshRef = useRef<Promise<void> | null>(null);
  const workAbortRef = useRef<AbortController | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  function showToast(message: string) {
    setToast(message);
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(""), 2600);
  }

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
  // function bound to the current token. 401 on a protected path refreshes the
  // session and throws SessionExpired so the caller drops to login.
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
      if (path === "/status" || path === "/session") {
        setConnectionError(false);
      }
    } catch (e) {
      if (e instanceof ConnectionError) setConnectionError(true);
      throw e;
    }
    if (r.status === 204) return undefined as unknown as T;
    if (r.status === 401 && path !== "/session") {
      await refreshSession();
      throw new SessionExpired();
    }
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const ct = r.headers.get("content-type") || "";
    if (!ct.toLowerCase().startsWith("application/json")) {
      if (!r.ok) throw new Error(`Сервер недоступен (${r.status})`);
      throw new Error(`Неожиданный ответ сервера (${r.status})`);
    }
    const result = await r.json();
    if (!r.ok) {
      const code = (result && (result.code as string)) || `error_${r.status}`;
      const message = (result && (result.message as string)) || code;
      throw new ApiError(r.status, code, message);
    }
    return result as T;
  }

  // Stable identity: sections key effects on request, and api is recreated on
  // every render (it closes over the CSRF token), which refetched and blanked
  // panels on each status poll.
  const apiRef = useRef(api);
  apiRef.current = api;
  const request = useCallback<Request>(
    (path, method, data, key, signal) =>
      apiRef.current(path, method, data, key, signal),
    [],
  );

  const allowed = useCallback(
    (permission: string, who: Session | null = session) =>
      who?.permissions?.includes(permission) ?? false,
    [session],
  );

  async function load(who: Session | null = session) {
    setStatus(await api<Status>("/status"));
    if (allowed("configure", who)) {
      const [d, h, a] = await Promise.all([
        api<Draft>("/configuration/draft"),
        api<{ items: RevisionSummary[] }>("/revisions"),
        api<{ snapshot: import("./model").Snapshot }>("/configuration/active"),
      ]);
      setDraft(d);
      setHistory(h.items);
      setActive(a.snapshot);
    } else {
      setDraft(null);
      setActive(null);
      setHistory([]);
    }
  }

  // Bootstrap: fetch /session, and if authenticated, draft+status. Retries on
  // transport failure so a transient proxy/TLS hiccup doesn't strand the UI.
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Background status poll: keeps the draft-tray and status indicator fresh
  // without forcing a full load. Stops while a work() mutation is in-flight.
  useEffect(() => {
    if (!session?.authenticated) return;
    let active = true;
    let timer: ReturnType<typeof setInterval>;
    async function tick() {
      try {
        const s = await api<Status>(
          "/status",
          "GET",
          undefined,
          undefined,
          undefined,
        );
        if (active) setStatus(s);
      } catch {
        // surfaced by the connection banner; no need to spam toasts
      }
      // Rail badge on Клиенты: new notifications (denied domains missing
      // from a policy and undescribed sources).
      try {
        const u = await api<{ counts: { new: number } }>("/notifications");
        if (active) setNewNotifications(u.counts?.new ?? 0);
      } catch {
        // the badge is optional; keep the last value
      }
    }
    void tick();
    timer = setInterval(tick, 10000);
    return () => {
      active = false;
      clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.authenticated, csrf]);

  function work(fn: (signal?: AbortSignal) => Promise<void>) {
    setBusy(true);
    setError("");
    const controller = new AbortController();
    workAbortRef.current = controller;
    return fn(controller.signal).finally(() => {
      if (workAbortRef.current === controller) {
        workAbortRef.current = null;
      }
      setBusy(false);
    });
  }

  useEffect(() => {
    return () => workAbortRef.current?.abort();
  }, []);

  async function login(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    await work(async () => {
      const s = await api<Session>("/session", "POST", {
        username: form.get("username"),
        password: form.get("password"),
      });
      setCsrf(s.csrf_token);
      setSession(s);
      await load(s);
    }).catch(async (e) => {
      if (e instanceof ApiError) setError(e.message);
      else if (!(e instanceof ConnectionError)) setError((e as Error).message);
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
    }).catch(async (e) => {
      if (e instanceof ApiError) setError(e.message);
      else if (!(e instanceof ConnectionError)) setError((e as Error).message);
    });
  }

  // Apply flow wiring. onApplied reloads draft/status and clears the sheet.
  // Recovery lives inside the hook so the same operation state drives both the
  // visible steps and the post-transport-failure poll.
  const role: PanelRole = session?.role === "manager" ? "manager" : "admin";
  const applyRun = useApplyFlow(
    request,
    draft,
    role,
    session?.authenticated === true,
    async () => {
      const [d, s] = await Promise.all([
        api<Draft>("/configuration/draft"),
        api<Status>("/status"),
      ]);
      setDraft(d);
      setStatus(s);
      setPreview(
        await api<Preview>("/configuration/preview").catch(() => preview),
      );
      showToast("Изменения применены");
    },
  );

  // commitDraft PUTs the next snapshot with the current expected_version and
  // returns the stable server error code so sections can surface field errors.
  // 409 reloads draft+active and toasts, returning revision_conflict.
  async function commitDraft(next: Snapshot): Promise<CommitResult> {
    if (!draft) return { ok: false, code: "no_draft" };
    try {
      const result = await api<Draft>("/configuration/draft", "PUT", {
        expected_version: draft.version,
        snapshot: next,
      });
      setDraft(result);
      return { ok: true };
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 409) {
          showToast("Черновик изменён другим. Обновляем…");
          try {
            const [d, a] = await Promise.all([
              api<Draft>("/configuration/draft"),
              api<{ snapshot: Snapshot }>("/configuration/active"),
            ]);
            setDraft(d);
            setActive(a.snapshot);
          } catch {
            // transport error already surfaced by api()
          }
          return { ok: false, code: "revision_conflict" };
        }
        return { ok: false, code: e.code, message: e.message };
      }
      return { ok: false, message: (e as Error).message };
    }
  }

  // discardDraftNow DELETEs the draft (resets to active snapshot) and reloads.
  async function discardDraftNow(): Promise<void> {
    if (!draft) return;
    await work(async () => {
      try {
        const result = await api<Draft>("/configuration/draft", "DELETE", {
          expected_version: draft.version,
        });
        setDraft(result);
        const a = await api<{ snapshot: Snapshot }>("/configuration/active");
        setActive(a.snapshot);
        showToast("Черновик сброшен");
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          showToast("Черновик уже изменён. Обновляем…");
          setDraft(await api<Draft>("/configuration/draft"));
        } else if (e instanceof ApiError) {
          setError(e.message);
        } else if (!(e instanceof ConnectionError)) {
          setError((e as Error).message);
        }
      }
    });
  }

  // One-shot palette intents (new-client etc.) are handed to the active
  // section; the section clears this once it has handled the hint.
  const handleIntentHandled = useCallback(
    () => setSectionIntent(undefined),
    [],
  );

  const handlers: ShellHandlers = {
    onLogout: () => {
      void logout();
    },
    onChangePassword: () => {
      setNavId("accounts");
      showToast("Откроется форма смены пароля");
    },
    onRefreshDraft: async () => {
      await work(async () => {
        setDraft(await api<Draft>("/configuration/draft"));
        setPreview(
          await api<Preview>("/configuration/preview").catch(() => preview),
        );
      }).catch(async (e) => {
        if (e instanceof ApiError) setError(e.message);
        else if (!(e instanceof ConnectionError))
          setError((e as Error).message);
      });
    },
    onDiscardDraft: () => {
      setConfirm({
        title: "Сбросить черновик?",
        text: "Все несохранённые изменения будут удалены. Действующая конфигурация не изменится.",
        ok: "Сбросить",
        danger: true,
      });
      setPendingAction("discard");
    },
    onOpenAudit: () => setNavId("audit"),
    onAction: (id) => {
      const intent = paletteIntent(id, role);
      if (intent && intent.kind === "nav") {
        setNavId(intent.nav);
        if (intent.intent) setSectionIntent(intent.intent);
      }
    },
  };

  function confirmYes() {
    const action = pendingAction;
    setConfirm(null);
    setPendingAction(null);
    if (action === "discard") {
      void discardDraftNow();
    }
  }

  // Fatal / connecting / login views.
  if (fatal)
    return (
      <main className="grid min-h-dvh place-items-center bg-background p-6">
        <ErrorState
          title="Панель недоступна"
          detail={fatal}
          onRetry={() => location.reload()}
        />
      </main>
    );
  if (!session)
    return (
      <main className="grid min-h-dvh place-items-center bg-background p-6">
        <Card className="w-full max-w-[410px] gap-4 p-8 shadow-[0_8px_30px_#0000000d]">
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-[-0.015em]">
            <KrakenMark title="" className="size-8" />
            Kraken
          </h1>
          <p className="text-muted-foreground">Подключение…</p>
        </Card>
      </main>
    );
  if (!session.authenticated)
    return (
      <LoginView
        onLogin={login}
        busy={busy}
        connectionError={connectionError}
        error={error}
      />
    );

  const canConfigure = allowed("configure");
  const navItemsForRole = navItems(role);
  // Keep nav inside the role's allowed set (defensive: if the server trims a
  // permission, we don't show a dead section).
  const visibleNav: NavId[] = navItemsForRole
    .map((n) => n.id)
    .filter((id) => {
      if (id === "settings") return canConfigure;
      return true;
    });

  function renderNav(): React.ReactNode {
    if (navId === "help") return <HelpView request={request} />;
    if (navId === "clients")
      return (
        <ClientsSection
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
          intent={sectionIntent}
          onIntentHandled={handleIntentHandled}
        />
      );
    if (navId === "policies")
      return (
        <PoliciesSection
          intent={sectionIntent}
          onIntentHandled={handleIntentHandled}
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
        />
      );
    if (navId === "log")
      return (
        <LogSection
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
        />
      );
    if (navId === "audit")
      return (
        <AuditSection
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
        />
      );
    if (navId === "accounts")
      return (
        <AccountsSection
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
          intent={sectionIntent}
          onIntentHandled={handleIntentHandled}
        />
      );
    if (navId === "settings")
      return (
        <SettingsSection
          request={request}
          draft={draft}
          active={active}
          session={session!}
          onCommit={commitDraft}
          onDiscard={async () => {
            await discardDraftNow();
            return { ok: true };
          }}
          onToast={showToast}
          onNavigate={setNavId}
          onOpen={openWithIntent}
          onConfirm={(o) => setConfirm(o)}
          intent={sectionIntent}
          onIntentHandled={handleIntentHandled}
        />
      );
    return null;
  }

  return (
    <InspectorShell
      session={session}
      status={status}
      draft={draft}
      preview={preview}
      operation={applyRun.operation}
      applyRun={applyRun}
      navId={visibleNav.includes(navId) ? navId : "help"}
      badges={{ clients: newNotifications }}
      onOpen={openWithIntent}
      request={request}
      settings={active?.settings}
      onNavigate={setNavId}
      handlers={handlers}
      toast={toast}
      onToastDismiss={() => setToast("")}
      confirm={confirm}
      onConfirm={confirmYes}
      onConfirmCancel={() => {
        setConfirm(null);
        setPendingAction(null);
      }}
    >
      {connectionError && (
        <Alert
          variant="destructive"
          className="mx-5 my-4 flex items-center justify-between gap-4"
          role="alert"
        >
          <AlertDescription>
            Нет связи с панелью. Повторяем подключение…
          </AlertDescription>
        </Alert>
      )}
      {error && !busy && (
        <Alert
          variant="destructive"
          className="mx-5 my-4 flex items-center justify-between gap-4"
          role="alert"
        >
          <AlertDescription>{error}</AlertDescription>
          <Button variant="ghost" size="sm" onClick={() => setError("")}>
            Закрыть
          </Button>
        </Alert>
      )}
      {renderNav()}
    </InspectorShell>
  );
}

function LoginView({
  onLogin,
  busy,
  connectionError,
  error,
}: {
  onLogin: (e: React.FormEvent<HTMLFormElement>) => void;
  busy: boolean;
  connectionError: boolean;
  error: string;
}) {
  return (
    <main className="grid min-h-dvh place-items-center bg-background p-6">
      <Card className="w-full max-w-[410px] gap-4 p-8 shadow-[0_8px_30px_#0000000d]">
        <h1 className="flex items-center gap-2 text-xl font-semibold tracking-[-0.015em]">
          <KrakenMark title="" className="size-8" />
          Kraken
        </h1>
        <form onSubmit={onLogin} className="grid gap-4">
          <Field label="Имя пользователя" htmlFor="login-username">
            <Input
              id="login-username"
              name="username"
              autoComplete="username"
              required
              autoFocus
            />
          </Field>
          <Field label="Пароль" htmlFor="login-password">
            <Input
              id="login-password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
            />
          </Field>
          {connectionError && (
            <Callout tone="bad">
              Нет связи с панелью. Повторяем подключение…
            </Callout>
          )}
          {error && <Callout tone="bad">{error}</Callout>}
          <Button type="submit" size="lg" className="w-full" disabled={busy}>
            {busy ? "Вход…" : "Войти в панель"}
          </Button>
        </form>
      </Card>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <ThemeProvider>
    <App />
  </ThemeProvider>,
);

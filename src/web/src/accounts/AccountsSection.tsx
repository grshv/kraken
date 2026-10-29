import { useEffect, useState } from "react";
import { Ban, KeyRound, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import {
  buildManagerScopeBody,
  canDemote,
  canDisable,
  type PanelUser,
} from "./rules";
import { parseNetwork } from "../clients/networks";
import type { SectionProps } from "../sections/sectionProps";
import type { NavId } from "../shell/nav";

// API shapes mirrored from openapi.json (User, ProxyAccount, ManagerScope).
type User = { username: string; role: "admin" | "manager"; enabled: boolean };
type ProxyAccount = {
  username: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  owner?: string | null;
};
type ScopeBody = { networks: string[]; client_ids: string[] };
type DraftRow = { id: string; name: string; proxy_username: string };

// ADR 0016 permission table: rows compare admin vs manager scope.
const PERMS: [string, string, string][] = [
  ["Состояние сервера", "да", "да"],
  ["Клиенты, журнал, аудит", "все", "только назначенные"],
  ["Менять клиентов и их политики", "все", "назначенных"],
  ["Создавать клиентов", "любые адреса", "в своих сетях"],
  ["Править общие политики", "да", "если все её клиенты назначены"],
  ["Применять изменения", "да", "если черновик только про его клиентов"],
  [
    "Логины прокси",
    "все",
    "создаёт свои; управляет своими и теми, что используют только его клиенты",
  ],
  ["Настройки, запреты для всех, аккаунты", "да", "нет"],
];

// AccountsSection: panel users (admin only) and proxy logins. Admin sees both
// groups; manager sees only "Логины прокси" (server filters the list).
export function AccountsSection(props: SectionProps) {
  const {
    request,
    draft,
    session,
    onToast,
    onNavigate,
    intent,
    onIntentHandled,
  } = props;
  const [users, setUsers] = useState<User[] | null>(null);
  const [accounts, setAccounts] = useState<ProxyAccount[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState<"panel" | "proxy" | null>(null);

  const canManageUsers = !!session.permissions?.includes("users");
  const isAdmin = session.role === "admin";
  const snap = draft?.snapshot;
  const clients = snap?.clients ?? [];

  // Command palette "new-proxy-login": open the proxy login form once.
  useEffect(() => {
    if (intent !== "new-proxy-login") return;
    setCreating("proxy");
    onIntentHandled?.();
  }, [intent, onIntentHandled]);

  useEffect(() => {
    let live = true;
    if (canManageUsers) {
      request<{ items: User[] }>("/users")
        .then((r) => live && setUsers(r.items ?? []))
        .catch(() => live && setUsers(null));
    } else {
      setUsers([]);
    }
    request<{ items: ProxyAccount[] }>("/proxy-accounts")
      .then((r) => live && setAccounts(r.items ?? []))
      .catch(() => live && setAccounts(null));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request, canManageUsers]);

  // Fatal surface only when a required list failed to load.
  const fatal = (canManageUsers && users === null) || accounts === null;

  if (fatal && !creating)
    return (
      <section className="inspector-pane">
        <div className="inspector-body">
          <ErrorState
            title="Аккаунты недоступны"
            detail="Нет связи с панелью. Повторите запрос."
            onRetry={() => location.reload()}
          />
        </div>
      </section>
    );

  if (creating) {
    return (
      <NewAccountForm
        kind={creating}
        onCancel={() => setCreating(null)}
        onCreate={async (username, password, role) => {
          if (!username || !password) {
            setError("Укажите имя и пароль");
            return false;
          }
          setError(null);
          try {
            if (creating === "panel") {
              await request("/users", "POST", { username, password, role });
              const r = await request<{ items: User[] }>("/users");
              setUsers(r.items ?? []);
              onToast("Пользователь создан");
            } else {
              await request("/proxy-accounts", "POST", { username, password });
              const r = await request<{ items: ProxyAccount[] }>(
                "/proxy-accounts",
              );
              setAccounts(r.items ?? []);
              onToast("Логин создан. Пароль передан один раз");
            }
            setSelected(username);
            setCreating(null);
            return true;
          } catch (e) {
            setError((e as Error).message);
            return false;
          }
        }}
        error={error}
      />
    );
  }

  const panelUsers: PanelUser[] = (users ?? []).map((u) => ({
    username: u.username,
    role: u.role,
    enabled: u.enabled,
  }));
  const proxyRows = accounts ?? [];
  const draftRows: DraftRow[] = clients.map((c) => ({
    id: c.id,
    name: c.name,
    proxy_username: c.proxy_username ?? "",
  }));

  return (
    <div className="placeholder-section">
      <aside className="list-pane">
        <div className="list-head">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <span className="list-title">Аккаунты</span>
            {canManageUsers ? (
              <Button size="xs" onClick={() => setCreating("panel")}>
                <Plus aria-hidden /> Пользователь
              </Button>
            ) : (
              <Button size="xs" onClick={() => setCreating("proxy")}>
                <Plus aria-hidden /> Логин
              </Button>
            )}
          </div>
          <div className="muted-text small">
            {isAdmin
              ? "Кто входит в панель и какие логины принимает прокси."
              : "Логины для ваших клиентов, подключённых по TLS."}
          </div>
        </div>
        <div className="list-body">
          {canManageUsers && (
            <>
              <div className="list-group-label">Панель управления</div>
              {panelUsers.map((u) => (
                <button
                  key={u.username}
                  className={
                    "list-item" + (selected === u.username ? " selected" : "")
                  }
                  style={{ gridTemplateColumns: "minmax(0,1fr) auto auto" }}
                  onClick={() => setSelected(u.username)}
                >
                  <span className="ellipsis" style={{ fontWeight: 500 }}>
                    {u.username}
                  </span>
                  {!u.enabled && (
                    <Badge variant="secondary">заблокирован</Badge>
                  )}
                  <span className="muted-text small">
                    {u.role === "admin" ? "Администратор" : "Менеджер"}
                  </span>
                </button>
              ))}
              {panelUsers.length === 0 && (
                <div
                  className="muted-text small"
                  style={{ padding: "8px 14px" }}
                >
                  Нет пользователей
                </div>
              )}
            </>
          )}
          <div className="list-group-label">Логины прокси</div>
          {proxyRows.map((a) => {
            // Where used: draft clients that reference this proxy login.
            const usedBy = draftRows.filter(
              (c) => c.proxy_username === a.username,
            );
            return (
              <button
                key={a.username}
                className={
                  "list-item" + (selected === a.username ? " selected" : "")
                }
                style={{ gridTemplateColumns: "minmax(0,1fr) auto auto" }}
                onClick={() => setSelected(a.username)}
              >
                <span className="mono ellipsis">{a.username}</span>
                {!a.enabled && <Badge variant="secondary">выкл</Badge>}
                <span className="muted-text small ellipsis">
                  {usedBy.length
                    ? usedBy.map((c) => c.name).join(", ")
                    : "не используется"}
                </span>
              </button>
            );
          })}
          {proxyRows.length === 0 && (
            <div className="muted-text small" style={{ padding: "8px 14px" }}>
              Нет логинов прокси
            </div>
          )}
        </div>
      </aside>
      <section className="inspector-pane">
        {selected ? (
          <AccountDetail
            username={selected}
            users={panelUsers}
            accounts={proxyRows}
            draftClients={draftRows}
            session={session}
            request={request}
            onToast={onToast}
            onNavigate={onNavigate}
            onMutated={async () => {
              // Refetch both lists to reflect mutated enabled/role/owner.
              if (canManageUsers) {
                const r = await request<{ items: User[] }>("/users").catch(
                  () => null,
                );
                if (r) setUsers(r.items ?? []);
              }
              const r = await request<{ items: ProxyAccount[] }>(
                "/proxy-accounts",
              ).catch(() => null);
              if (r) setAccounts(r.items ?? []);
            }}
          />
        ) : (
          <div className="inspector-body">
            <EmptyState icon={<KeyRound />} title="Выберите аккаунт">
              {canManageUsers
                ? "Выберите пользователя панели или логин прокси слева."
                : "Выберите логин прокси слева или создайте новый."}
            </EmptyState>
          </div>
        )}
      </section>
    </div>
  );
}

function AccountDetail({
  username,
  users,
  accounts,
  draftClients,
  session,
  request,
  onToast,
  onMutated,
}: {
  username: string;
  users: PanelUser[];
  accounts: ProxyAccount[];
  draftClients: DraftRow[];
  session: SectionProps["session"];
  request: SectionProps["request"];
  onToast: (m: string) => void;
  onNavigate: (id: NavId) => void;
  onMutated: () => Promise<void>;
}) {
  const user = users.find((u) => u.username === username) ?? null;
  const account = accounts.find((a) => a.username === username) ?? null;
  const [error, setError] = useState<string | null>(null);

  if (user) {
    return (
      <PanelUserDetail
        user={user}
        users={users}
        draftClients={draftClients}
        session={session}
        request={request}
        onToast={onToast}
        onMutated={onMutated}
        error={error}
        setError={setError}
      />
    );
  }
  if (account) {
    return (
      <ProxyAccountDetail
        account={account}
        draftClients={draftClients}
        session={session}
        request={request}
        onToast={onToast}
        onMutated={onMutated}
        error={error}
        setError={setError}
      />
    );
  }
  return (
    <div className="inspector-body">
      <EmptyState icon={<KeyRound />} title="Не найдено">
        Аккаунт не существует или вне вашей области.
      </EmptyState>
    </div>
  );
}

function PanelUserDetail({
  user,
  users,
  draftClients,
  session,
  request,
  onToast,
  onMutated,
  error,
  setError,
}: {
  user: PanelUser;
  users: PanelUser[];
  draftClients: DraftRow[];
  session: SectionProps["session"];
  request: SectionProps["request"];
  onToast: (m: string) => void;
  onMutated: () => Promise<void>;
  error: string | null;
  setError: (s: string | null) => void;
}) {
  const self = session.username === user.username;
  const [pwd, setPwd] = useState("");
  const [scope, setScope] = useState<ScopeBody | null>(null);
  const [scopeErr, setScopeErr] = useState<string | null>(null);
  const [scopeDraft, setScopeDraft] = useState<{
    networks: string[];
    clientIDs: string[];
  } | null>(null);

  // Load the manager's current scope (admin-only endpoint). Non-manager target
  // returns 422 not_manager; we skip the editor.
  useEffect(() => {
    let live = true;
    setScope(null);
    setScopeErr(null);
    setScopeDraft(null);
    if (user.role !== "manager") return;
    request<ScopeBody>(`/users/${user.username}/scope`)
      .then((s) => {
        if (!live) return;
        setScope(s);
        setScopeDraft({
          networks: s.networks ?? [],
          clientIDs: s.client_ids ?? [],
        });
      })
      .catch((e) => {
        if (!live) return;
        setScopeErr((e as Error).message);
      });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request, user.username, user.role]);

  const demote = canDemote(users, user.username);
  const disable = canDisable(users, user.username);

  async function patch(payload: Record<string, unknown>, ok: string) {
    setError(null);
    try {
      await request(`/users/${user.username}`, "PATCH", payload);
      onToast(ok);
      await onMutated();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function savePassword() {
    if (!pwd) {
      onToast("Введите новый пароль");
      return;
    }
    await patch({ password: pwd }, "Пароль изменён, сессии отозваны");
    setPwd("");
  }

  async function saveScope() {
    if (!scopeDraft) return;
    setScopeErr(null);
    const body = buildManagerScopeBody(
      scopeDraft.networks,
      scopeDraft.clientIDs,
      parseNetwork,
    );
    try {
      const updated = await request<ScopeBody>(
        `/users/${user.username}/scope`,
        "PUT",
        body,
      );
      setScope(updated);
      setScopeDraft({
        networks: updated.networks ?? [],
        clientIDs: updated.client_ids ?? [],
      });
      onToast("Область менеджера обновлена");
    } catch (e) {
      setScopeErr((e as Error).message);
    }
  }

  return (
    <>
      <header className="inspector-head">
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <h1 className="page-title" style={{ margin: 0 }}>
            {user.username}
          </h1>
          {self && <Badge variant="secondary">это вы</Badge>}
          <Badge variant="secondary">
            {user.role === "admin" ? "Администратор" : "Менеджер"}
          </Badge>
          {user.enabled ? (
            <Badge className="badge-ok">активен</Badge>
          ) : (
            <Badge variant="secondary">заблокирован</Badge>
          )}
          <span className="grow" />
        </div>
      </header>
      <div className="inspector-body">
        {error && (
          <div className="callout callout-bad">
            <Ban aria-hidden />
            <div className="small">{error}</div>
          </div>
        )}
        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Роль</span>
          </div>
          <div className="card-body">
            <div className="opts">
              <button
                type="button"
                className={"opt" + (user.role === "admin" ? " selected" : "")}
                aria-pressed={user.role === "admin"}
                disabled={user.role === "admin"}
                onClick={() => patch({ role: "admin" }, "Роль изменена")}
              >
                <span className="radio" aria-hidden />
                <span>
                  <div style={{ fontWeight: 500 }}>Администратор</div>
                  <div className="small muted-text">
                    Всё, включая настройки и аккаунты
                  </div>
                </span>
              </button>
              <button
                type="button"
                className={"opt" + (user.role === "manager" ? " selected" : "")}
                aria-pressed={user.role === "manager"}
                disabled={user.role === "admin" && !demote}
                title={
                  user.role === "admin" && !demote
                    ? "Нельзя понизить последнего администратора"
                    : undefined
                }
                onClick={() => patch({ role: "manager" }, "Роль изменена")}
              >
                <span className="radio" aria-hidden />
                <span>
                  <div style={{ fontWeight: 500 }}>Менеджер</div>
                  <div className="small muted-text">
                    Только назначенные клиенты
                  </div>
                </span>
              </button>
            </div>
            <table style={{ width: "100%", marginTop: 10 }}>
              <thead>
                <tr>
                  <th style={{ textAlign: "left" }}>Право</th>
                  <th>Администратор</th>
                  <th>Менеджер</th>
                </tr>
              </thead>
              <tbody>
                {PERMS.map(([p, ad, mg]) => (
                  <tr key={p}>
                    <td style={{ textAlign: "left" }}>{p}</td>
                    <td className={user.role === "admin" ? "" : "muted-text"}>
                      {ad}
                    </td>
                    <td className={user.role === "manager" ? "" : "muted-text"}>
                      {mg}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        {user.role === "manager" && (
          <ManagerScopeEditor
            draft={scopeDraft}
            scope={scope}
            draftClients={draftClients}
            error={scopeErr}
            onToast={onToast}
            onNetworks={(nets) =>
              setScopeDraft((d) => (d ? { ...d, networks: nets } : d))
            }
            onClients={(ids) =>
              setScopeDraft((d) => (d ? { ...d, clientIDs: ids } : d))
            }
            onSave={saveScope}
          />
        )}

        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Смена пароля</span>
          </div>
          <div className="card-body">
            <Label>
              Новый пароль
              <Input
                type="password"
                value={pwd}
                autoComplete="new-password"
                onChange={(e) => setPwd(e.target.value)}
              />
            </Label>
            <div className="row" style={{ marginTop: 8 }}>
              <Button size="sm" onClick={savePassword} disabled={!pwd}>
                <KeyRound aria-hidden /> Сменить пароль
              </Button>
              <span className="muted-text small">
                Хранится только как Argon2id; смена отзывает все сессии.
              </span>
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Блокировка</span>
          </div>
          <div className="card-body">
            <Button
              variant={user.enabled ? "outline" : "default"}
              size="sm"
              disabled={!disable}
              title={
                !disable
                  ? "Нельзя заблокировать последнего активного администратора"
                  : undefined
              }
              onClick={() =>
                patch(
                  { enabled: !user.enabled },
                  user.enabled
                    ? "Пользователь заблокирован"
                    : "Пользователь разблокирован",
                )
              }
            >
              {user.enabled ? (
                <>
                  <Ban aria-hidden /> Заблокировать
                </>
              ) : (
                "Разблокировать"
              )}
            </Button>
          </div>
        </div>
      </div>
    </>
  );
}

function ManagerScopeEditor({
  draft,
  scope,
  draftClients,
  error,
  onToast,
  onNetworks,
  onClients,
  onSave,
}: {
  draft: { networks: string[]; clientIDs: string[] } | null;
  scope: ScopeBody | null;
  draftClients: DraftRow[];
  error: string | null;
  onToast: (m: string) => void;
  onNetworks: (n: string[]) => void;
  onClients: (ids: string[]) => void;
  onSave: () => void;
}) {
  const [netInput, setNetInput] = useState("");
  if (!draft) {
    return error ? (
      <div className="card">
        <div className="card-body">
          <div className="callout callout-bad">
            <Ban aria-hidden />
            <div className="small">{error}</div>
          </div>
        </div>
      </div>
    ) : (
      <div className="card">
        <div className="card-body muted-text small">Загрузка области…</div>
      </div>
    );
  }

  const dirty =
    JSON.stringify({
      networks: draft.networks,
      client_ids: draft.clientIDs,
    }) !== JSON.stringify(scope);

  function addNet() {
    const n = parseNetwork(netInput);
    if (!n) {
      onToast("Это не похоже на CIDR");
      return;
    }
    if (!draft!.networks.includes(n)) onNetworks([...draft!.networks, n]);
    setNetInput("");
  }

  // Toggle a client assignment from the draft list.
  function toggleClient(id: string) {
    if (draft!.clientIDs.includes(id)) {
      onClients(draft!.clientIDs.filter((x) => x !== id));
    } else {
      onClients([...draft!.clientIDs, id]);
    }
  }

  return (
    <div className="card">
      <div className="card-head">
        <span className="card-title grow">Область менеджера</span>
        {dirty && <Badge variant="secondary">в черновике</Badge>}
      </div>
      <div className="card-body">
        <div className="small muted-text" style={{ marginBottom: 6 }}>
          Сети для новых клиентов (CIDR)
        </div>
        <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
          {draft.networks.map((n) => (
            <span className="chip" key={n}>
              {n}
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={`Удалить ${n}`}
                onClick={() =>
                  onNetworks(draft.networks.filter((x) => x !== n))
                }
              >
                <X aria-hidden />
              </Button>
            </span>
          ))}
          {draft.networks.length === 0 && (
            <span className="muted-text small">
              Не заданы: создавать клиентов нельзя
            </span>
          )}
        </div>
        <div className="row" style={{ marginTop: 8 }}>
          <Input
            className="mono"
            placeholder="10.20.0.0/22"
            value={netInput}
            onChange={(e) => setNetInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addNet();
              }
            }}
          />
          <Button size="sm" variant="outline" onClick={addNet}>
            <Plus aria-hidden /> Сеть
          </Button>
        </div>

        <div
          className="small muted-text"
          style={{ marginTop: 14, marginBottom: 6 }}
        >
          Назначенные клиенты
        </div>
        {draftClients.length === 0 ? (
          <div className="muted-text small">В черновике нет клиентов</div>
        ) : (
          <div className="tw">
            <table style={{ width: "100%" }}>
              <tbody>
                {draftClients.map((c) => {
                  const checked = draft.clientIDs.includes(c.id);
                  return (
                    <tr key={c.id}>
                      <td style={{ padding: "5px 8px" }}>
                        <button
                          type="button"
                          className="row"
                          style={{ gap: 8, textAlign: "left" }}
                          onClick={() => toggleClient(c.id)}
                          aria-pressed={checked}
                        >
                          <span
                            className={"cbx" + (checked ? " on" : "")}
                            aria-hidden
                          />
                          <span>{c.name}</span>
                          <span className="mono muted-text small">{c.id}</span>
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {error && (
          <div className="callout callout-bad" style={{ marginTop: 8 }}>
            <Ban aria-hidden />
            <div className="small">{error}</div>
          </div>
        )}
        <div className="row" style={{ marginTop: 10 }}>
          <Button size="sm" onClick={onSave} disabled={!dirty}>
            Сохранить область
          </Button>
        </div>
      </div>
    </div>
  );
}

function ProxyAccountDetail({
  account,
  draftClients,
  session,
  request,
  onToast,
  onMutated,
  error,
  setError,
}: {
  account: ProxyAccount;
  draftClients: DraftRow[];
  session: SectionProps["session"];
  request: SectionProps["request"];
  onToast: (m: string) => void;
  onMutated: () => Promise<void>;
  error: string | null;
  setError: (s: string | null) => void;
}) {
  const usedBy = draftClients.filter(
    (c) => c.proxy_username === account.username,
  );
  const owner = account.owner ?? "";
  const ownerIsSelf = session.role === "manager" && owner === session.username;
  const [pwd, setPwd] = useState("");

  async function patch(payload: Record<string, unknown>, ok: string) {
    setError(null);
    try {
      await request(`/proxy-accounts/${account.username}`, "PATCH", payload);
      onToast(ok);
      await onMutated();
      setPwd("");
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <>
      <header className="inspector-head">
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <h1 className="page-title mono" style={{ margin: 0 }}>
            {account.username}
          </h1>
          <Badge variant="secondary">логин прокси</Badge>
          {ownerIsSelf && <Badge variant="secondary">создан вами</Badge>}
          {account.enabled ? (
            <Badge className="badge-ok">активен</Badge>
          ) : (
            <Badge variant="secondary">выкл</Badge>
          )}
          <span className="grow" />
        </div>
      </header>
      <div className="inspector-body">
        {error && (
          <div className="callout callout-bad">
            <Ban aria-hidden />
            <div className="small">{error}</div>
          </div>
        )}
        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Где используется</span>
          </div>
          <div className="card-body">
            {usedBy.length > 0 ? (
              <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
                {usedBy.map((c) => (
                  <Badge key={c.id} variant="outline">
                    {c.name}
                  </Badge>
                ))}
              </div>
            ) : (
              <div className="muted-text small">
                Ни к одному клиенту не привязан.
              </div>
            )}
            <div className="muted-text small" style={{ marginTop: 8 }}>
              Логин подтверждает, кто подключился. Что разрешено, решает
              политика клиента.
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Владелец</span>
          </div>
          <div className="card-body">
            <span className="mono">{owner || "admin"}</span>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Смена пароля</span>
          </div>
          <div className="card-body">
            <Label>
              Новый пароль
              <Input
                type="password"
                value={pwd}
                autoComplete="new-password"
                onChange={(e) => setPwd(e.target.value)}
              />
            </Label>
            <div className="row" style={{ marginTop: 8 }}>
              <Button
                size="sm"
                onClick={() => patch({ password: pwd }, "Пароль изменён")}
                disabled={!pwd}
              >
                <KeyRound aria-hidden /> Сменить пароль
              </Button>
              <span className="muted-text small">
                Смена сразу закрывает старый пароль у прокси.
              </span>
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Состояние</span>
          </div>
          <div className="card-body">
            <Button
              variant={account.enabled ? "outline" : "default"}
              size="sm"
              onClick={() =>
                patch(
                  { enabled: !account.enabled },
                  account.enabled ? "Логин отключён" : "Логин включён",
                )
              }
            >
              {account.enabled ? "Отключить" : "Включить"}
            </Button>
          </div>
        </div>
      </div>
    </>
  );
}

function NewAccountForm({
  kind,
  onCancel,
  onCreate,
  error,
}: {
  kind: "panel" | "proxy";
  onCancel: () => void;
  onCreate: (
    username: string,
    password: string,
    role: "admin" | "manager",
  ) => Promise<boolean>;
  error: string | null;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<"admin" | "manager">("manager");
  return (
    <section className="inspector-pane">
      <header className="inspector-head">
        <h1 className="page-title" style={{ margin: 0 }}>
          {kind === "panel" ? "Новый пользователь" : "Новый логин прокси"}
        </h1>
      </header>
      <div className="inspector-body">
        {error && (
          <div className="callout callout-bad">
            <Ban aria-hidden />
            <div className="small">{error}</div>
          </div>
        )}
        <div className="card">
          <div className="card-body">
            <Label>
              {kind === "panel" ? "Имя пользователя" : "Логин"}
              <Input
                className="mono"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="латинские буквы, цифры, точка, дефис"
                autoFocus
              />
            </Label>
            <Label style={{ marginTop: 8 }}>
              Пароль
              <Input
                type="password"
                value={password}
                autoComplete="new-password"
                onChange={(e) => setPassword(e.target.value)}
              />
            </Label>
            {kind === "panel" && (
              <div className="opts" style={{ marginTop: 8 }}>
                <button
                  type="button"
                  className={"opt" + (role === "admin" ? " selected" : "")}
                  aria-pressed={role === "admin"}
                  onClick={() => setRole("admin")}
                >
                  <span className="radio" aria-hidden />
                  <span>
                    <div style={{ fontWeight: 500 }}>Администратор</div>
                  </span>
                </button>
                <button
                  type="button"
                  className={"opt" + (role === "manager" ? " selected" : "")}
                  aria-pressed={role === "manager"}
                  onClick={() => setRole("manager")}
                >
                  <span className="radio" aria-hidden />
                  <span>
                    <div style={{ fontWeight: 500 }}>Менеджер</div>
                  </span>
                </button>
              </div>
            )}
            <div
              className="row"
              style={{ justifyContent: "flex-end", marginTop: 12 }}
            >
              <Button variant="ghost" onClick={onCancel}>
                Отмена
              </Button>
              <Button onClick={() => onCreate(username, password, role)}>
                <Plus aria-hidden /> Создать
              </Button>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

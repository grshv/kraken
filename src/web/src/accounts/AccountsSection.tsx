import { useEffect, useRef, useState } from "react";
import { Ban, Check, KeyRound, Plus, X } from "lucide-react";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  InspectorBody,
  InspectorHeader,
  InspectorPane,
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListGroupLabel,
  ListRow,
} from "@/components/kraken/layout";
import { Callout, Chip } from "@/components/kraken/blocks";
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

  // On first load, default to the current user when they're in the visible
  // list (prototype: S.acc='admin'); managers fall back to the first proxy
  // login since they don't see panel users.
  useEffect(() => {
    if (selected !== null) return;
    if (canManageUsers && users && users.length) {
      const me = users.find((u) => u.username === session.username);
      setSelected(me ? me.username : users[0].username);
    } else if (accounts && accounts.length) {
      const me = accounts.find((a) => a.username === session.username);
      setSelected(me ? me.username : accounts[0].username);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canManageUsers, users, accounts, session.username, selected]);

  // Fatal surface only when a required list failed to load.
  const fatal = (canManageUsers && users === null) || accounts === null;

  if (fatal && !creating)
    return (
      <InspectorPane>
        <InspectorBody>
          <ErrorState
            title="Аккаунты недоступны"
            detail="Нет связи с панелью. Повторите запрос."
            onRetry={() => location.reload()}
          />
        </InspectorBody>
      </InspectorPane>
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
    <>
      <ListPane>
        <ListPaneHeader
          title="Аккаунты"
          description={
            isAdmin
              ? "Кто входит в панель и какие логины принимает прокси."
              : "Логины для ваших клиентов, подключённых по TLS."
          }
          action={
            canManageUsers ? (
              <Button size="xs" onClick={() => setCreating("panel")}>
                <Plus aria-hidden /> Пользователь
              </Button>
            ) : (
              <Button size="xs" onClick={() => setCreating("proxy")}>
                <Plus aria-hidden /> Логин
              </Button>
            )
          }
        />
        <ListPaneBody>
          {canManageUsers && (
            <>
              <ListGroupLabel>Панель управления</ListGroupLabel>
              {panelUsers.map((u) => (
                <ListRow
                  key={u.username}
                  selected={selected === u.username}
                  onClick={() => setSelected(u.username)}
                  title={
                    <span className="truncate font-medium">{u.username}</span>
                  }
                  subtitle={
                    u.role === "admin" ? "Администратор" : "Менеджер"
                  }
                  aside={
                    u.enabled ? undefined : (
                      <Badge variant="outline">заблокирован</Badge>
                    )
                  }
                />
              ))}
              {panelUsers.length === 0 && (
                <div className="px-3.5 py-2 text-sm text-muted-foreground">
                  Нет пользователей
                </div>
              )}
            </>
          )}
          <ListGroupLabel>Логины прокси</ListGroupLabel>
          {proxyRows.map((a) => {
            // Where used: draft clients that reference this proxy login.
            const usedBy = draftRows.filter(
              (c) => c.proxy_username === a.username,
            );
            return (
              <ListRow
                key={a.username}
                selected={selected === a.username}
                onClick={() => setSelected(a.username)}
                title={
                  <span className="truncate font-mono">{a.username}</span>
                }
                subtitle={
                  usedBy.length
                    ? usedBy.map((c) => c.name).join(", ")
                    : "не используется"
                }
                aside={
                  a.enabled ? undefined : (
                    <Badge variant="outline">заблокирован</Badge>
                  )
                }
              />
            );
          })}
          {proxyRows.length === 0 && (
            <div className="px-3.5 py-2 text-sm text-muted-foreground">
              Нет логинов прокси
            </div>
          )}
        </ListPaneBody>
      </ListPane>
      <InspectorPane>
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
          <InspectorBody>
            <EmptyState icon={<KeyRound />} title="Выберите аккаунт">
              {canManageUsers
                ? "Выберите пользователя панели или логин прокси слева."
                : "Выберите логин прокси слева или создайте новый."}
            </EmptyState>
          </InspectorBody>
        )}
      </InspectorPane>
    </>
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
    <InspectorBody>
      <EmptyState icon={<KeyRound />} title="Не найдено">
        Аккаунт не существует или вне вашей области.
      </EmptyState>
    </InspectorBody>
  );
}

// RoleOption keeps the original button[aria-pressed] a11y pattern from the
// prototype's .opt, restyled with Tailwind; the radio dot is a plain span so
// roles and aria-pressed are unchanged.
function RoleOption({
  selected,
  disabled,
  title,
  onClick,
  label,
  hint,
}: {
  selected: boolean;
  disabled?: boolean;
  title?: string;
  onClick: () => void;
  label: string;
  hint?: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      disabled={disabled}
      title={title}
      onClick={onClick}
      className={cn(
        "grid grid-cols-[18px_minmax(0,1fr)] items-start gap-2.5 rounded-lg border p-3 text-left disabled:cursor-not-allowed disabled:opacity-60",
        selected
          ? "border-foreground shadow-[0_0_0_1px_var(--foreground)]"
          : "border-border",
      )}
    >
      <span
        aria-hidden
        className="mt-px grid size-4 place-items-center rounded-full border border-input"
      >
        {selected && <span className="size-2 rounded-full bg-foreground" />}
      </span>
      <span>
        <div className="font-medium">{label}</div>
        {hint && (
          <div className="text-sm text-muted-foreground">{hint}</div>
        )}
      </span>
    </button>
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
  const pwdRef = useRef<HTMLInputElement>(null);
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
      <InspectorHeader
        title={user.username}
        status={
          <>
            {self && <Badge variant="secondary">это вы</Badge>}
            <Badge variant="secondary">
              {user.role === "admin" ? "Администратор" : "Менеджер"}
            </Badge>
            {user.enabled ? (
              <Badge variant="ok">активен</Badge>
            ) : (
              <Badge variant="secondary">заблокирован</Badge>
            )}
          </>
        }
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => pwdRef.current?.focus()}
          >
            <KeyRound aria-hidden /> Сменить пароль
          </Button>
        }
      />
      <InspectorBody>
        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <Card>
          <CardHeader>
            <CardTitle>Роль</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-2 max-[1180px]:grid-cols-1">
              <RoleOption
                selected={user.role === "admin"}
                disabled={user.role === "admin"}
                onClick={() => patch({ role: "admin" }, "Роль изменена")}
                label="Администратор"
                hint="Всё, включая настройки и аккаунты"
              />
              <RoleOption
                selected={user.role === "manager"}
                disabled={user.role === "admin" && !demote}
                title={
                  user.role === "admin" && !demote
                    ? "Нельзя понизить последнего администратора"
                    : undefined
                }
                onClick={() => patch({ role: "manager" }, "Роль изменена")}
                label="Менеджер"
                hint="Только назначенные клиенты"
              />
            </div>
            <Table className="mt-2.5">
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[38%]">Право</TableHead>
                  <TableHead className="w-[18%]">Администратор</TableHead>
                  <TableHead>Менеджер</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {PERMS.map(([p, ad, mg]) => (
                  <TableRow key={p}>
                    <TableCell className="w-[38%]">{p}</TableCell>
                    <TableCell
                      className={
                        "w-[18%] " +
                        (user.role === "admin" ? "" : "text-muted-foreground")
                      }
                    >
                      {ad}
                    </TableCell>
                    <TableCell
                      className={
                        user.role === "manager" ? "" : "text-muted-foreground"
                      }
                    >
                      {mg}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

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

        <Card>
          <CardHeader>
            <CardTitle>Смена пароля</CardTitle>
          </CardHeader>
          <CardContent>
            <Label>
              Новый пароль
              <Input
                ref={pwdRef}
                type="password"
                value={pwd}
                autoComplete="new-password"
                onChange={(e) => setPwd(e.target.value)}
              />
            </Label>
            <div className="mt-2 flex items-center gap-2">
              <Button size="sm" onClick={savePassword} disabled={!pwd}>
                <KeyRound aria-hidden /> Сменить пароль
              </Button>
              <span className="text-sm text-muted-foreground">
                Хранится только как Argon2id; смена отзывает все сессии.
              </span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Блокировка</CardTitle>
          </CardHeader>
          <CardContent>
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
          </CardContent>
        </Card>
      </InspectorBody>
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
      <Card>
        <CardContent>
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        </CardContent>
      </Card>
    ) : (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          Загрузка области…
        </CardContent>
      </Card>
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
    <Card>
      <CardHeader>
        <CardTitle>Область менеджера</CardTitle>
        {dirty && (
          <CardAction>
            <Badge variant="secondary">в черновике</Badge>
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        <div className="mb-1.5 text-sm text-muted-foreground">
          Сети для новых клиентов (CIDR)
        </div>
        <div className="flex flex-wrap gap-1.5">
          {draft.networks.map((n) => (
            <Chip key={n}>
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
            </Chip>
          ))}
          {draft.networks.length === 0 && (
            <span className="text-sm text-muted-foreground">
              Не заданы: создавать клиентов нельзя
            </span>
          )}
        </div>
        <div className="mt-2 flex items-center gap-2">
          <Input
            className="font-mono"
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

        <div className="mt-3.5 mb-1.5 text-sm text-muted-foreground">
          Назначенные клиенты
        </div>
        {draftClients.length === 0 ? (
          <div className="text-sm text-muted-foreground">
            В черновике нет клиентов
          </div>
        ) : (
          <Table>
            <TableBody>
              {draftClients.map((c) => {
                const checked = draft.clientIDs.includes(c.id);
                return (
                  <TableRow key={c.id}>
                    <TableCell className="py-1.5 px-2">
                      <button
                        type="button"
                        aria-pressed={checked}
                        onClick={() => toggleClient(c.id)}
                        className="flex items-center gap-2 text-left"
                      >
                        <span
                          aria-hidden
                          className={cn(
                            "grid size-3.5 shrink-0 place-content-center rounded-sm border",
                            checked
                              ? "border-primary bg-primary text-primary-foreground"
                              : "border-input",
                          )}
                        >
                          {checked && <Check className="size-3" />}
                        </span>
                        <span>{c.name}</span>
                        <span className="font-mono text-sm text-muted-foreground">
                          {c.id}
                        </span>
                      </button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        {error && (
          <Callout
            tone="bad"
            icon={<Ban aria-hidden />}
            className="mt-2"
          >
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <div className="mt-2.5 flex items-center gap-2">
          <Button size="sm" onClick={onSave} disabled={!dirty}>
            Сохранить область
          </Button>
        </div>
      </CardContent>
    </Card>
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
  const pwdRef = useRef<HTMLInputElement>(null);

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
      <InspectorHeader
        title={<span className="font-mono">{account.username}</span>}
        status={
          <>
            <Badge variant="secondary">логин прокси</Badge>
            {ownerIsSelf && (
              <Badge variant="secondary">создан вами</Badge>
            )}
            {account.enabled ? (
              <Badge variant="ok">активен</Badge>
            ) : (
              <Badge variant="secondary">выкл</Badge>
            )}
          </>
        }
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => pwdRef.current?.focus()}
          >
            <KeyRound aria-hidden /> Сменить пароль
          </Button>
        }
      />
      <InspectorBody>
        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <Card>
          <CardHeader>
            <CardTitle>Где используется</CardTitle>
          </CardHeader>
          <CardContent>
            {usedBy.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {usedBy.map((c) => (
                  <Badge key={c.id} variant="outline">
                    {c.name}
                  </Badge>
                ))}
              </div>
            ) : (
              <div className="text-sm text-muted-foreground">
                Ни к одному клиенту не привязан.
              </div>
            )}
            <div className="mt-2 text-sm text-muted-foreground">
              Логин подтверждает, кто подключился. Что разрешено, решает
              политика клиента.
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Владелец</CardTitle>
          </CardHeader>
          <CardContent>
            <span className="font-mono text-sm">{owner || "admin"}</span>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Смена пароля</CardTitle>
          </CardHeader>
          <CardContent>
            <Label>
              Новый пароль
              <Input
                ref={pwdRef}
                type="password"
                value={pwd}
                autoComplete="new-password"
                onChange={(e) => setPwd(e.target.value)}
              />
            </Label>
            <div className="mt-2 flex items-center gap-2">
              <Button
                size="sm"
                onClick={() => patch({ password: pwd }, "Пароль изменён")}
                disabled={!pwd}
              >
                <KeyRound aria-hidden /> Сменить пароль
              </Button>
              <span className="text-sm text-muted-foreground">
                Смена сразу закрывает старый пароль у прокси.
              </span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Состояние</CardTitle>
          </CardHeader>
          <CardContent>
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
          </CardContent>
        </Card>
      </InspectorBody>
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
    <InspectorPane>
      <InspectorHeader
        title={kind === "panel" ? "Новый пользователь" : "Новый логин прокси"}
      />
      <InspectorBody>
        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <Card>
          <CardContent>
            <Label>
              {kind === "panel" ? "Имя пользователя" : "Логин"}
              <Input
                className="font-mono"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="латинские буквы, цифры, точка, дефис"
                autoFocus
              />
            </Label>
            <Label className="mt-2">
              Пароль
              <Input
                type="password"
                value={password}
                autoComplete="new-password"
                onChange={(e) => setPassword(e.target.value)}
              />
            </Label>
            {kind === "panel" && (
              <div className="mt-2 grid grid-cols-2 gap-2 max-[1180px]:grid-cols-1">
                <RoleOption
                  selected={role === "admin"}
                  onClick={() => setRole("admin")}
                  label="Администратор"
                />
                <RoleOption
                  selected={role === "manager"}
                  onClick={() => setRole("manager")}
                  label="Менеджер"
                />
              </div>
            )}
            <div className="mt-3 flex justify-end gap-2">
              <Button variant="ghost" onClick={onCancel}>
                Отмена
              </Button>
              <Button onClick={() => onCreate(username, password, role)}>
                <Plus aria-hidden /> Создать
              </Button>
            </div>
          </CardContent>
        </Card>
      </InspectorBody>
    </InspectorPane>
  );
}

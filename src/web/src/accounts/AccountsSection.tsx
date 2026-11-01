import { Disclosure } from "@/components/kraken/disclosure";
import { Timestamp } from "@/components/kraken/timestamp";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Checkbox } from "@/components/ui/checkbox";
import { useEffect, useState } from "react";
import { Ban, KeyRound, Plus, X } from "lucide-react";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
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
import { Callout, Chip, KeyValue } from "@/components/kraken/blocks";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import {
  buildManagerScopeBody,
  canDemote,
  canDisable,
  type PanelUser,
} from "./rules";
import { parseNetwork } from "../clients/networks";
import { SessionsCard } from "./SessionsCard";
import { RecentActions } from "./RecentActions";
import { ProxyKpis, ProxySources, useProxyActivity } from "./ProxyActivity";
import type { SectionProps } from "../sections/sectionProps";
import type { NavId } from "../shell/nav";
import { releasePasswordFocus } from "../shell/passwordFocus";

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
  const [users, setUsers] = useState<User[] | null | undefined>(undefined);
  const [accounts, setAccounts] = useState<ProxyAccount[] | null | undefined>(
    undefined,
  );
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState<"panel" | "proxy" | null>(null);

  const canManageUsers = !!session.permissions?.includes("users");
  const isAdmin = session.role === "admin";
  const snap = draft?.snapshot;
  const clients = snap?.clients ?? [];

  // Command palette "new-proxy-login" opens the proxy login form once;
  // "account:<username>" (the profile menu) selects that account.
  useEffect(() => {
    if (intent === "new-proxy-login") {
      setCreating("proxy");
      onIntentHandled?.();
    } else if (intent?.startsWith("account:")) {
      setSelected(intent.slice("account:".length));
      setCreating(null);
      onIntentHandled?.();
    }
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

  // Fatal surface only when a required list failed to load (null); undefined
  // is still loading and must not flash the error.
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
          action={
            canManageUsers ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button size="xs">
                    <Plus aria-hidden /> Создать
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => setCreating("panel")}>
                    Пользователь панели
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setCreating("proxy")}>
                    Логин прокси
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
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
                  subtitle={u.role === "admin" ? "Администратор" : "Менеджер"}
                  aside={
                    u.enabled ? undefined : (
                      <span
                        className="inline-flex align-middle text-muted-foreground"
                        title="Заблокирован"
                      >
                        <Ban
                          className="size-3.5"
                          role="img"
                          aria-label="Заблокирован"
                        />
                      </span>
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
                title={<span className="truncate font-mono">{a.username}</span>}
                subtitle={
                  usedBy.length
                    ? usedBy.map((c) => c.name).join(", ")
                    : "не используется"
                }
                aside={
                  a.enabled ? undefined : (
                    <Badge variant="bad">
                      <Ban aria-hidden />
                      заблокирован
                    </Badge>
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
  onNavigate,
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
        onNavigate={onNavigate}
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

// A role choice composes the shared shadcn RadioGroupItem and Label.
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
    <Label
      title={title}
      className={cn(
        "grid grid-cols-[18px_minmax(0,1fr)] items-start gap-2.5 rounded-lg border p-3 text-left",
        selected ? "border-input bg-sel" : "border-border bg-surface-control",
        disabled && "opacity-60",
      )}
    >
      <RadioGroupItem
        value={label === "Администратор" ? "admin" : "manager"}
        disabled={disabled}
        onClick={onClick}
        className="mt-px"
      />
      <span>
        <span className="block font-medium">{label}</span>
        {hint && (
          <span className="block text-sm text-muted-foreground">{hint}</span>
        )}
      </span>
    </Label>
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
  onNavigate,
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
  onNavigate: (id: NavId) => void;
  error: string | null;
  setError: (s: string | null) => void;
}) {
  const self = session.username === user.username;
  const [pwd, setPwd] = useState("");
  const [pwdOpen, setPwdOpen] = useState(false);
  const logins = useLoginStats(request, user.username);
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
    releasePasswordFocus();
    setPwd("");
    setPwdOpen(false);
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
              <Badge variant="bad">
                <Ban aria-hidden />
                заблокирован
              </Badge>
            )}
          </>
        }
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPwdOpen(true)}
            >
              <KeyRound aria-hidden /> Сбросить пароль
            </Button>
            {!self && (
              <Button
                variant="outline"
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
                {user.enabled ? "Заблокировать" : "Разблокировать"}
              </Button>
            )}
          </>
        }
      />
      <Dialog open={pwdOpen} onOpenChange={setPwdOpen}>
        <DialogContent className="sm:max-w-[420px]">
          <DialogHeader>
            <DialogTitle>Сброс пароля: {user.username}</DialogTitle>
            <DialogDescription>
              Все сессии пользователя завершатся. Для смены своего пароля с
              сохранением текущей сессии используйте меню профиля.
            </DialogDescription>
          </DialogHeader>
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              void savePassword();
            }}
          >
            <Input
              type="password"
              aria-label="Новый пароль"
              autoComplete="new-password"
              autoFocus
              value={pwd}
              onChange={(e) => setPwd(e.target.value)}
            />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setPwdOpen(false)}
              >
                Отмена
              </Button>
              <Button type="submit" disabled={!pwd}>
                Сбросить пароль
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <InspectorBody>
        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <div className="grid grid-cols-1 items-start gap-6 @[900px]:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-6">
            <Card>
              <CardHeader>
                <div>
                  <CardTitle>Роль</CardTitle>
                  {user.role === "admin" && !demote && (
                    <CardDescription>
                      Последнего администратора нельзя понизить или
                      заблокировать
                    </CardDescription>
                  )}
                </div>
              </CardHeader>
              <CardContent>
                <RadioGroup
                  value={user.role}
                  className="grid grid-cols-2 gap-2"
                >
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
                </RadioGroup>
                <Disclosure className="mt-3 text-sm" label="Полномочия роли">
                  <dl className="mt-2 flex flex-col text-sm">
                    {PERMS.map(([p, ad, mg]) => (
                      <div
                        key={p}
                        className="flex items-baseline justify-between gap-4 border-b py-1.5 last:border-b-0"
                      >
                        <dt className="text-muted-foreground">{p}</dt>
                        <dd className="m-0 text-right">
                          {user.role === "admin" ? ad : mg}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </Disclosure>
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
          </div>
          <div className="flex min-w-0 flex-col gap-6">
            <Card>
              <CardHeader>
                <CardTitle>Входы</CardTitle>
              </CardHeader>
              <CardContent>
                <KeyValue
                  items={[
                    [
                      "Последний",
                      <Timestamp value={logins.last} seconds={false} compact />,
                    ],
                    [
                      "Неудачных за 24 ч",
                      <span className={cn(logins.failed24h > 0 && "text-bad")}>
                        {logins.failed24h}
                      </span>,
                    ],
                  ]}
                />
                <Disclosure
                  className="mt-3 text-sm text-muted-foreground"
                  label="Ограничения входа"
                >
                  <p className="mt-2">
                    Больше 5 попыток в минуту с адреса или 20 для имени — вход
                    закрыт на минуту.
                  </p>
                </Disclosure>
              </CardContent>
            </Card>
            <RecentActions
              username={user.username}
              request={request}
              onOpenAudit={() => onNavigate("audit")}
            />
          </div>
        </div>

        <SessionsCard
          username={user.username}
          request={request}
          onToast={onToast}
        />
      </InspectorBody>
    </>
  );
}

// useLoginStats reads the panel user's last successful login and failed
// logins over 24 h from the audit (login / login_failed rows by actor).
function useLoginStats(request: SectionProps["request"], username: string) {
  const [stats, setStats] = useState<{
    last: string | null;
    failed24h: number;
  }>({ last: null, failed24h: 0 });
  useEffect(() => {
    let live = true;
    const q = (action: string) =>
      request<{ items: { timestamp: string }[] }>(
        `/audit?actor=${encodeURIComponent(username)}&action=${action}&limit=200`,
      ).catch(() => ({ items: [] as { timestamp: string }[] }));
    Promise.all([q("login"), q("login_failed")]).then(([ok, bad]) => {
      if (!live) return;
      const since = Date.now() - 24 * 3600 * 1000;
      setStats({
        last: ok.items[0]?.timestamp ?? null,
        failed24h: bad.items.filter((x) => Date.parse(x.timestamp) >= since)
          .length,
      });
    });
    return () => {
      live = false;
    };
  }, [request, username]);
  return stats;
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
            <Badge variant="secondary">Не сохранено</Badge>
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
                      <Label className="flex items-center gap-2 text-left">
                        <Checkbox
                          checked={checked}
                          onCheckedChange={() => toggleClient(c.id)}
                          aria-label={"Назначить " + c.name}
                        />

                        <span>{c.name}</span>
                      </Label>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />} className="mt-2">
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
  const activity = useProxyActivity(request, account.username);
  const [pwdOpen, setPwdOpen] = useState(false);

  async function patch(payload: Record<string, unknown>, ok: string) {
    setError(null);
    try {
      await request(`/proxy-accounts/${account.username}`, "PATCH", payload);
      onToast(ok);
      await onMutated();
      setPwd("");
      return true;
    } catch (e) {
      setError((e as Error).message);
      return false;
    }
  }

  return (
    <>
      <InspectorHeader
        title={<span className="font-mono">{account.username}</span>}
        meta="Изменения логина действуют сразу"
        status={
          <>
            <Badge variant="secondary">логин прокси</Badge>
            {ownerIsSelf && <Badge variant="secondary">создан вами</Badge>}
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
            onClick={() => {
              setError(null);
              setPwdOpen(true);
            }}
          >
            <KeyRound aria-hidden /> Сменить пароль
          </Button>
        }
      />
      <Dialog
        open={pwdOpen}
        onOpenChange={(open) => {
          setPwdOpen(open);
          if (!open) setPwd("");
        }}
      >
        <DialogContent className="sm:max-w-[420px]">
          <DialogHeader>
            <DialogTitle>Пароль прокси: {account.username}</DialogTitle>
            <DialogDescription>
              Новый пароль заменит прежний сразу. Открытые туннели сохранятся.
            </DialogDescription>
          </DialogHeader>
          <form
            className="flex flex-col gap-4"
            onSubmit={async (e) => {
              e.preventDefault();
              if (
                pwd &&
                (await patch({ password: pwd }, "Пароль прокси изменён"))
              ) {
                releasePasswordFocus();
                setPwdOpen(false);
              }
            }}
          >
            <Label htmlFor="proxy-new-password">Новый пароль</Label>
            <Input
              id="proxy-new-password"
              type="password"
              autoComplete="new-password"
              autoFocus
              value={pwd}
              onChange={(e) => setPwd(e.target.value)}
              aria-invalid={!!error}
            />
            {error && <Callout tone="bad">{error}</Callout>}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setPwdOpen(false);
                  setPwd("");
                }}
              >
                Отмена
              </Button>
              <Button type="submit" disabled={!pwd}>
                Сменить пароль
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <InspectorBody>
        {error && (
          <Callout tone="bad" icon={<Ban aria-hidden />}>
            <span className="text-sm">{error}</span>
          </Callout>
        )}
        <ProxyKpis activity={activity} />
        <ProxySources activity={activity} />
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

export function NewAccountForm({
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
              <RadioGroup
                aria-label="Роль пользователя"
                value={role}
                onValueChange={(value) => setRole(value as "admin" | "manager")}
                className="mt-2 grid grid-cols-2 gap-2 max-[1180px]:grid-cols-1"
              >
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
              </RadioGroup>
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

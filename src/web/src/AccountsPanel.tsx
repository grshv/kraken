import { Label } from "@/components/ui/label";
import { Alert } from "@/components/ui/alert";
import { Panel } from "./components/Panel";
import { RefreshCw } from "lucide-react";
import { EntityDialog } from "./components/EntityDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectField } from "./components/SelectField";
import { SelectItem } from "@/components/ui/select";
import { Table } from "@/components/ui/table";
import { TableHeader } from "@/components/ui/table";
import { TableBody } from "@/components/ui/table";
import { TableRow } from "@/components/ui/table";
import { TableHead } from "@/components/ui/table";
import { TableCell } from "@/components/ui/table";
import { useEffect, useRef, useState } from "react";
import type { ApiRequest, PanelRole } from "./model";

type Account = { username: string; enabled: boolean; role?: PanelRole };
export const roleNames: Record<PanelRole, string> = {
  admin: "Администратор",
  manager: "Менеджер",
};

export function AccountsPanel({
  kind,
  request,
  currentUser,
}: {
  kind: "users" | "proxy-accounts";
  request: ApiRequest;
  currentUser: string | null;
}) {
  const api = useRef(request);
  api.current = request;
  const [items, setItems] = useState<Account[]>([]);
  const [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [open, setOpen] = useState(false),
    [editing, setEditing] = useState<string | null>(null);
  const [username, setUsername] = useState(""),
    [password, setPassword] = useState(""),
    [role, setRole] = useState<PanelRole>("manager");
  const unchanged =
    editing !== null &&
    password === "" &&
    (kind === "proxy-accounts" ||
      role === items.find((account) => account.username === editing)?.role);
  async function reload() {
    const result = await api.current<{ items: Account[] }>(`/${kind}`);
    setItems(result.items);
    setError("");
  }
  async function refreshList() {
    setLoading(true);
    try {
      await reload();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    let active = true;
    setLoading(true);
    reload()
      .catch((e) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [kind]);
  function closeEditor() {
    if (busy) return;
    setOpen(false);
    setPassword("");
    setError("");
  }
  function edit(account?: Account) {
    setEditing(account?.username ?? null);
    setUsername(account?.username ?? "");
    setPassword("");
    setRole(account?.role ?? "manager");
    setOpen(true);
    setError("");
  }
  async function submit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy || unchanged) return;
    setBusy(true);
    setError("");
    try {
      const body = editing
        ? {
            ...(kind === "users" ? { role } : {}),
            ...(password ? { password } : {}),
          }
        : { username, password, ...(kind === "users" ? { role } : {}) };
      await api.current(
        `/${kind}${editing ? `/${encodeURIComponent(editing)}` : ""}`,
        editing ? "PATCH" : "POST",
        body,
      );
      setPassword("");
      setOpen(false);
      if (kind === "users" && editing === currentUser) {
        location.reload();
        return;
      }
      await reload();
    } catch (e) {
      setError((e as Error).message);
      setPassword("");
    } finally {
      setBusy(false);
    }
  }
  async function toggle(account: Account) {
    setBusy(true);
    setError("");
    try {
      await api.current(
        `/${kind}/${encodeURIComponent(account.username)}`,
        "PATCH",
        { enabled: !account.enabled },
      );
      if (kind === "users" && account.username === currentUser) {
        location.reload();
        return;
      }
      await reload();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Panel
      className="section accounts"
      aria-label={kind === "users" ? "Пользователи панели" : "Логины прокси"}
    >
      <div className="section-head">
        <h2>{kind === "users" ? "Пользователи панели" : "Логины прокси"}</h2>
        <div className="actions">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Обновить список"
            onClick={refreshList}
            disabled={busy || loading}
          >
            <RefreshCw aria-hidden="true" />
          </Button>
          <Button onClick={() => edit()} disabled={busy}>
            {kind === "users" ? "Добавить пользователя" : "Добавить логин"}
          </Button>
        </div>
      </div>
      {error && !open && (
        <Alert variant="destructive" className="error" role="alert">
          {error}
        </Alert>
      )}
      <EntityDialog
        open={open}
        onOpenChange={(next) => {
          if (!next) closeEditor();
        }}
        busy={busy}
        title={
          kind === "users"
            ? editing
              ? "Изменить пользователя"
              : "Добавить пользователя"
            : editing
              ? "Изменить логин прокси"
              : "Добавить логин прокси"
        }
        onSubmit={submit}
        submitDisabled={unchanged}
        error={error}
        formLabel={
          editing ? "Редактирование учётной записи" : "Новая учётная запись"
        }
      >
        <Label>
          Логин
          <Input
            aria-label="Логин учётной записи"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={editing !== null}
            pattern="[A-Za-z0-9._-]+"
            maxLength={128}
            required
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            autoFocus
          />
        </Label>
        {kind === "users" && (
          <Label>
            Роль
            <SelectField
              aria-label="Роль пользователя"
              value={role}
              onValueChange={(value) => setRole(value as PanelRole)}
            >
              {Object.entries(roleNames).map(([value, name]) => (
                <SelectItem key={value} value={value}>
                  {name}
                </SelectItem>
              ))}
            </SelectField>
          </Label>
        )}
        <Label>
          {editing ? "Новый пароль" : "Пароль"}
          <Input
            aria-label="Пароль учётной записи"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={12}
            maxLength={1024}
            required={!editing}
            autoComplete="new-password"
            placeholder={
              editing
                ? "Оставьте пустым, чтобы сохранить"
                : "Не менее 12 символов"
            }
          />
        </Label>
      </EntityDialog>
      <div className="table-wrap">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Логин</TableHead>
              {kind === "users" && <TableHead>Роль</TableHead>}
              <TableHead>Доступ</TableHead>
              <TableHead>
                <span className="sr-only">Действия</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((account) => (
              <TableRow key={account.username}>
                <TableCell>
                  <strong>{account.username}</strong>
                  {account.username === currentUser && kind === "users" && (
                    <span className="subtle"> · вы</span>
                  )}
                </TableCell>
                {kind === "users" && (
                  <TableCell>{roleNames[account.role ?? "manager"]}</TableCell>
                )}
                <TableCell>
                  <span
                    className={`client-state ${account.enabled ? "" : "disabled"}`}
                  >
                    {account.enabled ? "Включён" : "Заблокирован"}
                  </span>
                </TableCell>
                <TableCell className="row-actions">
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() => edit(account)}
                  >
                    Изменить
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() => toggle(account)}
                  >
                    {account.enabled ? "Заблокировать" : "Разблокировать"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {loading && <p className="empty">Загрузка…</p>}
      {!loading && !error && !items.length && (
        <p className="empty">
          {kind === "users"
            ? "Пользователей пока нет."
            : "Логины прокси ещё не добавлены."}
        </p>
      )}
    </Panel>
  );
}

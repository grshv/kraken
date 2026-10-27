import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
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
  viewer: "Наблюдатель",
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
    [role, setRole] = useState<PanelRole>("viewer");
  async function reload() {
    const result = await api.current<{ items: Account[] }>(`/${kind}`);
    setItems(result.items);
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
  function edit(account?: Account) {
    setEditing(account?.username ?? null);
    setUsername(account?.username ?? "");
    setPassword("");
    setRole(account?.role ?? "viewer");
    setOpen(true);
    setError("");
  }
  async function submit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
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
    <section
      className="section accounts"
      aria-label={kind === "users" ? "Пользователи панели" : "Логины прокси"}
    >
      <div className="section-head">
        <h2>{kind === "users" ? "Пользователи панели" : "Логины прокси"}</h2>
        <Button onClick={() => edit()} disabled={busy}>
          {kind === "users" ? "Добавить пользователя" : "Добавить логин"}
        </Button>
      </div>
      {kind === "proxy-accounts" && (
        <p className="inline-note">
          Назначьте логин клиенту в его настройках. Парольная авторизация
          требует TLS к прокси.
        </p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {open && (
        <form
          className="account-form"
          onSubmit={submit}
          aria-label={
            editing ? "Редактирование учётной записи" : "Новая учётная запись"
          }
        >
          <label>
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
              autoFocus
            />
          </label>
          {kind === "users" && (
            <label>
              Роль
              <NativeSelect
                aria-label="Роль пользователя"
                value={role}
                onChange={(e) => setRole(e.target.value as PanelRole)}
              >
                {Object.entries(roleNames).map(([value, name]) => (
                  <option key={value} value={value}>
                    {name}
                  </option>
                ))}
              </NativeSelect>
            </label>
          )}
          <label>
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
          </label>
          <div className="actions">
            <Button disabled={busy}>
              {busy ? "Сохранение…" : "Сохранить"}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => {
                setOpen(false);
                setPassword("");
              }}
            >
              Отмена
            </Button>
          </div>
        </form>
      )}
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
                  <TableCell>{roleNames[account.role ?? "viewer"]}</TableCell>
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
      {!loading && !items.length && !error && (
        <p className="empty">
          {kind === "users"
            ? "Пользователей пока нет."
            : "Логины прокси ещё не добавлены."}
        </p>
      )}
    </section>
  );
}

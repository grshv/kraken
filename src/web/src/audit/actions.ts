// Russian labels and categories for audit actions emitted by the Go server.
// Audit rows carry only {actor, action, entity_id, timestamp}; the UI never
// invents free-text titles, it derives them from `action`. Keeping the table
// here means a new Go action surfaces as the raw code instead of a silent
// placeholder.
//
// Grepped in src/internal: action strings written via Store.Audit /
// Store.AuditTx — auth.go (login, login_failed, logout, csrf_failed,
// rate_limited, session_capacity_reached, password_reset), users.go
// (update_scope), store/users.go (create_user, update_user),
// store/proxy_accounts.go (create_proxy_account, update_proxy_account,
// reset_proxy_password), httpapi/server.go (update_draft, discard_draft,
// create_revision).

export type AuditCategory = "apply" | "login" | "account" | "draft" | "other";

export const AUDIT_ACTIONS: readonly string[] = [
  "csrf_failed",
  "rate_limited",
  "login_failed",
  "login",
  "logout",
  "session_capacity_reached",
  "password_reset",
  "create_user",
  "update_user",
  "update_scope",
  "create_proxy_account",
  "update_proxy_account",
  "reset_proxy_password",
  "update_draft",
  "discard_draft",
  "create_revision",
] as const;

const LABELS: Record<string, string> = {
  csrf_failed: "Ошибка проверки CSRF",
  rate_limited: "Превышен лимит попыток входа",
  login_failed: "Неудачный вход",
  login: "Вход в панель",
  logout: "Выход из панели",
  session_capacity_reached: "Достигнут лимит сессий",
  password_reset: "Сброс пароля (CLI)",
  create_user: "Создан пользователь панели",
  update_user: "Изменён пользователь панели",
  update_scope: "Изменена область менеджера",
  create_proxy_account: "Создан логин прокси",
  update_proxy_account: "Изменён логин прокси",
  reset_proxy_password: "Сброшен пароль прокси",
  update_draft: "Изменение в черновике",
  discard_draft: "Сброс черновика",
  create_revision: "Создание ревизии",
};

const CATEGORIES: Record<string, AuditCategory> = {
  create_revision: "apply",
  login: "login",
  login_failed: "login",
  logout: "login",
  csrf_failed: "login",
  rate_limited: "login",
  session_capacity_reached: "login",
  create_user: "account",
  update_user: "account",
  update_scope: "account",
  create_proxy_account: "account",
  update_proxy_account: "account",
  reset_proxy_password: "account",
  password_reset: "account",
  update_draft: "draft",
  discard_draft: "draft",
};

// actionLabel returns a Russian phrase for the action, or the raw code when
// the server emits an action the UI was not updated for.
export function actionLabel(action: string): string {
  return LABELS[action] ?? action;
}

// actionCategory maps an action to a filter group. Unknown actions fall in
// "other" so they still appear under "Все" in the audit list.
export function actionCategory(action: string): AuditCategory {
  return CATEGORIES[action] ?? "other";
}

export const AUDIT_FILTERS: { id: AuditCategory | "all"; label: string }[] = [
  { id: "all", label: "Все" },
  { id: "apply", label: "Применения" },
  { id: "login", label: "Входы" },
  { id: "account", label: "Учётки" },
];

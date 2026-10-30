import type { Snapshot } from "../model";

export type Explain = {
  decision: "allowed" | "denied";
  reason: string;
  client_id: string | null;
  policy_id: string | null;
};

export type ExplainStep = { ok: boolean; text: string };

// explainSteps turns /explain's single reason into the check chain the server
// walks (explainEvent in httpapi): built-in protection, blocked for all, the
// source client, enabled, proxy login, then the client's access: policy_id
// names the attached policy that allowed the host, null its own domains. Checks before
// the failing one passed; checks after it were not reached and are omitted.
export function explainSteps(
  ex: Explain,
  ev: { source_ip: string; destination: string },
  snap: Snapshot | null,
): ExplainStep[] {
  const [host, port = ""] = ev.destination.split("/")[0].split(":");
  const client = snap?.clients.find((c) => c.id === ex.client_id);
  const policy = snap?.policies.find((p) => p.id === ex.policy_id);
  const steps: ExplainStep[] = [];
  const add = (ok: boolean, text: string) => {
    steps.push({ ok, text });
    return ok;
  };
  const r = ex.reason;
  if (
    !add(
      r !== "protected_destination",
      r === "protected_destination"
        ? `Адрес или порт${port ? " " + port : ""} закрыт встроенной защитой`
        : `${port ? `Порт ${port} разрешён, а` : "А"}дрес внешний`,
    )
  )
    return steps;
  if (
    !add(
      r !== "blocked_for_all",
      r === "blocked_for_all"
        ? `${host} в «запрещено для всех»`
        : "Не в «запрещено для всех»",
    )
  )
    return steps;
  if (
    !add(
      r !== "unknown_source",
      r === "unknown_source"
        ? `Источник ${ev.source_ip} не описан как клиент`
        : `Источник: клиент ${client?.name ?? ex.client_id ?? "—"}`,
    )
  )
    return steps;
  if (r === "client_disabled") {
    add(false, "Клиент отключён");
    return steps;
  }
  if (r === "auth_failed") {
    add(false, "Логин прокси не подошёл");
    return steps;
  }
  if (r === "allowed_all_web") add(true, "Доступ клиента: весь веб");
  else if (r === "allowed_by_policy")
    add(
      true,
      policy
        ? `${host} есть в политике «${policy.name}»`
        : `${host} есть в своих доменах клиента`,
    );
  else if (client?.access === "none") add(false, "У клиента нет доступа");
  else add(false, `${host} нет в списке доменов клиента`);
  return steps;
}

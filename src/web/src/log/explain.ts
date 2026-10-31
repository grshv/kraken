import type { Snapshot } from "../model";

export type Explain = {
  decision: "allowed" | "denied";
  reason: string;
  client_id: string | null;
  policy_id: string | null;
};

// whyText explains a denial against the current (applied) rules in one
// sentence, after the chain the server walks (explainEvent in httpapi):
// built-in protection, blocked for all, the source client, enabled, proxy
// login, then the client's access mode and lists. An allowed decision means
// the denial is historical.
export function whyText(
  ex: Explain,
  ev: { source_ip: string; destination: string },
  snap: Snapshot | null,
): string {
  const [host, port = ""] = ev.destination.split("/")[0].split(":");
  const client = snap?.clients.find((c) => c.id === ex.client_id);
  const name = client?.name ?? "Клиент";
  if (ex.decision === "allowed")
    return "Исторический отказ: сейчас такой запрос разрешён.";
  switch (ex.reason) {
    case "protected_destination":
      return `${port ? `Порт ${port} или адрес` : "Адрес"} закрыт встроенной защитой: открыты только порты сайтов HTTP и HTTPS, внутренние адреса закрыты.`;
    case "blocked_for_all":
      return `${host} в «запрещено для всех»: отказ всем клиентам, включая «весь веб».`;
    case "unknown_source":
      return `IP ${ev.source_ip} не входит ни в одного клиента. Неописанные источники получают отказ.`;
    case "client_disabled":
      return `${name} выключен: все его запросы получают отказ.`;
    case "auth_failed":
      return `${name} требует логин прокси, а он не подошёл.`;
  }
  if (client?.access === "none") return `${name} в режиме «Нет доступа».`;
  const policies = (snap?.policies ?? [])
    .filter((p) => client?.policy_ids.includes(p.id))
    .map((p) => `«${p.name}»`);
  return `${name} работает по списку доменов: ${host} нет ни в своих доменах${
    policies.length
      ? `, ни в ${policies.length > 1 ? "политиках" : "политике"} ${policies.join(", ")}`
      : ""
  }.`;
}

import type { Client, Destination, Policy, Snapshot } from "../model";

// clientChanges describes what changed for one client between two snapshots
// (an older applied revision and a newer one, or active and draft): the
// client's own fields and the content of the policy it uses. The audit log
// stores only action names, so the client history is derived from snapshots.
export function clientChanges(
  before: Snapshot | null,
  after: Snapshot,
  clientID: string,
): string[] {
  const a = before?.clients.find((c) => c.id === clientID);
  const b = after.clients.find((c) => c.id === clientID);
  if (!a && !b) return [];
  if (!b) return ["Клиент удалён"];
  const pb = policyOf(after, b);
  if (!a) {
    const what = !pb
      ? "без доступа"
      : pb.mode === "all_web"
        ? "весь веб"
        : pb.destinations.length
          ? `политика: ${pb.destinations.map(label).join(", ")}`
          : "без доступа";
    return [`Создан клиент, ${what}`];
  }
  const out: string[] = [];
  if (a.name !== b.name) out.push(`Имя ${a.name} → ${b.name}`);
  if ((a.description ?? "") !== (b.description ?? ""))
    out.push("Изменено описание");
  if (a.enabled !== b.enabled)
    out.push(b.enabled ? "Клиент включён" : "Клиент отключён");
  if (a.source_networks.join(", ") !== b.source_networks.join(", "))
    out.push(
      `Источник ${a.source_networks.join(", ")} → ${b.source_networks.join(", ")}`,
    );
  if ((a.proxy_username ?? "") !== (b.proxy_username ?? ""))
    out.push(
      b.proxy_username
        ? `Логин прокси: ${b.proxy_username}`
        : "Логин прокси снят",
    );
  const pa = before ? policyOf(before, a) : null;
  if (a.policy_id !== b.policy_id) {
    out.push(`Политика: ${pa?.name ?? "—"} → ${pb?.name ?? "—"}`);
    return out;
  }
  if (pa && pb) {
    if (pa.mode !== pb.mode)
      out.push(
        pb.mode === "all_web" ? "Доступ: весь веб" : "Доступ: список доменов",
      );
    const added = pb.destinations.filter((d) => !has(pa.destinations, d));
    const removed = pa.destinations.filter((d) => !has(pb.destinations, d));
    for (const d of pb.destinations) {
      const old = pa.destinations.find((x) => x.domain === d.domain);
      if (old && old.include_subdomains !== d.include_subdomains)
        out.push(
          `${d.domain}: ${d.include_subdomains ? "с поддоменами" : "без поддоменов"}`,
        );
    }
    if (added.length)
      out.push(
        `${added.length > 1 ? "Добавлены" : "Добавлен"} ${added.map(label).join(", ")}`,
      );
    if (removed.length)
      out.push(
        `${removed.length > 1 ? "Удалены" : "Удалён"} ${removed.map(label).join(", ")}`,
      );
  }
  return out;
}

function policyOf(s: Snapshot, c: Client): Policy | null {
  return s.policies.find((p) => p.id === c.policy_id) ?? null;
}

function label(d: Destination): string {
  return (d.include_subdomains ? "*." : "") + d.domain;
}

function has(list: Destination[], d: Destination): boolean {
  return list.some((x) => x.domain === d.domain);
}

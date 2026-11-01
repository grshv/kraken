import type { Client, Destination, Snapshot } from "../model";
import { effectiveDestinations } from "../draft/edits";

// clientChanges describes what changed for one client between two snapshots
// (an older applied revision and a newer one, or active and draft): the
// client's own fields, its access mode, attached policies and the final list
// of domains it may reach. The audit log stores only action names, so the
// client history is derived from snapshots.
export function clientChanges(
  before: Snapshot | null,
  after: Snapshot,
  clientID: string,
): string[] {
  const a = before?.clients.find((c) => c.id === clientID);
  const b = after.clients.find((c) => c.id === clientID);
  if (!a && !b) return [];
  if (!b) return ["Клиент удалён"];
  if (!a) {
    const list = finalList(after, b);
    const what =
      b.access === "all_web"
        ? "весь веб"
        : b.access === "list" && list.length
          ? `домены: ${list.map(label).join(", ")}`
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
  if (a.tls_profile_id !== b.tls_profile_id) {
    const name = after.tls_profiles.find((p) => p.id === b.tls_profile_id);
    out.push(`Подключение: ${name?.name ?? b.tls_profile_id}`);
  }
  if (a.access !== b.access) out.push(`Доступ: ${accessLabel(b)}`);
  const policyName = (id: string) =>
    after.policies.find((p) => p.id === id)?.name ??
    before?.policies.find((p) => p.id === id)?.name ??
    id;
  for (const id of b.policy_ids)
    if (!a.policy_ids.includes(id))
      out.push(`Подключена политика «${policyName(id)}»`);
  for (const id of a.policy_ids)
    if (!b.policy_ids.includes(id))
      out.push(`Отключена политика «${policyName(id)}»`);
  if (b.access === "list") {
    const la = before ? finalList(before, a) : [];
    const lb = finalList(after, b);
    for (const d of lb) {
      const old = la.find((x) => x.domain === d.domain);
      if (old && old.include_subdomains !== d.include_subdomains)
        out.push(
          `${d.domain}: ${d.include_subdomains ? "с поддоменами" : "без поддоменов"}`,
        );
    }
    const added = lb.filter((d) => !has(la, d));
    const removed = la.filter((d) => !has(lb, d));
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

function finalList(s: Snapshot, c: Client): Destination[] {
  return effectiveDestinations(s, c).map((e) => e.destination);
}

export function accessLabel(c: Pick<Client, "access">): string {
  return c.access === "all_web"
    ? "весь веб"
    : c.access === "none"
      ? "нет доступа"
      : "список доменов";
}

function label(d: Destination): string {
  return (d.include_subdomains ? "*." : "") + d.domain;
}

function has(list: Destination[], d: Destination): boolean {
  return list.some((x) => x.domain === d.domain);
}

import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import { whyText, type Explain } from "../../src/log/explain";

const snap = {
  clients: [
    { id: "c1", name: "ci-01", access: "list", policy_ids: ["p1", "p2"] },
    { id: "c2", name: "lab", access: "none", policy_ids: [] },
  ],
  policies: [
    { id: "p1", name: "Сборка" },
    { id: "p2", name: "Python" },
  ],
} as unknown as Snapshot;
const ev = { source_ip: "10.20.0.15", destination: "registry.npmjs.org:443" };
const ex = (reason: string, decision = "denied", client_id = "c1"): Explain =>
  ({ reason, decision, client_id, policy_id: null }) as Explain;

describe("whyText", () => {
  it("names the client's lists the domain is missing from", () => {
    expect(whyText(ex("not_in_allowlist"), ev, snap)).toBe(
      "ci-01 работает по списку доменов: registry.npmjs.org нет ни в своих доменах, ни в политиках «Сборка», «Python».",
    );
  });

  it("stops at protection with the port", () => {
    expect(whyText(ex("protected_destination"), ev, snap)).toMatch(
      /^Порт 443 или адрес закрыт встроенной защитой/,
    );
  });

  it("names the undescribed source", () => {
    expect(
      whyText({ ...ex("unknown_source"), client_id: null }, ev, snap),
    ).toBe(
      "IP 10.20.0.15 не входит ни в одного клиента. Неописанные источники получают отказ.",
    );
  });

  it("explains the no-access mode and a disabled client", () => {
    expect(whyText(ex("not_in_allowlist", "denied", "c2"), ev, snap)).toBe(
      "lab в режиме «Нет доступа».",
    );
    expect(whyText(ex("client_disabled"), ev, snap)).toBe(
      "ci-01 выключен: все его запросы получают отказ.",
    );
  });

  it("calls a denial historical when the rules now allow it", () => {
    expect(whyText(ex("allowed_by_policy", "allowed"), ev, snap)).toBe(
      "Исторический отказ: сейчас такой запрос разрешён.",
    );
  });
});

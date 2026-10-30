import { describe, it, expect } from "vitest";
import type { Snapshot } from "../../src/model";
import { explainSteps, type Explain } from "../../src/log/explain";

const snap = {
  clients: [{ id: "c1", name: "ci-01" }],
  policies: [{ id: "p1", name: "Сборка" }],
} as unknown as Snapshot;
const ev = { source_ip: "10.20.0.15", destination: "registry.npmjs.org:443" };
const ex = (reason: string, decision = "denied"): Explain =>
  ({ reason, decision, client_id: "c1", policy_id: "p1" }) as Explain;

describe("explainSteps", () => {
  it("walks to the failing policy check", () => {
    expect(explainSteps(ex("not_in_allowlist"), ev, snap)).toEqual([
      { ok: true, text: "Порт 443 разрешён, адрес внешний" },
      { ok: true, text: "Не в «запрещено для всех»" },
      { ok: true, text: "Источник: клиент ci-01" },
      { ok: false, text: "registry.npmjs.org нет в политике «Сборка»" },
    ]);
  });

  it("stops at protection without reaching the client", () => {
    expect(explainSteps(ex("protected_destination"), ev, snap)).toEqual([
      { ok: false, text: "Адрес или порт 443 закрыт встроенной защитой" },
    ]);
  });

  it("names the undescribed source", () => {
    const steps = explainSteps(
      { ...ex("unknown_source"), client_id: null, policy_id: null },
      ev,
      snap,
    );
    expect(steps.at(-1)).toEqual({
      ok: false,
      text: "Источник 10.20.0.15 не описан как клиент",
    });
  });

  it("marks an allowed request as passing every check", () => {
    const steps = explainSteps(ex("allowed_by_policy", "allowed"), ev, snap);
    expect(steps.every((s) => s.ok)).toBe(true);
    expect(steps.at(-1)?.text).toBe(
      "registry.npmjs.org есть в политике «Сборка»",
    );
  });
});

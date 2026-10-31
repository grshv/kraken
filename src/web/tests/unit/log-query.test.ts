import { describe, expect, it } from "vitest";
import { denyReasonLabel, isDenied } from "../../src/log/query";
import {
  decodeState,
  defaultFilters,
  facetStep,
  hashFor,
  logIntent,
  periodWindow,
  shiftWindow,
  stateFromHash,
  toQuery,
  type Filters,
} from "../../src/log/filters";
import { hostOf, portOf, formatBytes } from "../../src/log/data";
import { fromMoscow } from "../../src/log/DateRangePicker";

const NOW = new Date("2026-10-29T13:00:00Z");
const f = (over: Partial<Filters> = {}): Filters => ({
  ...defaultFilters(),
  ...over,
});

describe("isDenied", () => {
  it("matches DENIED and FAIL in proxy_result and status >= 400", () => {
    expect(
      isDenied({ proxy_result: "TCP_DENIED/403", proxy_http_status: 403 }),
    ).toBe(true);
    expect(
      isDenied({ proxy_result: "tcp_denied/403", proxy_http_status: null }),
    ).toBe(true);
    expect(isDenied({ proxy_result: "FAIL/500", proxy_http_status: 500 })).toBe(
      true,
    );
    expect(
      isDenied({ proxy_result: "TCP_MISS/000", proxy_http_status: 503 }),
    ).toBe(true);
    expect(
      isDenied({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 }),
    ).toBe(false);
  });
  it("labels reasons that are not filters too", () => {
    expect(denyReasonLabel("no_rule")).toBe("Нет правила для клиента");
    expect(denyReasonLabel("proxy_auth")).toBe(
      "Нет или неверный пароль прокси",
    );
    expect(denyReasonLabel("something")).toBe("something");
  });
});

describe("toQuery", () => {
  it("presets end now and pick the histogram step", () => {
    const q = toQuery(f({ period: "24 ч" }), NOW, { step: true });
    expect(q.get("to")).toBe(NOW.toISOString());
    expect(q.get("from")).toBe("2026-10-28T13:00:00.000Z");
    expect(q.get("step")).toBe("15m");
    expect(toQuery(f({ period: "1 ч" }), NOW, { step: true }).get("step")).toBe(
      "1m",
    );
    expect(
      toQuery(f({ period: "7 дн" }), NOW, { step: true }).get("step"),
    ).toBe("1h");
  });

  it("repeats multi-select values; unmatched sources are their own flag", () => {
    const q = toQuery(
      f({
        clients: ["c1", "", "c2"],
        methods: ["GET", "CONNECT"],
        reasons: ["no_rule"],
      }),
      NOW,
    );
    expect(q.getAll("client_id")).toEqual(["c1", "c2"]);
    expect(q.get("unmatched")).toBe("true");
    expect(q.getAll("method")).toEqual(["GET", "CONNECT"]);
    expect(q.getAll("deny_reason")).toEqual(["no_rule"]);
  });

  it("maps the result to failures_only / allowed_only", () => {
    expect(toQuery(f({ result: "denied" }), NOW).get("failures_only")).toBe(
      "true",
    );
    const allowed = toQuery(f({ result: "allowed" }), NOW);
    expect(allowed.get("allowed_only")).toBe("true");
    expect(allowed.has("failures_only")).toBe(false);
    expect(
      toQuery(f({ result: "denied" }), NOW, { except: "result" }).has(
        "failures_only",
      ),
    ).toBe(false);
  });

  it("leaves out the facet's own selection", () => {
    const q = toQuery(f({ clients: ["c1"], methods: ["GET"] }), NOW, {
      except: "clients",
    });
    expect(q.has("client_id")).toBe(false);
    expect(q.getAll("method")).toEqual(["GET"]);
  });

  it("a client tab always narrows to its client", () => {
    const q = toQuery(f({ clients: ["other"] }), NOW, {
      clientID: "c9",
      except: "clients",
    });
    expect(q.getAll("client_id")).toEqual(["c9"]);
  });

  it("search is a source IP when it is an address, else a domain; host wins", () => {
    expect(toQuery(f({ search: "10.1.2.3" }), NOW).get("source_ip")).toBe(
      "10.1.2.3",
    );
    expect(toQuery(f({ search: "fd00::1" }), NOW).get("source_ip")).toBe(
      "fd00::1",
    );
    expect(toQuery(f({ search: " npmjs " }), NOW).get("destination")).toBe(
      "npmjs",
    );
    const both = toQuery(f({ search: "npmjs", host: "pypi.org" }), NOW);
    expect(both.get("destination")).toBe("pypi.org");
  });

  it("a custom range is used as is", () => {
    const range = {
      from: "2026-10-27T09:00:00.000Z",
      to: "2026-10-27T10:30:00.000Z",
    };
    const q = toQuery(f({ period: "custom", range }), NOW, { step: true });
    expect(q.get("from")).toBe(range.from);
    expect(q.get("to")).toBe(range.to);
    expect(q.get("step")).toBe("15m");
    expect(facetStep(new Date(range.from), new Date(range.from))).toBe("1m");
  });

  it("a custom period without a range falls back to an hour", () => {
    const w = periodWindow(f({ period: "custom" }), NOW);
    expect(NOW.getTime() - w.from.getTime()).toBe(3600_000);
  });
});

describe("journal state", () => {
  it("round-trips through the hash and the intent", () => {
    const s = {
      view: "domains" as const,
      filters: f({ clients: ["c1", ""], result: "denied", host: "a.test" }),
    };
    expect(stateFromHash(hashFor(s))).toEqual(s);
    expect(decodeState(logIntent(s).slice(4))).toEqual(s);
  });

  it("drops malformed state and unknown values", () => {
    expect(stateFromHash("#log=%7Bbad")).toBeNull();
    expect(stateFromHash("#other")).toBeNull();
    expect(decodeState('{"v":"pairs","f":{}}')).toBeNull();
    const s = decodeState(
      '{"v":"events","f":{"result":"x","period":"9 лет","clients":[1,"c"]}}',
    );
    expect(s?.filters.result).toBe("all");
    expect(s?.filters.period).toBe("24 ч");
    expect(s?.filters.clients).toEqual(["c"]);
    expect(
      decodeState('{"v":"events","f":{"period":"custom"}}')?.filters.period,
    ).toBe("24 ч");
  });
});

describe("destinations and units", () => {
  it("splits host and port, IPv6 in brackets", () => {
    expect(hostOf("pypi.org:443")).toBe("pypi.org");
    expect(portOf("pypi.org:443")).toBe("443");
    expect(hostOf("[2001:db8::1]:443")).toBe("2001:db8::1");
    expect(portOf("[2001:db8::1]:443")).toBe("443");
    expect(hostOf("example.com")).toBe("example.com");
  });
  it("formats bytes", () => {
    expect(formatBytes(0)).toBe("0 Б");
    expect(formatBytes(1536)).toBe("1,5 КБ");
  });
  it("custom range times are Moscow time", () => {
    expect(fromMoscow(new Date(2026, 9, 27), "12:30")).toBe(
      "2026-10-27T09:30:00.000Z",
    );
  });
});

describe("long windows and window navigation", () => {
  const LATE = new Date("2026-10-29T13:25:10Z");
  it("windows over a day snap to whole hours so the server uses its aggregate", () => {
    const w = periodWindow(f({ period: "30 дн" }), LATE);
    expect(w.to.toISOString()).toBe("2026-10-29T14:00:00.000Z");
    expect(w.from.toISOString()).toBe("2026-09-29T14:00:00.000Z");
    expect(
      toQuery(f({ period: "30 дн" }), LATE, { step: true }).get("step"),
    ).toBe("1h");
    // A day or less stays exact.
    expect(periodWindow(f({ period: "24 ч" }), LATE).to).toEqual(LATE);
  });
  it("a long custom range widens to hours but never past 31 days", () => {
    const w = periodWindow(
      f({
        period: "custom",
        range: { from: "2026-09-01T10:20:00Z", to: "2026-10-02T10:40:00Z" },
      }),
      LATE,
    );
    expect(w.to.toISOString()).toBe("2026-10-02T11:00:00.000Z");
    expect(w.to.getTime() - w.from.getTime()).toBe(31 * 24 * 3600_000);
  });
  it("steps back by the window length and returns to live at now", () => {
    const back = shiftWindow(f({ period: "24 ч" }), NOW, -1);
    expect(back).toEqual({
      period: "custom",
      range: {
        from: "2026-10-27T13:00:00.000Z",
        to: "2026-10-28T13:00:00.000Z",
      },
    });
    expect(shiftWindow(f(back), NOW, 1)).toEqual({
      period: "24 ч",
      range: undefined,
    });
    // A custom length without a preset stops at now.
    const odd = f({
      period: "custom",
      range: { from: "2026-10-29T10:00:00Z", to: "2026-10-29T12:00:00Z" },
    });
    expect(shiftWindow(odd, NOW, 1).range?.to).toBe(NOW.toISOString());
  });
});

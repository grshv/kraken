import { describe, expect, it } from "vitest";
import {
  buildEventsParams,
  buildFacetsParams,
  FACET_WINDOWS,
  isDenied,
  parseLogQuery,
  toEventsQuery,
  toFacetsQuery,
  type LogEvent,
} from "../../src/log/query";

// ev uses explicit undefined checks (not ??) so callers can override fields
// back to null, which `null ?? default` would otherwise undo.
function ev(over: Partial<LogEvent> = {}): LogEvent {
  const o = (k: keyof LogEvent, d: LogEvent[typeof k]) =>
    over[k] !== undefined ? over[k]! : d;
  return {
    id: o("id", "e1"),
    timestamp: o("timestamp", "2026-10-29T13:00:00Z"),
    client_id: o("client_id", "c1"),
    client_name: o("client_name", "ci-01"),
    source_ip: o("source_ip", "10.0.0.5"),
    destination: o("destination", "example.com:443"),
    method: o("method", "CONNECT"),
    proxy_result: o("proxy_result", "TCP_TUNNEL/200"),
    proxy_http_status: o("proxy_http_status", 200),
  };
}

describe("parseLogQuery", () => {
  it("parses every typed token", () => {
    const q = parseLogQuery(
      "client:ci-01 host:example.com method:GET ip:10.0.0.5 result:denied free1 free2",
    );
    expect(q).toEqual({
      client: "ci-01",
      host: "example.com",
      method: "GET",
      source: "10.0.0.5",
      result: "denied",
      free: ["free1", "free2"],
    });
  });
  it("accepts Russian result synonyms and source alias", () => {
    expect(parseLogQuery("результат:разрешено").result).toBe("allowed");
    expect(parseLogQuery("результат:отказ").result).toBe("denied");
    expect(parseLogQuery("source:10.1.2.3").source).toBe("10.1.2.3");
  });
  it("ignores empty values and unknown result values", () => {
    const q = parseLogQuery("client: host: method: result:bogus ip:");
    expect(q).toEqual({ free: [] });
    expect(q.result).toBeUndefined();
  });
  it("drops unknown prefixes into free", () => {
    expect(parseLogQuery("port:443 hello").free).toEqual(["port:443", "hello"]);
  });
  it("returns empty for blank input", () => {
    expect(parseLogQuery("")).toEqual({ free: [] });
    expect(parseLogQuery("   \t  ")).toEqual({ free: [] });
  });
  it("last value wins for repeated typed fields", () => {
    expect(parseLogQuery("client:a client:b").client).toBe("b");
    expect(parseLogQuery("result:allowed result:отказ").result).toBe("denied");
  });
});

describe("isDenied", () => {
  it("matches DENIED and FAIL in proxy_result", () => {
    expect(
      isDenied(ev({ proxy_result: "TCP_DENIED/403", proxy_http_status: 403 })),
    ).toBe(true);
    expect(
      isDenied(ev({ proxy_result: "FAIL/500", proxy_http_status: 500 })),
    ).toBe(true);
    expect(
      isDenied(ev({ proxy_result: "tcp_denied/403", proxy_http_status: null })),
    ).toBe(true);
  });
  it("treats status >= 400 as denied even with a passing result", () => {
    expect(
      isDenied(ev({ proxy_result: "TCP_MISS/000", proxy_http_status: 503 })),
    ).toBe(true);
    expect(
      isDenied(ev({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 })),
    ).toBe(false);
  });
  it("allows ok results", () => {
    expect(
      isDenied(ev({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 })),
    ).toBe(false);
  });
});

describe("buildEventsParams", () => {
  const resolve = (name: string) =>
    name.toLowerCase() === "ci-01" ? "uuid-ci-01" : undefined;
  it("maps client name to client_id, denied -> failures_only, free/host -> destination", () => {
    const q = parseLogQuery(
      "client:ci-01 результат:отказ method:CONNECT ip:10.0.0.5 host:example.com free1 free2",
    );
    const p = buildEventsParams(q, resolve, { limit: 200 });
    expect(p).toEqual({
      client_id: "uuid-ci-01",
      failures_only: true,
      method: "CONNECT",
      source_ip: "10.0.0.5",
      destination: "example.com",
      limit: 200,
    });
  });
  it("drops an unresolved client name and uses free text for destination", () => {
    const q = parseLogQuery("client:ghost hello world");
    const p = buildEventsParams(q, resolve);
    expect(p.client_id).toBeUndefined();
    expect(p.destination).toBe("hello world");
  });
  it("host wins over free for destination", () => {
    const q = parseLogQuery("host:a.com free word");
    expect(buildEventsParams(q, resolve).destination).toBe("a.com");
  });
  it("no failures_only when result omitted or allowed", () => {
    expect(
      buildEventsParams(parseLogQuery(""), resolve).failures_only,
    ).toBeUndefined();
    expect(
      buildEventsParams(parseLogQuery("результат:разрешено"), resolve)
        .failures_only,
    ).toBeUndefined();
  });
  it("serializes with toEventsQuery skipping undefined", () => {
    const q = parseLogQuery("client:ci-01 результат:отказ");
    const s = toEventsQuery(buildEventsParams(q, resolve, { limit: 200 }));
    expect(s).toContain("client_id=uuid-ci-01");
    expect(s).toContain("failures_only=true");
    expect(s).toContain("limit=200");
    expect(s).not.toContain("destination");
    expect(s).not.toContain("cursor");
  });
});

describe("buildFacetsParams", () => {
  const resolve = (name: string) =>
    name.toLowerCase() === "ci-01" ? "uuid-ci-01" : undefined;
  const NOW = new Date("2026-10-29T13:00:00Z");
  const HOUR = FACET_WINDOWS[0];
  const DAY = FACET_WINDOWS[1];
  const WEEK = FACET_WINDOWS[2];

  it("builds from/to/step from the window end and mirrors /events filters", () => {
    const q = parseLogQuery(
      "client:ci-01 результат:отказ method:CONNECT ip:10.0.0.5 host:example.com free1 free2",
    );
    const p = buildFacetsParams(q, resolve, NOW, HOUR);
    expect(p.from).toBe("2026-10-29T12:00:00.000Z");
    expect(p.to).toBe("2026-10-29T13:00:00.000Z");
    expect(p.step).toBe("1m");
    expect(p.client_id).toBe("uuid-ci-01");
    expect(p.failures_only).toBe(true);
    expect(p.method).toBe("CONNECT");
    expect(p.source_ip).toBe("10.0.0.5");
    expect(p.destination).toBe("example.com");
  });
  it("24h window uses 15m step and spans a day back", () => {
    const p = buildFacetsParams(parseLogQuery(""), resolve, NOW, DAY);
    expect(p.from).toBe("2026-10-28T13:00:00.000Z");
    expect(p.step).toBe("15m");
  });
  it("7d window uses 1h step and spans a week back", () => {
    const p = buildFacetsParams(parseLogQuery(""), resolve, NOW, WEEK);
    expect(p.from).toBe("2026-10-22T13:00:00.000Z");
    expect(p.step).toBe("1h");
  });
  it("omits failures_only when result is allowed or absent", () => {
    expect(
      buildFacetsParams(
        parseLogQuery("результат:разрешено"),
        resolve,
        NOW,
        HOUR,
      ).failures_only,
    ).toBeUndefined();
    expect(
      buildFacetsParams(parseLogQuery(""), resolve, NOW, HOUR).failures_only,
    ).toBeUndefined();
  });
  it("drops an unresolved client name", () => {
    const p = buildFacetsParams(
      parseLogQuery("client:ghost"),
      resolve,
      NOW,
      HOUR,
    );
    expect(p.client_id).toBeUndefined();
  });
  it("serializes required keys with toFacetsQuery", () => {
    const q = parseLogQuery("client:ci-01 результат:отказ");
    const s = toFacetsQuery(buildFacetsParams(q, resolve, NOW, HOUR));
    expect(s).toContain("from=2026-10-29T12%3A00%3A00.000Z");
    expect(s).toContain("to=2026-10-29T13%3A00%3A00.000Z");
    expect(s).toContain("step=1m");
    expect(s).toContain("client_id=uuid-ci-01");
    expect(s).toContain("failures_only=true");
    expect(s).not.toContain("cursor");
    expect(s).not.toContain("limit");
  });
});

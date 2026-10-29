import { describe, expect, it } from "vitest";
import {
  buildEventsParams,
  facetCounts,
  isDenied,
  minuteHistogram,
  parseLogQuery,
  toEventsQuery,
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
    expect(isDenied(ev({ proxy_result: "TCP_DENIED/403", proxy_http_status: 403 }))).toBe(true);
    expect(isDenied(ev({ proxy_result: "FAIL/500", proxy_http_status: 500 }))).toBe(true);
    expect(isDenied(ev({ proxy_result: "tcp_denied/403", proxy_http_status: null }))).toBe(true);
  });
  it("treats status >= 400 as denied even with a passing result", () => {
    expect(isDenied(ev({ proxy_result: "TCP_MISS/000", proxy_http_status: 503 }))).toBe(true);
    expect(isDenied(ev({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 }))).toBe(false);
  });
  it("allows ok results", () => {
    expect(isDenied(ev({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 }))).toBe(false);
  });
});

describe("facetCounts", () => {
  it("groups clients by client_id (not name); empty client_id under ''", () => {
    const events: LogEvent[] = [
      ev({ proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200, method: "CONNECT" }),
      ev({ client_id: null, client_name: null, proxy_result: "TCP_DENIED/403", proxy_http_status: 403, method: "GET" }),
      ev({ client_name: "ci-01", proxy_result: "TCP_MISS/200", proxy_http_status: 200, method: "GET" }),
    ];
    const f = facetCounts(events);
    expect(f.result).toEqual({ allowed: 2, denied: 1 });
    expect(f.client).toEqual({ c1: 2, "": 1 });
    expect(f.method).toEqual({ CONNECT: 1, GET: 2 });
  });
  it("splits two client_ids sharing the same name into separate keys", () => {
    const events: LogEvent[] = [
      ev({ id: "e1", client_id: "c1", client_name: "same" }),
      ev({ id: "e2", client_id: "c2", client_name: "same" }),
    ];
    expect(facetCounts(events).client).toEqual({ c1: 1, c2: 1 });
  });
  it("keeps a present client_id as the key even when client_name is null", () => {
    const events: LogEvent[] = [
      ev({ client_id: "c3", client_name: null }),
    ];
    expect(facetCounts(events).client).toEqual({ c3: 1 });
  });
  it("returns empty groups for no events", () => {
    const f = facetCounts([]);
    expect(f.result).toEqual({ allowed: 0, denied: 0 });
    expect(f.client).toEqual({});
    expect(f.method).toEqual({});
  });
});

describe("minuteHistogram", () => {
  const NOW = new Date("2026-10-29T13:05:30Z");
  it("emits minutes bins oldest-first with the last holding now", () => {
    const h = minuteHistogram([], NOW, 5);
    expect(h).toHaveLength(5);
    expect(h[0].start).toBe("2026-10-29T13:01:00.000Z");
    expect(h[4].start).toBe("2026-10-29T13:05:00.000Z");
    expect(h[4].allowed).toBe(0);
    expect(h[4].denied).toBe(0);
  });
  it("buckets events by minute and class", () => {
    const events: LogEvent[] = [
      ev({ timestamp: "2026-10-29T13:01:00Z", proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 }),
      ev({ timestamp: "2026-10-29T13:01:30Z", proxy_result: "TCP_DENIED/403", proxy_http_status: 403 }),
      ev({ timestamp: "2026-10-29T13:05:00Z", proxy_result: "TCP_TUNNEL/200", proxy_http_status: 200 }),
    ];
    const h = minuteHistogram(events, NOW, 5);
    expect(h[0]).toEqual({ start: "2026-10-29T13:01:00.000Z", allowed: 1, denied: 1 });
    expect(h[4]).toEqual({ start: "2026-10-29T13:05:00.000Z", allowed: 1, denied: 0 });
    expect(h.slice(1, 4).every((b) => b.allowed === 0 && b.denied === 0)).toBe(true);
  });
  it("ignores events outside the window", () => {
    const events: LogEvent[] = [
      ev({ timestamp: "2026-10-29T13:00:59Z" }),
      ev({ timestamp: "2026-10-29T13:06:00Z" }),
    ];
    const h = minuteHistogram(events, NOW, 5);
    expect(h.every((b) => b.allowed === 0 && b.denied === 0)).toBe(true);
  });
  it("border bins: exactly first and exactly last", () => {
    const events: LogEvent[] = [
      ev({ timestamp: "2026-10-29T13:01:00Z" }),
      ev({ timestamp: "2026-10-29T13:05:59Z" }),
    ];
    const h = minuteHistogram(events, NOW, 5);
    expect(h[0].allowed).toBe(1);
    expect(h[4].allowed).toBe(1);
  });
  it("returns [] for non-positive minutes", () => {
    expect(minuteHistogram([], NOW, 0)).toEqual([]);
  });
});

describe("buildEventsParams", () => {
  const resolve = (name: string) =>
    name.toLowerCase() === "ci-01" ? "uuid-ci-01" : undefined;
  it("maps client name to client_id, denied -> failures_only, free/host -> destination", () => {
    const q = parseLogQuery("client:ci-01 результат:отказ method:CONNECT ip:10.0.0.5 host:example.com free1 free2");
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
    expect(buildEventsParams(parseLogQuery(""), resolve).failures_only).toBeUndefined();
    expect(
      buildEventsParams(parseLogQuery("результат:разрешено"), resolve).failures_only,
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

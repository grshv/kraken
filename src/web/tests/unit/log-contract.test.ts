import { describe, expect, it } from "vitest";
import { facetCounts, minuteHistogram, parseLogQuery, type LogEvent } from "../../src/log/query";

const ev = (ts: string, client: string | null, method: string, denied: boolean): LogEvent => ({
  id: ts + client + method,
  timestamp: ts,
  client_id: client,
  client_name: client ? client + "-name" : null,
  source_ip: "10.0.0.1",
  destination: "a.example:443",
  method,
  proxy_result: denied ? "TCP_DENIED" : "TCP_TUNNEL",
  proxy_http_status: denied ? 403 : 200,
});

describe("parseLogQuery", () => {
  it("extracts known tokens and keeps free text", () => {
    expect(parseLogQuery("client:ci-01 host:npmjs.org результат:отказ method:CONNECT ip:10.1.0.5 foo bar")).toEqual({
      client: "ci-01",
      host: "npmjs.org",
      result: "denied",
      method: "CONNECT",
      source: "10.1.0.5",
      free: ["foo", "bar"],
    });
  });
  it("accepts english and russian result words, ignores empty values", () => {
    expect(parseLogQuery("result:allowed").result).toBe("allowed");
    expect(parseLogQuery("результат:разрешено").result).toBe("allowed");
    expect(parseLogQuery("result:denied").result).toBe("denied");
    expect(parseLogQuery("client:  ").client).toBeUndefined();
    expect(parseLogQuery("").free).toEqual([]);
  });
});

describe("facetCounts", () => {
  it("counts results, clients (null as unknown) and methods", () => {
    const f = facetCounts([
      ev("2026-10-29T10:00:00Z", "c-a", "CONNECT", false),
      ev("2026-10-29T10:00:10Z", "c-a", "GET", true),
      ev("2026-10-29T10:00:20Z", null, "CONNECT", true),
    ]);
    expect(f.result).toEqual({ allowed: 1, denied: 2 });
    expect(f.client).toEqual({ "c-a": 2, "": 1 });
    expect(f.method).toEqual({ CONNECT: 2, GET: 1 });
  });
});

describe("minuteHistogram", () => {
  it("buckets by minute ending at now, oldest first, empty minutes zero", () => {
    const now = new Date("2026-10-29T10:05:30Z");
    const h = minuteHistogram([
      ev("2026-10-29T10:05:10Z", "c-a", "CONNECT", false),
      ev("2026-10-29T10:05:20Z", "c-a", "CONNECT", true),
      ev("2026-10-29T10:03:59Z", "c-a", "CONNECT", false),
      ev("2026-10-29T09:00:00Z", "c-a", "CONNECT", false),
    ], now, 3);
    expect(h).toHaveLength(3);
    expect(h.map((b) => [b.allowed, b.denied])).toEqual([[1, 0], [0, 0], [1, 1]]);
    expect(new Date(h[2].start).toISOString()).toBe("2026-10-29T10:05:00.000Z");
  });
});

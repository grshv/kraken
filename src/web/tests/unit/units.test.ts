import { describe, it, expect } from "vitest";
import {
  parsePortList,
  parseDuration,
  formatDuration,
  parseNameservers,
} from "../../src/settings/units";

describe("parsePortList", () => {
  it("parses and dedupes, preserving order", () => {
    expect(parsePortList("80, 443,443,8080")).toEqual([80, 443, 8080]);
    expect(parsePortList("443, 80, 443")).toEqual([443, 80]);
  });
  it("accepts a single port", () => {
    expect(parsePortList("443")).toEqual([443]);
  });
  it("null on empty/whitespace", () => {
    expect(parsePortList("")).toBeNull();
    expect(parsePortList("   ")).toBeNull();
    expect(parsePortList(" , , ")).toBeNull();
  });
  it("rejects out-of-range, decimals and garbage", () => {
    expect(parsePortList("0")).toBeNull();
    expect(parsePortList("65536")).toBeNull();
    expect(parsePortList("80.5")).toBeNull();
    expect(parsePortList("abc")).toBeNull();
    expect(parsePortList("80,abc")).toBeNull();
    expect(parsePortList("-80")).toBeNull();
    expect(parsePortList("80 443")).toBeNull(); // space, not comma
  });
});

describe("parseDuration", () => {
  it("seconds, minutes, hours", () => {
    expect(parseDuration("60 с")).toBe(60);
    expect(parseDuration("1 мин")).toBe(60);
    expect(parseDuration("15 мин")).toBe(900);
    expect(parseDuration("1 ч")).toBe(3600);
    expect(parseDuration("2 часов")).toBe(7200);
  });
  it("bare number is seconds", () => {
    expect(parseDuration("90")).toBe(90);
    expect(parseDuration("0")).toBe(0);
  });
  it("trims whitespace", () => {
    expect(parseDuration("  60 с  ")).toBe(60);
  });
  it("rejects negatives, fractions and garbage", () => {
    expect(parseDuration("")).toBeNull();
    expect(parseDuration("-5")).toBeNull();
    expect(parseDuration("1.5 мин")).toBeNull();
    expect(parseDuration("abc")).toBeNull();
    expect(parseDuration("10 дней")).toBeNull();
  });
});

describe("formatDuration", () => {
  it("largest whole unit: 900 → 15 мин, 90 → 90 с", () => {
    expect(formatDuration(900)).toBe("15 мин");
    expect(formatDuration(90)).toBe("90 с");
    expect(formatDuration(3600)).toBe("1 ч");
    expect(formatDuration(7200)).toBe("2 ч");
    expect(formatDuration(60)).toBe("1 мин");
    expect(formatDuration(0)).toBe("0 с");
  });
  it("non-whole minute stays in seconds", () => {
    expect(formatDuration(61)).toBe("61 с");
    expect(formatDuration(3599)).toBe("3599 с");
  });
  it("non-whole hour falls back to minutes", () => {
    expect(formatDuration(5400)).toBe("90 мин"); // 1.5 h
  });
});

describe("duration round-trip", () => {
  it("format→parse round-trips for representative values", () => {
    const vals = [0, 1, 60, 90, 900, 3600, 7200, 86400];
    for (const v of vals) {
      expect(parseDuration(formatDuration(v)), `v=${v}`).toBe(v);
    }
  });
});

describe("parseNameservers", () => {
  it("parses comma-separated IPv4, deduping with order", () => {
    expect(parseNameservers("10.0.0.53, 1.1.1.1")).toEqual([
      "10.0.0.53",
      "1.1.1.1",
    ]);
    expect(parseNameservers("10.0.0.53, 1.1.1.1, 10.0.0.53")).toEqual([
      "10.0.0.53",
      "1.1.1.1",
    ]);
  });
  it("accepts whitespace and newline separators", () => {
    expect(parseNameservers("10.0.0.53 8.8.8.8")).toEqual([
      "10.0.0.53",
      "8.8.8.8",
    ]);
    expect(parseNameservers("10.0.0.53\n8.8.8.8")).toEqual([
      "10.0.0.53",
      "8.8.8.8",
    ]);
  });
  it("accepts and canonicalizes IPv6", () => {
    expect(parseNameservers("2001:DB8::53")).toEqual(["2001:db8::53"]);
    expect(parseNameservers("2001:db8:0:0:0:0:0:53")).toEqual([
      "2001:db8::53",
    ]);
  });
  it("accepts mixed IPv4/IPv6", () => {
    expect(parseNameservers("10.0.0.53, 2001:db8::53")).toEqual([
      "10.0.0.53",
      "2001:db8::53",
    ]);
  });
  it("dedupes canonical-equal IPv6 forms", () => {
    expect(parseNameservers("2001:db8::53, 2001:db8:0:0:0:0:0:53")).toEqual([
      "2001:db8::53",
    ]);
  });
  it("empty/whitespace-only → [] (container DNS)", () => {
    expect(parseNameservers("")).toEqual([]);
    expect(parseNameservers("   ")).toEqual([]);
    expect(parseNameservers(" , , ")).toEqual([]);
  });
  it("trailing comma is fine", () => {
    expect(parseNameservers("10.0.0.53,")).toEqual(["10.0.0.53"]);
  });
  it("rejects garbage", () => {
    expect(parseNameservers("abc")).toBeNull();
    expect(parseNameservers("not.a.dns.server")).toBeNull();
  });
  it("rejects CIDR prefixes", () => {
    expect(parseNameservers("10.0.0.0/8")).toBeNull();
    expect(parseNameservers("2001:db8::/32")).toBeNull();
    expect(parseNameservers("10.0.0.53, 10.0.0.0/8")).toBeNull();
  });
  it("rejects host:port (IPv4 and bracketed IPv6)", () => {
    expect(parseNameservers("10.0.0.1:53")).toBeNull();
    expect(parseNameservers("[2001:db8::1]:53")).toBeNull();
  });
  it("rejects when any token is invalid among valid ones", () => {
    expect(parseNameservers("10.0.0.53, garbage")).toBeNull();
  });
  it("null input → []", () => {
    expect(parseNameservers(null as unknown as string)).toEqual([]);
  });
});

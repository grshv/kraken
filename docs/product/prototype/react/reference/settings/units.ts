// Pure parsing/formatting helpers for Settings fields. No DOM, no fetch.
//
// parsePortList accepts a comma-separated list of TCP ports; duplicates are
// dropped while keeping first-seen order. An empty/whitespace-only list yields
// null so the caller can fall back to the existing value rather than storing
// an empty array (the server requires at least one safe/connect port).
// parseDuration/formatDuration round-trip the seconds-backed settings (DNS
// TTLs, timeouts) for human-friendly input and read-only display.
// parseNameservers splits on comma/whitespace and accepts bare IPv4/IPv6
// literals (no prefix, no port), reusing the canonical formatters from
// clients/networks.ts so the server's netip.ParseAddr sees normalized text.

import { fmtIPv4, fmtIPv6, parseIPv4, parseIPv6 } from "../clients/networks";
import type { UpstreamProxy } from "../model";

// A peer is a single IP literal, not a DNS name, network or URL.
export function parseUpstream(address: string, port: string, exclusive: boolean): UpstreamProxy | null {
  const addresses = parseNameservers(address);
  if (!address.trim() || /[\s,]/.test(address.trim()) || !addresses || addresses.length !== 1) return null;
  if (!/^\d+$/.test(port.trim())) return null;
  const value = Number(port);
  if (!Number.isInteger(value) || value < 1 || value > 65535) return null;
  return { address: addresses[0], port: value, exclusive };
}

// parsePortList: "80, 443,443,8080" → [80,443,8080]; "" → null.
export function parsePortList(text: string): number[] | null {
  if (text == null) return null;
  const trimmed = text.trim();
  if (trimmed === "") return null;
  const seen = new Set<number>();
  const out: number[] = [];
  for (const raw of trimmed.split(",")) {
    const t = raw.trim();
    if (t === "") continue;
    // Integer only; no leading zeros, no "+", no signs, no decimals.
    if (!/^\d+$/.test(t)) return null;
    const v = Number(t);
    if (!Number.isInteger(v) || v < 1 || v > 65535) return null;
    if (seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  // Empty after filtering (e.g. " , ") is the same as empty input.
  return out.length > 0 ? out : null;
}

// Unit spec for durations. Order matters: largest first so formatDuration
// prefers minutes over seconds, hours over minutes.
type DurUnit = { suffix: string; seconds: number };
const UNITS: DurUnit[] = [
  { suffix: "ч", seconds: 3600 },
  { suffix: "мин", seconds: 60 },
  { suffix: "с", seconds: 1 },
];

// parseDuration: "15 мин" → 900, "1 ч" → 3600, "90" → 90, "0" → 0.
// Negative, fractional, unknown unit or garbage → null.
export function parseDuration(text: string): number | null {
  if (text == null) return null;
  const t = text.trim();
  if (t === "") return null;
  const m = t.match(/^(\d+)\s*(с|min|мин|ч|h|час|часов|минут|сек|секунд)?$/i);
  if (!m) return null;
  const v = Number(m[1]);
  if (!Number.isInteger(v) || v < 0) return null;
  const unit = (m[2] || "").toLowerCase();
  if (unit === "") return v; // bare number = seconds
  if (unit === "с" || unit === "сек" || unit === "секунд") return v;
  if (unit === "мин" || unit === "min" || unit === "минут") return v * 60;
  if (unit === "ч" || unit === "h" || unit === "час" || unit === "часов")
    return v * 3600;
  return null;
}

// formatDuration picks the largest unit in which the value is a whole number:
// 900 → "15 мин", 90 → "90 с", 0 → "0 с". 0 special-cased since it divides
// every unit but seconds is the canonical display.
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0 || !Number.isInteger(seconds))
    return String(seconds ?? "");
  if (seconds === 0) return "0 с";
  for (const u of UNITS) {
    if (seconds % u.seconds === 0) {
      const v = seconds / u.seconds;
      return `${v} ${u.suffix}`;
    }
  }
  // seconds >= 1 always divides by 1, so this is unreachable.
  return `${seconds} с`;
}

// parseNameservers: "10.0.0.53, 1.1.1.1" → ["10.0.0.53","1.1.1.1"];
// "2001:DB8::53 8.8.8.8" → ["2001:db8::53","8.8.8.8"]. Comma or whitespace
// separated; each token must be a bare IPv4 or IPv6 literal (no CIDR prefix,
// no port). Canonicalized via fmtIPv4/fmtIPv6 so dedup is stable. Duplicates
// drop with first-seen order. Empty/whitespace-only → [] (use container DNS).
// Any invalid token → null so the caller surfaces a field error.
export function parseNameservers(text: string): string[] | null {
  if (text == null) return [];
  const trimmed = text.trim();
  if (trimmed === "") return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of trimmed.split(/[\s,]+/)) {
    if (raw === "") continue;
    const v4 = parseIPv4(raw);
    if (v4 !== null) {
      const canon = fmtIPv4(v4);
      if (!seen.has(canon)) {
        seen.add(canon);
        out.push(canon);
      }
      continue;
    }
    const v6 = parseIPv6(raw);
    if (v6 !== null) {
      const canon = fmtIPv6(v6);
      if (!seen.has(canon)) {
        seen.add(canon);
        out.push(canon);
      }
      continue;
    }
    // Not a bare IP literal: CIDR, host:port, hostname, garbage.
    return null;
  }
  return out;
}

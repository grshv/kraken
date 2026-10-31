import { useEffect, useState } from "react";
import type { Request } from "../api";
import type { Event } from "../model";

// EventPage mirrors the GET /events response fields the journal consumes.
export type EventPage = {
  items: Event[];
  next_cursor: string | null;
  gap?: boolean;
  observed_at?: string;
  ingestion?: { stale?: boolean };
};

// DomainsReport mirrors GET /events/domains.
export type DomainSource = {
  client_id: string | null;
  client_name: string | null;
  source_ip: string;
  requests: number;
  denied: number;
};
export type DomainRow = {
  host: string;
  requests: number;
  denied: number;
  bytes: number;
  last_seen: string;
  ports: number[];
  source_count: number;
  sources: DomainSource[];
};
export type DomainsReport = {
  items: DomainRow[];
  truncated: boolean;
  summary: {
    requests: number;
    denied: number;
    bytes: number;
    unmatched_sources: number;
    clients: {
      client_id: string | null;
      client_name: string | null;
      requests: number;
      denied: number;
    }[];
  };
};

// hostOf/portOf split a Squid destination ("host:port", IPv6 in brackets).
export function hostOf(destination: string): string {
  const d = destination.split("/")[0];
  if (d.startsWith("[")) return d.slice(1, d.indexOf("]"));
  const i = d.lastIndexOf(":");
  return i > 0 && d.indexOf(":") === i ? d.slice(0, i) : d;
}
export function portOf(destination: string): string {
  const d = destination.split("/")[0];
  const i = d.lastIndexOf(":");
  if (d.startsWith("[")) return d.slice(d.indexOf("]") + 2);
  return i > 0 && d.indexOf(":") === i ? d.slice(i + 1) : "";
}

export const formatCount = (n: number) => n.toLocaleString("ru-RU");

export function formatBytes(n: number): string {
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toLocaleString("ru-RU", { maximumFractionDigits: i ? 1 : 0 })} ${units[i]}`;
}

// useQueries fetches the path of every named slot (paths shared by several
// slots once) whenever a path or the tick changes. A slot keeps its previous
// answer until the new one arrives, so sliding windows do not flash empty.
// failed is true when the latest round failed for any slot.
export function useQueries<T>(
  request: Request,
  slots: Record<string, string | null>,
  tick: number,
): { data: Record<string, T | undefined>; failed: boolean; loaded: boolean } {
  const [data, setData] = useState<Record<string, T | undefined>>({});
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const key = JSON.stringify(slots);
  useEffect(() => {
    const wanted = JSON.parse(key) as Record<string, string | null>;
    const unique = [
      ...new Set(Object.values(wanted).filter((p): p is string => !!p)),
    ];
    const ctrl = new AbortController();
    Promise.allSettled(
      unique.map((p) =>
        request<T>(p, "GET", undefined, undefined, ctrl.signal),
      ),
    ).then((results) => {
      if (ctrl.signal.aborted) return;
      const byPath = new Map(unique.map((p, i) => [p, results[i]]));
      setFailed(results.some((r) => r.status === "rejected"));
      setLoaded(true);
      setData((prev) => {
        const next: Record<string, T | undefined> = {};
        for (const [slot, path] of Object.entries(wanted)) {
          const r = path ? byPath.get(path) : undefined;
          next[slot] =
            r?.status === "fulfilled" ? r.value : path ? prev[slot] : undefined;
        }
        return next;
      });
    });
    return () => ctrl.abort();
  }, [request, key, tick]);
  return { data, failed, loaded };
}

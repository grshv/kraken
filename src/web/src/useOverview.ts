import { useEffect, useRef, useState } from "react";
import type { Request } from "./api";
import { ConnectionError } from "./api";
import type { Status, Event } from "./model";
export type Statistics = {
  from: string;
  to: string;
  observed_at: string;
  requests: number;
  denied: number;
  bytes_observed: number;
  unique_sources: number;
  record_limit: number;
  retention_limited: boolean;
  hourly: { from: string; to: string; requests: number; denied: number }[];
};
export type Ingestion = {
  observed_at: string;
  stale: boolean;
  source_available: boolean;
  backlog: boolean;
  dropped_records: number;
};
export function useOverview(request: Request, enabled: boolean) {
  const api = useRef(request);
  api.current = request;
  const [status, setStatus] = useState<Status | null>(null),
    [events, setEvents] = useState<Event[]>([]),
    [statistics, setStatistics] = useState<Statistics | null>(null),
    [ingestion, setIngestion] = useState<Ingestion | null>(null),
    [overviewError, setOverviewError] = useState("");
  async function refreshOverview(signal?: AbortSignal) {
    const results = await Promise.allSettled([
      api.current<Status>("/status", "GET", undefined, undefined, signal),
      api.current<{ items: Event[]; ingestion: Ingestion }>(
        "/events",
        "GET",
        undefined,
        undefined,
        signal,
      ),
      api.current<Statistics>(
        "/statistics",
        "GET",
        undefined,
        undefined,
        signal,
      ),
    ]);
    if (signal?.aborted) return;
    const [health, history, stats] = results;
    if (health.status === "fulfilled") setStatus(health.value);
    else
      setStatus((prev) =>
        prev
          ? {
              ...prev,
              squid: { ...prev.squid, stale: true },
              runtime: { ...prev.runtime, stale: true },
            }
          : null,
      );
    if (history.status === "fulfilled") {
      setEvents(history.value.items);
      setIngestion(history.value.ingestion);
    } else setIngestion((prev) => (prev ? { ...prev, stale: true } : null));
    if (stats.status === "fulfilled") setStatistics(stats.value);
    const failure = results.find(
      (r) =>
        r.status === "rejected" &&
        !(r.reason instanceof ConnectionError) &&
        r.reason?.name !== "SessionExpired",
    );
    setOverviewError(
      failure
        ? "Часть данных не обновилась. Показаны последние полученные значения."
        : "",
    );
  }
  useEffect(() => {
    if (!enabled) {
      setStatus(null);
      setStatistics(null);
      setEvents([]);
      setIngestion(null);
      return;
    }
    const controller = new AbortController();
    let active = true,
      pending = false;
    const poll = async () => {
      if (!active || pending) return;
      pending = true;
      try {
        await refreshOverview(controller.signal);
      } finally {
        pending = false;
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 10000);
    return () => {
      active = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [enabled]);
  return {
    status,
    setStatus,
    events,
    statistics,
    ingestion,
    overviewError,
    refreshOverview,
  };
}

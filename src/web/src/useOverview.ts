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
export type SquidMetrics = {
  available: boolean;
  stale: boolean;
  observed_at: string | null;
  reason?: string;
  squid_version?: string;
  uptime_seconds: number | null;
  active_requests: number | null;
  cpu_seconds: number | null;
  cpu_usage_percent: number | null;
  cpu_usage_5m_percent: number | null;
  memory_peak_bytes: number | null;
  memory_accounted_bytes: number | null;
  cache_enabled: boolean | null;
  cache_hit_ratio_percent: number | null;
  origin_requests_total: number | null;
  origin_errors_total: number | null;
  origin_requests_per_second: number | null;
  origin_bytes_in_total: number | null;
  origin_bytes_out_total: number | null;
  origin_bytes_in_per_second: number | null;
  origin_bytes_out_per_second: number | null;
};
export function useOverview(request: Request, enabled: boolean) {
  const api = useRef(request);
  api.current = request;
  const [status, setStatus] = useState<Status | null>(null),
    [statistics, setStatistics] = useState<Statistics | null>(null),
    [metrics, setMetrics] = useState<SquidMetrics | null>(null),
    [failures, setFailures] = useState<Event[]>([]),
    [ingestion, setIngestion] = useState<Ingestion | null>(null),
    [overviewError, setOverviewError] = useState("");
  const liveError = useRef(""),
    historyError = useRef("");
  function updateError() {
    setOverviewError(
      [liveError.current, historyError.current].filter(Boolean).join(" "),
    );
  }
  function visibleError(results: PromiseSettledResult<unknown>[]) {
    return results.some(
      (r) =>
        r.status === "rejected" &&
        !(r.reason instanceof ConnectionError) &&
        r.reason?.name !== "SessionExpired",
    );
  }
  async function refreshLive(signal?: AbortSignal) {
    const results = await Promise.allSettled([
      api.current<Status>("/status", "GET", undefined, undefined, signal),
      api.current<SquidMetrics>(
        "/squid/metrics",
        "GET",
        undefined,
        undefined,
        signal,
      ),
    ]);
    if (signal?.aborted) return;
    const [health, live] = results;
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
    if (live.status === "fulfilled") setMetrics(live.value);
    else
      setMetrics((prev) =>
        prev
          ? { ...prev, available: false, stale: true, reason: "unavailable" }
          : null,
      );
    liveError.current = visibleError(results)
      ? "Показатели Squid не обновились."
      : "";
    updateError();
  }
  async function refreshHistory(signal?: AbortSignal) {
    const results = await Promise.allSettled([
      api.current<{ items: Event[]; ingestion: Ingestion }>(
        "/events?failures_only=true&limit=5",
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
    const [history, stats] = results;
    if (history.status === "fulfilled") {
      setFailures(history.value.items);
      setIngestion(history.value.ingestion);
    } else setIngestion((prev) => (prev ? { ...prev, stale: true } : null));
    if (stats.status === "fulfilled") setStatistics(stats.value);
    historyError.current = visibleError(results)
      ? "История запросов не обновилась."
      : "";
    updateError();
  }
  async function refreshOverview(signal?: AbortSignal) {
    await Promise.all([refreshLive(signal), refreshHistory(signal)]);
  }
  useEffect(() => {
    if (!enabled) {
      setStatus(null);
      setMetrics(null);
      setStatistics(null);
      setFailures([]);
      setIngestion(null);
      return;
    }
    const controller = new AbortController();
    let active = true,
      livePending = false,
      historyPending = false;
    async function live() {
      if (!active || livePending) return;
      livePending = true;
      try {
        await refreshLive(controller.signal);
      } finally {
        livePending = false;
      }
    }
    async function history() {
      if (!active || historyPending) return;
      historyPending = true;
      try {
        await refreshHistory(controller.signal);
      } finally {
        historyPending = false;
      }
    }
    void live();
    void history();
    const liveTimer = setInterval(() => void live(), 5000),
      historyTimer = setInterval(() => void history(), 15000);
    return () => {
      active = false;
      controller.abort();
      clearInterval(liveTimer);
      clearInterval(historyTimer);
    };
  }, [enabled]);
  return {
    status,
    setStatus,
    statistics,
    metrics,
    failures,
    ingestion,
    overviewError,
    refreshOverview,
  };
}

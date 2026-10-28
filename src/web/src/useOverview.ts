import { useEffect, useRef, useState } from "react";
import type { Request } from "./api";
import { ConnectionError } from "./api";
import type { Status } from "./model";
export type ActivitySource = {
  source_ip: string;
  client_name: string | null;
  proxy_username: string | null;
  requests: number;
  denied: number;
  errors: number;
  bytes_observed: number;
  last_seen: string;
};
export type ActivityIssue = {
  proxy_username: string | null;
  source_ip: string;
  client_name: string | null;
  destination: string;
  result: string;
  count: number;
  last_seen: string;
};
export type ActivityReport = {
  activity: {
    from: string;
    to: string;
    sources: ActivitySource[];
    issues: ActivityIssue[];
    total_sources: number;
    total_issues: number;
    retention_limited: boolean;
  };
  applied: {
    mode: string;
    enabled_clients: number;
    allowlist_clients: number;
    all_web_clients: number;
  } | null;
  ingestion: {
    observed_at: string;
    source_available: boolean;
    gap: boolean;
    backlog: boolean;
    dropped_records: number;
  };
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
    [activity, setActivity] = useState<ActivityReport | null>(null),
    [metrics, setMetrics] = useState<SquidMetrics | null>(null),
    [overviewError, setOverviewError] = useState("");
  const [minutes, setMinutes] = useState("60");
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
    try {
      const report = await api.current<ActivityReport>(
        `/activity?minutes=${minutes}`,
        "GET",
        undefined,
        undefined,
        signal,
      );
      if (signal?.aborted) return;
      setActivity(report);
      historyError.current = "";
    } catch (e) {
      if (signal?.aborted) return;
      historyError.current =
        e instanceof ConnectionError
          ? ""
          : "Активность клиентов не обновилась. Показаны последние полученные данные.";
    }
    updateError();
  }
  async function refreshOverview(signal?: AbortSignal) {
    await Promise.all([refreshLive(signal), refreshHistory(signal)]);
  }
  useEffect(() => {
    if (!enabled) {
      setStatus(null);
      setMetrics(null);
      setActivity(null);
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
  }, [enabled, minutes]);
  return {
    status,
    setStatus,
    activity,
    minutes,
    setMinutes,
    metrics,
    overviewError,
    refreshOverview,
  };
}

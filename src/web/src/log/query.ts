// Pure helpers for the Log screen: parse the search box, decide denial,
// compute facet counts and the per-minute histogram. Tested by vitest without
// React. LogEvent is the subset of the /events item the screen renders; the
// API contract is in api/openapi.json (Event schema).

export type LogEvent = {
  id: string;
  timestamp: string;
  client_id: string | null;
  client_name: string | null;
  source_ip: string;
  destination: string;
  method: string;
  proxy_result: string;
  proxy_http_status: number | null;
};

export type LogQuery = {
  client?: string;
  host?: string;
  result?: "allowed" | "denied";
  method?: string;
  source?: string;
  free: string[];
};

// parseLogQuery splits the search box into typed tokens. Recognised prefixes
// are client:, host:, method:, ip:/source:, result:/результат:. Unknown
// prefixes and bare tokens fall back to `free`. Empty values are ignored. For
// result, Russian synonyms (разрешено/отказ) are accepted besides English.
export function parseLogQuery(text: string): LogQuery {
  const q: LogQuery = { free: [] };
  for (const raw of text.split(/\s+/)) {
    if (!raw) continue;
    const colon = raw.indexOf(":");
    if (colon <= 0) {
      q.free.push(raw);
      continue;
    }
    const key = raw.slice(0, colon).toLowerCase();
    const val = raw.slice(colon + 1);
    if (val === "") continue;
    if (key === "client") q.client = val;
    else if (key === "host") q.host = val;
    else if (key === "method") q.method = val;
    else if (key === "ip" || key === "source") q.source = val;
    else if (key === "result" || key === "результат") {
      const v = val.toLowerCase();
      if (v === "allowed" || v === "разрешено") q.result = "allowed";
      else if (v === "denied" || v === "отказ") q.result = "denied";
      // unknown value: drop the token
    } else {
      q.free.push(raw);
    }
  }
  return q;
}

// isDenied mirrors the server's denial classification so the UI buckets events
// the same way GET /events?failures_only would. DENIED/FAIL in proxy_result or
// HTTP status >= 400 counts as denied.
export function isDenied(e: LogEvent): boolean {
  const r = (e.proxy_result || "").toUpperCase();
  if (r.includes("DENIED") || r.includes("FAIL")) return true;
  if (e.proxy_http_status != null && e.proxy_http_status >= 400) return true;
  return false;
}

export type FacetCounts = {
  result: { allowed: number; denied: number };
  // Keys are stable client_ids, not client_names (names are non-unique and can
  // change). Events without client_id collapse under "" ("Не описан").
  client: Record<string, number>;
  method: Record<string, number>;
};

// facetCounts summarise loaded events for the sidebar groups. Clients without
// client_id collapse under "" (shown in the UI as "Не описан").
export function facetCounts(events: LogEvent[]): FacetCounts {
  const out: FacetCounts = {
    result: { allowed: 0, denied: 0 },
    client: {},
    method: {},
  };
  for (const e of events) {
    if (isDenied(e)) out.result.denied++;
    else out.result.allowed++;
    // Group by stable client_id, not client_name: names are not unique and can
    // change after edits, while the /events filter resolves to client_id. A
    // missing client_id (unknown source) collapses under "".
    const key = e.client_id ?? "";
    out.client[key] = (out.client[key] ?? 0) + 1;
    const m = e.method || "";
    out.method[m] = (out.method[m] ?? 0) + 1;
  }
  return out;
}

export type HistogramBin = { start: string; allowed: number; denied: number };

// minuteHistogram buckets events into `minutes` per-minute bins; the last bin
// holds the minute containing `now`. Bins are oldest-first; `start` is the ISO
// start-of-minute timestamp. Events outside the window are ignored.
export function minuteHistogram(
  events: LogEvent[],
  now: Date,
  minutes: number,
): HistogramBin[] {
  const bins: HistogramBin[] = [];
  if (minutes <= 0) return bins;
  const lastMs = Math.floor(now.getTime() / 60000) * 60000;
  const firstMs = lastMs - (minutes - 1) * 60000;
  for (let t = firstMs; t <= lastMs; t += 60000) {
    bins.push({ start: new Date(t).toISOString(), allowed: 0, denied: 0 });
  }
  for (const e of events) {
    const ms = Date.parse(e.timestamp);
    if (Number.isNaN(ms)) continue;
    const idx = Math.floor((ms - firstMs) / 60000);
    if (idx < 0 || idx >= bins.length) continue;
    if (isDenied(e)) bins[idx].denied++;
    else bins[idx].allowed++;
  }
  return bins;
}

// EventsParams mirrors the GET /events query parameters the Log screen sends.
// client/hostname resolution happens client-side: the server only accepts an
// exact stored client_id, so a typed name must map to its UUID first.
export type EventsParams = {
  client_id?: string;
  failures_only?: boolean;
  destination?: string;
  method?: string;
  source_ip?: string;
  cursor?: string;
  limit?: number;
};

// buildEventsParams composes the /events query string from a parsed LogQuery
// and a client-name -> id resolver (sourced from the draft or active snapshot).
// host takes precedence over free tokens for the destination substring, since
// the server accepts a single literal substring.
export function buildEventsParams(
  q: LogQuery,
  resolveClient: (name: string) => string | undefined,
  opts: { cursor?: string; limit?: number } = {},
): EventsParams {
  const p: EventsParams = {};
  if (q.client) {
    const id = resolveClient(q.client);
    if (id) p.client_id = id;
    // Unresolved client name: drop (no events match a non-existent client).
  }
  if (q.result === "denied") p.failures_only = true;
  if (q.method) p.method = q.method;
  if (q.source) p.source_ip = q.source;
  const freeStr = q.free.map((s) => s.trim()).filter(Boolean).join(" ");
  const dest = q.host ?? (freeStr || undefined);
  if (dest) p.destination = dest;
  if (opts.cursor) p.cursor = opts.cursor;
  if (opts.limit != null) p.limit = opts.limit;
  return p;
}

// toEventsQuery serializes EventsParams into a query string for fetch.
export function toEventsQuery(p: EventsParams): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) {
    if (v === undefined || v === null) continue;
    sp.set(k, String(v));
  }
  return sp.toString();
}

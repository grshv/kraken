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
  count?: number;
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
  const freeStr = q.free
    .map((s) => s.trim())
    .filter(Boolean)
    .join(" ");
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

// FacetWindow is one preset for the log sidebar: a span and the histogram step
// the server requires. The default is the last hour at 1-minute resolution.
export type FacetWindow = {
  label: string;
  minutes: number;
  step: "1m" | "15m" | "1h";
};

export const FACET_WINDOWS: FacetWindow[] = [
  { label: "1 ч", minutes: 60, step: "1m" },
  { label: "24 ч", minutes: 24 * 60, step: "15m" },
  { label: "7 дн", minutes: 7 * 24 * 60, step: "1h" },
];

// FacetsParams mirrors GET /events/facets query parameters. from/to/to are
// required and derived from the window end (now); the same scalar filters as
// /events are applied so facets match the loaded list.
export type FacetsParams = {
  from: string;
  to: string;
  step: "1m" | "15m" | "1h";
  client_id?: string;
  failures_only?: boolean;
  destination?: string;
  method?: string;
  source_ip?: string;
};

// buildFacetsParams composes the /events/facets query from a parsed LogQuery,
// a client resolver and a window ending at `now`. Filter mapping mirrors
// buildEventsParams minus cursor/limit (facets has no paging).
export function buildFacetsParams(
  q: LogQuery,
  resolveClient: (name: string) => string | undefined,
  now: Date,
  window: FacetWindow,
): FacetsParams {
  const to = new Date(now);
  const from = new Date(to.getTime() - window.minutes * 60000);
  // The server's fast aggregate path requires From/To aligned to 15-minute UTC
  // boundaries for windows over 24h; align the 7-day window down so it is used.
  if (window.minutes > 24 * 60) {
    alignDown15m(from);
    alignDown15m(to);
  }
  const p: FacetsParams = {
    from: from.toISOString(),
    to: to.toISOString(),
    step: window.step,
  };
  if (q.client) {
    const id = resolveClient(q.client);
    if (id) p.client_id = id;
  }
  if (q.result === "denied") p.failures_only = true;
  if (q.method) p.method = q.method;
  if (q.source) p.source_ip = q.source;
  const freeStr = q.free
    .map((s) => s.trim())
    .filter(Boolean)
    .join(" ");
  const dest = q.host ?? (freeStr || undefined);
  if (dest) p.destination = dest;
  return p;
}

// toFacetsQuery serializes FacetsParams into a query string for fetch.
export function toFacetsQuery(p: FacetsParams): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) {
    if (v === undefined || v === null) continue;
    sp.set(k, String(v));
  }
  return sp.toString();
}

// alignDown15m floors a Date to the previous 15-minute UTC boundary in place.
const MS_15M = 15 * 60 * 1000;
function alignDown15m(d: Date): void {
  d.setTime(Math.floor(d.getTime() / MS_15M) * MS_15M);
}

// FacetsResponse mirrors the GET /events/facets response body.
export type FacetsResponse = {
  allowed: number;
  denied: number;
  clients: { id: string; name: string | null; count: number }[];
  methods: { method: string; count: number }[];
  histogram: { start: string; allowed: number; denied: number }[];
};

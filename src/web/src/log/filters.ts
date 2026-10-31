// Journal filters from the prototype: view, period, result, multi-select
// clients/methods/deny reasons, a search box and a drilled-in domain. Pure
// helpers turn them into GET /events, /events/facets and /events/domains
// query strings, the URL hash (#log=…) and the "log:<json>" section intent.
// Tested by vitest without React.

export type View = "events" | "domains";
export type Period = "1 ч" | "24 ч" | "7 дн" | "custom";
export type Result = "all" | "denied" | "allowed";
// Range is a custom period as RFC 3339 instants; presets end at "now".
export type Range = { from: string; to: string };

export type Filters = {
  // Domain substring, or an exact source IP when the text is an address.
  search: string;
  // A domain picked from the summary or the domains view.
  host?: string;
  // Client ids; "" stands for unmatched (unknown) sources.
  clients: string[];
  methods: string[];
  reasons: string[];
  result: Result;
  period: Period;
  range?: Range;
};

export const PERIODS: Exclude<Period, "custom">[] = ["1 ч", "24 ч", "7 дн"];
const PERIOD_MS: Record<Exclude<Period, "custom">, number> = {
  "1 ч": 3600_000,
  "24 ч": 24 * 3600_000,
  "7 дн": 7 * 24 * 3600_000,
};
// The server bounds facets and domains to 7 days.
export const MAX_RANGE_MS = 7 * 24 * 3600_000;

export function defaultFilters(): Filters {
  return {
    search: "",
    clients: [],
    methods: [],
    reasons: [],
    result: "all",
    period: "24 ч",
  };
}

// periodWindow resolves the period to concrete instants; presets end at now.
export function periodWindow(f: Filters, now: Date): { from: Date; to: Date } {
  if (f.period === "custom" && f.range) {
    const from = new Date(f.range.from);
    const to = new Date(f.range.to);
    if (Number.isFinite(from.getTime()) && Number.isFinite(to.getTime()))
      return { from, to };
  }
  const span = f.period === "custom" ? PERIOD_MS["1 ч"] : PERIOD_MS[f.period];
  return { from: new Date(now.getTime() - span), to: now };
}

// facetStep picks the histogram step the server accepts for the window:
// minutes up to an hour, quarters up to a day, hours beyond.
export function facetStep(from: Date, to: Date): "1m" | "15m" | "1h" {
  const span = to.getTime() - from.getTime();
  if (span <= PERIOD_MS["1 ч"]) return "1m";
  if (span <= PERIOD_MS["24 ч"]) return "15m";
  return "1h";
}

const IPV4 = /^(\d{1,3}\.){3}\d{1,3}$/;
const IPV6 = /^[0-9a-f:]+:[0-9a-f:]*$/i;
export function isAddress(text: string): boolean {
  return IPV4.test(text) || (IPV6.test(text) && text.includes(":"));
}

// FilterKey names a multi-select facet; toQuery can leave one out so the
// facet's own counts do not shrink to the current selection.
export type FilterKey = "clients" | "methods" | "reasons" | "result";

// toQuery serialises filters for /events, /events/facets and /events/domains.
export function toQuery(
  f: Filters,
  now: Date,
  opts: { except?: FilterKey; step?: boolean; clientID?: string } = {},
): URLSearchParams {
  const q = new URLSearchParams();
  const { from, to } = periodWindow(f, now);
  q.set("from", from.toISOString());
  q.set("to", to.toISOString());
  if (opts.step) q.set("step", facetStep(from, to));
  const clients = opts.clientID
    ? [opts.clientID]
    : opts.except === "clients"
      ? []
      : f.clients;
  for (const id of clients) {
    if (id) q.append("client_id", id);
    else q.set("unmatched", "true");
  }
  if (opts.except !== "methods")
    for (const m of f.methods) q.append("method", m);
  if (opts.except !== "reasons")
    for (const r of f.reasons) q.append("deny_reason", r);
  if (opts.except !== "result") {
    if (f.result === "denied") q.set("failures_only", "true");
    if (f.result === "allowed") q.set("allowed_only", "true");
  }
  const text = f.search.trim();
  if (text && isAddress(text)) q.set("source_ip", text);
  const destination = f.host ?? (text && !isAddress(text) ? text : "");
  if (destination) q.set("destination", destination);
  return q;
}

// activeCount is the number of narrowing filters besides period and view.
export function activeCount(f: Filters): number {
  return (
    f.clients.length +
    f.methods.length +
    f.reasons.length +
    (f.result !== "all" ? 1 : 0) +
    (f.host ? 1 : 0) +
    (f.search.trim() ? 1 : 0)
  );
}

export type JournalState = { view: View; filters: Filters };

// encodeState/decodeState carry the journal state in the URL hash and in the
// "log:<json>" intent. Anything malformed decodes to null.
export function encodeState(s: JournalState): string {
  return JSON.stringify({ v: s.view, f: s.filters });
}

export function decodeState(raw: string): JournalState | null {
  try {
    const v = JSON.parse(raw) as { v?: unknown; f?: Partial<Filters> };
    if (v.v !== "events" && v.v !== "domains") return null;
    const f = v.f ?? {};
    const strings = (x: unknown) =>
      Array.isArray(x)
        ? x.filter((s): s is string => typeof s === "string")
        : [];
    const base = defaultFilters();
    const period = (PERIODS as string[]).includes(String(f.period))
      ? (f.period as Period)
      : f.period === "custom" &&
          typeof f.range?.from === "string" &&
          typeof f.range?.to === "string"
        ? "custom"
        : base.period;
    return {
      view: v.v,
      filters: {
        search: typeof f.search === "string" ? f.search : "",
        host: typeof f.host === "string" && f.host ? f.host : undefined,
        clients: strings(f.clients),
        methods: strings(f.methods),
        reasons: strings(f.reasons),
        result:
          f.result === "denied" || f.result === "allowed" ? f.result : "all",
        period,
        range:
          period === "custom"
            ? { from: String(f.range!.from), to: String(f.range!.to) }
            : undefined,
      },
    };
  } catch {
    return null;
  }
}

export function stateFromHash(hash: string): JournalState | null {
  const m = hash.match(/^#log=(.+)$/);
  if (!m) return null;
  try {
    return decodeState(decodeURIComponent(m[1]));
  } catch {
    return null;
  }
}

export function hashFor(s: JournalState): string {
  return "#log=" + encodeURIComponent(encodeState(s));
}

// logIntent builds the section intent that opens the journal on a state.
export function logIntent(s: JournalState): `log:${string}` {
  return `log:${encodeState(s)}`;
}

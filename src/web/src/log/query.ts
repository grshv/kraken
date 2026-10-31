// Labels and classification shared by the journal: deny reasons from
// squid.conf and the denial test the server uses. The API contract is in
// api/openapi.json (Event and EventFacets schemas).

export type LogEvent = {
  proxy_result: string;
  proxy_http_status: number | null;
};

// DENY_REASONS mirror policy.DenyReasons: the protective or final squid.conf
// rule that denied a request, logged at the time. proxy_auth (407) and other
// (expert rule or older record) come only from the facet, not as a filter.
export const DENY_REASONS: Record<string, string> = {
  port: "Запрещённый порт",
  connect_port: "CONNECT на запрещённый порт",
  manager: "Служебный адрес Squid",
  protected: "Защищённый адрес",
  private: "Внутренний адрес",
  blocked: "Запрещено для всех",
  tls_required: "Нужен TLS с логином",
  no_rule: "Нет правила для клиента",
};
const OTHER_REASONS: Record<string, string> = {
  proxy_auth: "Нет или неверный пароль прокси",
  other: "Другое правило",
};
export function denyReasonLabel(r: string): string {
  return DENY_REASONS[r] ?? OTHER_REASONS[r] ?? r;
}

// isDenied mirrors the server's failure classification (failures_only):
// DENIED/FAIL in proxy_result or HTTP status >= 400.
export function isDenied(e: LogEvent): boolean {
  const r = (e.proxy_result || "").toUpperCase();
  if (r.includes("DENIED") || r.includes("FAIL")) return true;
  if (e.proxy_http_status != null && e.proxy_http_status >= 400) return true;
  return false;
}

// FacetsResponse mirrors the GET /events/facets response body.
export type FacetsResponse = {
  allowed: number;
  denied: number;
  clients: { id: string; name: string | null; count: number }[];
  methods: { method: string; count: number }[];
  histogram: { start: string; allowed: number; denied: number }[];
  deny_reasons?: { reason: string; count: number }[];
  site_errors?: { class: string; count: number }[];
};

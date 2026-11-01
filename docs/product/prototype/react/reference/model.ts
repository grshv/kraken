import type { NavId } from "./shell/nav";
export type PanelRole = "admin" | "manager";
export type ApiRequest = <T>(
  path: string,
  method?: string,
  data?: unknown,
  key?: string,
  signal?: AbortSignal,
) => Promise<T>;

export type Destination = { domain: string; include_subdomains: boolean };
// Access: the union of attached policies and own domains, the whole web
// (protections and blocked-for-all still apply) or nothing.
export type Access = "list" | "all_web" | "none";
export type Client = {
  kind?: "host" | "network" | "group";
  id: string;
  name: string;
  description?: string;
  source_networks: string[];
  enabled: boolean;
  proxy_username?: string;
  access: Access;
  // HTTP or TLS listener (TLS is required for a proxy login).
  tls_profile_id: string;
  // Attached shared domain sets and the client's own domains.
  policy_ids: string[];
  destinations: Destination[];
};
// Policy is a named, shareable set of allowed domains.
export type Policy = {
  id: string;
  name: string;
  destinations: Destination[];
};
// Change is one user-visible difference between two snapshots, as returned by
// the preview endpoint alongside summary.
export type Change = {
  entity: "client" | "policy" | "settings" | "configuration" | "tls_profiles";
  id: string | null;
  action: "add" | "mod" | "del";
  text: string;
  details?: string[];
};
export type Snapshot = {
  settings?: Settings;
  configuration_mode?: "structured" | "expert";
  expert_config?: string;
  schema_version: number;
  clients: Client[];
  policies: Policy[];
  tls_profiles: {
    id: string;
    name: string;
    mode: string;
    proxy_transport: string;
    cert_ref: string | null;
  }[];
};
export type Draft = { version: number; snapshot: Snapshot; updated_at: string };
export type Session = {
  authenticated: boolean;
  role: PanelRole | null;
  permissions: string[];
  username: string | null;
  csrf_token: string;
  // OpenAPI sends RFC3339 for authenticated sessions and null for anonymous.
  // Type drift here previously meant callers that decoded it as if always
  // present silently dropped the value.
  expires_at: string | null;
  // Managers get their assigned networks and client IDs; admins/null get none.
  scope?: { networks: string[]; client_ids: string[] } | null;
};
export type Component = {
  state: "up" | "down" | "unknown" | "degraded";
  detail: string;
  observed_at: string | null;
  stale: boolean;
};
export type Status = {
  has_unapplied_changes: boolean | null;
  runtime_update_available: boolean | null;
  web: Component;
  runtime: Component;
  squid: Component;
  applied_revision: string | null;
  applied_revision_number?: number | null;
  // observed_config_hash was missing from the previous model; the UI does not
  // display it but including it keeps the type aligned with OpenAPI so future
  // fields land at compile time rather than runtime.
  observed_config_hash: string | null;
  capabilities: string[];
  ingestion?: { state: string; observed_at: string };
  // instance reports KRAKEN_INSTANCE_NAME and data-volume/journal freshness.
  instance: {
    name: string;
    data_free_bytes: number | null;
    data_total_bytes: number | null;
    journal_last_event_at: string | null;
    journal_checkpoint_at: string | null;
    last_applied_at: string | null;
  };
};
export type Preview = {
  draft_version?: number;
  user_changes: boolean;
  runtime_changes: boolean;
  summary: string[];
  changes?: Change[];
  config_diff: { kind: string; text: string }[];
  revision_id?: string;
  revision_number?: number;
  policy_hash: string;
  generated_config: string;
  warnings: string[];
  expected_applied_revision: string | null;
};
export type Operation = {
  id: string;
  action: string;
  revision_id: string;
  status: string;
  findings: string[];
  created_at: string;
  updated_at: string;
};
export type Event = {
  // Fields beyond what the UI currently renders are typed but not used; this
  // closes a type-drift gap where the runtime returned id/client_id/tls_mode
  // etc. but the model declared only title/source/destination, so any future
  // column would have surfaced as a runtime error instead of compile-time.
  id: string;
  timestamp: string;
  client_id: string | null;
  client_name: string | null;
  proxy_username?: string | null;
  client_match_basis: string;
  source_ip: string;
  destination: string;
  method: string;
  proxy_result: string;
  proxy_http_status: number | null;
  duration_ms: number | null;
  bytes_observed: number | null;
  count: number;
  tls_mode: "unknown" | "tunnel";
  revision_id: string | null;
  deny_reason?: string | null;
};

// UpstreamProxy routes client traffic through a parent Squid peer; exclusive
// forces never_direct so Squid never bypasses the peer.
export type UpstreamProxy = {
  address: string;
  port: number;
  exclusive: boolean;
};
export type Settings = {
  http_port: number;
  tls_port: number;
  safe_ports: number[];
  connect_ports: number[];
  cache_enabled: boolean;
  cache_memory_mb: number;
  maximum_object_kb: number;
  dns_nameservers: string[];
  positive_dns_ttl_seconds: number;
  negative_dns_ttl_seconds: number;
  connect_timeout_seconds: number;
  read_timeout_seconds: number;
  request_timeout_seconds: number;
  max_request_body_mb: number;
  log_rotations: number;
  allow_private_destinations: boolean;
  // blocked_domains denied for every client before user policy applies.
  blocked_domains?: Destination[];
  // hide_client_address strips the client address from forwarded requests.
  hide_client_address?: boolean;
  // upstream_proxy sends traffic through a parent proxy; null = go direct.
  upstream_proxy?: UpstreamProxy | null;
};
// revisionLabel shows a revision as r-<number>; revisions without a number
// (none after migration 006) fall back to the UUID prefix.
export function revisionLabel(id: string, number?: number | null): string {
  if (number) return `r-${number}`;
  return id.length > 8 ? id.slice(0, 8) : id;
}

export type RevisionSummary = {
  id: string;
  number: number;
  created_at: string;
  policy_hash: string;
  compiler_version: string;
  was_applied: boolean;
  is_current: boolean;
  can_rollback: boolean;
  latest_status: string;
};
export type Revision = {
  id: string;
  number: number;
  draft_version: number;
  policy_hash: string;
  compiler_version: string;
  // Server today returns image_digest only on the immutable revision object.
  // The UI does not display it; kept optional for forward compatibility.
  image_digest?: string;
  created_at: string;
};

// Help index row and full article mirror the OpenAPI HelpEntry/HelpArticle
// schemas. Markdown bodies are rendered by the small safe renderer in
// help/markdown.ts, never via dangerouslySetInnerHTML with raw HTML.
export type HelpEntry = {
  slug: string;
  title: string;
  section: string;
  order: number;
  // screen is the UI section the article describes ("Открыть в интерфейсе").
  screen?: NavId;
};
export type HelpArticle = HelpEntry & { markdown: string };
export const defaultSettings: Settings = {
  http_port: 3128,
  tls_port: 3129,
  safe_ports: [80, 443],
  connect_ports: [443],
  cache_enabled: false,
  cache_memory_mb: 64,
  maximum_object_kb: 512,
  dns_nameservers: [],
  positive_dns_ttl_seconds: 21600,
  negative_dns_ttl_seconds: 60,
  connect_timeout_seconds: 60,
  read_timeout_seconds: 900,
  request_timeout_seconds: 300,
  max_request_body_mb: 0,
  log_rotations: 3,
  allow_private_destinations: false,
};

// Russian plural: pluralRu(3, "отказ", "отказа", "отказов") === "3 отказа".
export function pluralRu(
  n: number,
  one: string,
  few: string,
  many: string,
): string {
  const abs = Math.abs(n);
  const mod10 = abs % 10;
  const mod100 = abs % 100;
  if (mod10 === 1 && mod100 !== 11) return `${n} ${one}`;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14))
    return `${n} ${few}`;
  return `${n} ${many}`;
}

// localTime renders an RFC 3339 timestamp as local wall-clock time ("14:02:11"
// or "14:02"); slicing the ISO string would show UTC.
export function localTime(iso: string, seconds = true): string {
  const d = timestampDate(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString("ru-RU", {
    timeZone: "Europe/Moscow",
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" } : {}),
  });
}

// ago renders a past timestamp as "сейчас", "N мин", "N ч" or "N д".
export function ago(iso: string | null | undefined): string {
  if (!iso) return "—";
  const min = Math.max(0, Math.round((Date.now() - timestampDate(iso).getTime()) / 60000));
  if (min < 1) return "сейчас";
  if (min < 60) return `${min} мин`;
  if (min < 48 * 60) return `${Math.round(min / 60)} ч`;
  return `${Math.round(min / 1440)} д`;
}

// All diagnostic timestamps use the same Moscow wall-clock, including fixtures
// without a UTC offset. Compact rows omit the year; exact tooltips retain it.
export function timestampDate(iso:string):Date {
  return new Date(/T\d{2}:\d{2}/.test(iso)&&!/(?:Z|[+-]\d{2}:\d{2})$/i.test(iso)?iso+'+03:00':iso);
}
export function dateTime(iso:string|null|undefined,seconds=true,compact=false):string {
  if(!iso)return '—';const date=timestampDate(iso);if(Number.isNaN(date.getTime()))return '—';
  const parts=Object.fromEntries(new Intl.DateTimeFormat('ru-RU',{timeZone:'Europe/Moscow',day:'2-digit',month:'2-digit',year:'numeric',hour:'2-digit',minute:'2-digit',...(seconds?{second:'2-digit'}:{})}).formatToParts(date).map(p=>[p.type,p.value]));
  return parts.day+'.'+parts.month+(compact?'':'.'+parts.year)+' '+parts.hour+':'+parts.minute+(seconds?':'+parts.second:'');
}

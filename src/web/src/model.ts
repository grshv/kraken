export type PanelRole = "admin" | "manager";
export type ApiRequest = <T>(
  path: string,
  method?: string,
  data?: unknown,
  key?: string,
  signal?: AbortSignal,
) => Promise<T>;

export type Destination = { domain: string; include_subdomains: boolean };
export type Client = {
  kind?: "host" | "network" | "group";
  id: string;
  name: string;
  description?: string;
  source_networks: string[];
  enabled: boolean;
  policy_id: string;
  proxy_username?: string;
};
export type Policy = {
  id: string;
  name: string;
  mode: "all_web" | "allowlist";
  destinations: Destination[];
  tls_profile_id: string;
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
  // observed_config_hash was missing from the previous model; the UI does not
  // display it but including it keeps the type aligned with OpenAPI so future
  // fields land at compile time rather than runtime.
  observed_config_hash: string | null;
  capabilities: string[];
};
export type Preview = {
  draft_version?: number;
  user_changes: boolean;
  runtime_changes: boolean;
  summary: string[];
  config_diff: { kind: string; text: string }[];
  revision_id?: string;
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
  tls_mode: "unknown" | "tunnel";
  revision_id: string | null;
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
};
export type RevisionSummary = {
  id: string;
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

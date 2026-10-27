export type Destination = { domain: string; include_subdomains: boolean };
export type Client = {
  kind?: "host" | "network" | "group";
  id: string;
  name: string;
  source_networks: string[];
  enabled: boolean;
  policy_id: string;
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
    ca_ref: string | null;
    bypass_domains: Destination[];
  }[];
};
export type Draft = { version: number; snapshot: Snapshot; updated_at: string };
export type Session = {
  authenticated: boolean;
  username: string | null;
  csrf_token: string;
};
export type Component = {
  state: string;
  detail: string;
  observed_at: string;
  stale: boolean;
};
export type Status = {
  has_unapplied_changes: boolean | null;
  web: Component;
  runtime: Component;
  squid: Component;
  applied_revision: string | null;
  capabilities: string[];
};
export type Preview = {
  summary: string[];
  config_diff: { kind: string; text: string }[];
  revision_id: string;
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
};
export type Event = {
  timestamp: string;
  source_ip: string;
  proxy_result: string;
  method: string;
  destination: string;
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

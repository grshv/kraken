package telemetry

// eventsSchema is the DDL for a monthly events-YYYY-MM.db file. host is a
// dictionary of bare destination hostnames; event references it by id. The
// (source_ip, ts) partial index covers unknown-source queries only. source_id
// preserves the input event id (the access-log line hash or a fixture handle)
// so the explain endpoint can look up an event by its legacy id; the API
// exposes the compact <YYYYMM>-<rowid> form, not source_id.
const eventsSchema = `
CREATE TABLE IF NOT EXISTS host (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS event (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  client_id TEXT,
  client_name TEXT,
  source_ip TEXT NOT NULL,
  host_id INTEGER NOT NULL REFERENCES host(id),
  port INTEGER,
  method TEXT,
  result TEXT,
  http_status INTEGER,
  duration_ms INTEGER,
  bytes INTEGER,
  count INTEGER NOT NULL DEFAULT 1,
  failed INTEGER NOT NULL DEFAULT 0,
  proxy_username TEXT,
  revision_id TEXT,
  match_basis TEXT,
  tls_mode TEXT,
  source_id TEXT,
  deny_reason TEXT
);
CREATE INDEX IF NOT EXISTS event_ts ON event(ts);
CREATE INDEX IF NOT EXISTS event_client_ts ON event(client_id, ts);
CREATE INDEX IF NOT EXISTS event_source_ts ON event(source_ip, ts) WHERE client_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS event_source_id ON event(source_id);
CREATE TABLE IF NOT EXISTS checkpoint (k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

// rollupsSchema is the DDL for rollups.db. All tables are WITHOUT ROWID so
// UPSERTs aggregate in place and there is no rowid bloat. watermark records how
// far each month has been rolled up.
const rollupsSchema = `
CREATE TABLE IF NOT EXISTS client_15m (
  bucket INTEGER NOT NULL,
  client_id TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL,
  PRIMARY KEY(bucket, client_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS client_host_1h (
  bucket INTEGER NOT NULL,
  client_id TEXT NOT NULL,
  host TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL,
  PRIMARY KEY(bucket, client_id, host)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS client_host_1d (
  day INTEGER NOT NULL,
  client_id TEXT NOT NULL,
  host TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL,
  PRIMARY KEY(day, client_id, host)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS client_host_1d_client_day ON client_host_1d(client_id, day);
CREATE TABLE IF NOT EXISTS unknown_15m (
  bucket INTEGER NOT NULL,
  source_ip TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL,
  PRIMARY KEY(bucket, source_ip)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS unknown_host_1h (
  bucket INTEGER NOT NULL,
  source_ip TEXT NOT NULL,
  host TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL,
  PRIMARY KEY(bucket, source_ip, host)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS unknown_host_1d (
  day INTEGER NOT NULL,
  source_ip TEXT NOT NULL,
  host TEXT NOT NULL,
  requests INTEGER NOT NULL,
  denied INTEGER NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL,
  PRIMARY KEY(day, source_ip, host)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS watermark (month TEXT PRIMARY KEY, last_event_id INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS facet_15m (
  bucket INTEGER NOT NULL,
  client_id TEXT NOT NULL,
  source_ip TEXT NOT NULL,
  method TEXT NOT NULL,
  proxy_result TEXT NOT NULL,
  failed INTEGER NOT NULL,
  requests INTEGER NOT NULL,
  PRIMARY KEY(bucket, client_id, source_ip, method, proxy_result, failed)
) WITHOUT ROWID;
`

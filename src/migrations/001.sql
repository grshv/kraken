PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL);
-- Anonymous CSRF-holder sessions use username='' so no FK can reference users
-- here without breaking the unauthenticated GET /session flow. Integrity is
-- enforced at the application layer (token_hash is sha256 of a 128-byte random
-- token, never persisted in plaintext).
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  csrf TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_username_idx ON sessions(username);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS operations (
  id TEXT PRIMARY KEY,
  idempotency_key TEXT UNIQUE NOT NULL,
  request_hash TEXT NOT NULL,
  body TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS operations_revision_idx ON operations(json_extract(body, '$.revision_id'));
CREATE TABLE IF NOT EXISTS audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS audit_events_action_idx ON audit_events(action);
INSERT OR IGNORE INTO schema_migrations(version, checksum, applied_at)
  VALUES (1, '7d2b8e0b2b2d2b27c2c8e3d2b27c2c8e3d2b27c2c8e3d2b27c2c8e3d2b27c2c8', strftime('%Y-%m-%dT%H:%M:%SZ','now'));

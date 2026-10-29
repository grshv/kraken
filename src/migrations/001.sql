-- Baseline schema. Later migrations append numbered files; never edit a shipped one.
CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE users (
 username TEXT PRIMARY KEY,
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL DEFAULT 'admin' CHECK(role IN ('admin','viewer')),
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1))
);
-- Only authenticated sessions are stored; token_hash is sha256 of a random token.
CREATE TABLE sessions (
 token_hash TEXT PRIMARY KEY,
 username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
 csrf TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 last_seen_at INTEGER NOT NULL
);
CREATE INDEX sessions_username_idx ON sessions(username);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);
CREATE TABLE operations (
 id TEXT PRIMARY KEY,
 idempotency_key TEXT UNIQUE NOT NULL,
 request_hash TEXT NOT NULL,
 body TEXT NOT NULL
);
CREATE INDEX operations_revision_idx ON operations(json_extract(body, '$.revision_id'));
CREATE TABLE audit_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 actor TEXT NOT NULL,
 action TEXT NOT NULL,
 entity_id TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE INDEX audit_events_created_idx ON audit_events(created_at);
CREATE INDEX audit_events_action_idx ON audit_events(action);
CREATE TABLE proxy_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL UNIQUE,
 client_id TEXT,
 client_name TEXT,
 failed INTEGER NOT NULL,
 ingested_at INTEGER NOT NULL,
 body TEXT NOT NULL
);
CREATE INDEX proxy_events_client_seq ON proxy_events(client_id,seq);
CREATE INDEX proxy_events_failed_seq ON proxy_events(failed,seq);
CREATE INDEX proxy_events_ingested ON proxy_events(ingested_at);
CREATE TABLE proxy_accounts (
 username TEXT PRIMARY KEY,
 password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);

-- Roles: replace viewer with manager and add manager scope tables.
-- users is recreated so the CHECK constraint accepts 'manager'. FK cascade on
-- sessions would drop active sessions when users is dropped, so sessions are
-- saved to a temp table and restored after the parent is recreated.
CREATE TABLE users_new (
 username TEXT PRIMARY KEY,
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL DEFAULT 'admin' CHECK(role IN ('admin','manager')),
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1))
);
INSERT INTO users_new(username, password_hash, role, enabled)
 SELECT username, password_hash, CASE WHEN role='viewer' THEN 'manager' ELSE role END, enabled FROM users;
CREATE TABLE sessions_backup AS SELECT * FROM sessions;
DROP TABLE sessions;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
CREATE TABLE sessions (
 token_hash TEXT PRIMARY KEY,
 username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
 csrf TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 last_seen_at INTEGER NOT NULL
);
CREATE INDEX sessions_username_idx ON sessions(username);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);
INSERT INTO sessions(token_hash, username, csrf, expires_at, last_seen_at)
 SELECT token_hash, username, csrf, expires_at, last_seen_at FROM sessions_backup;
DROP TABLE sessions_backup;
CREATE TABLE manager_networks (
 username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
 cidr TEXT NOT NULL,
 PRIMARY KEY(username, cidr)
);
CREATE TABLE manager_clients (
 username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 PRIMARY KEY(username, client_id)
);
ALTER TABLE proxy_accounts ADD COLUMN owner TEXT;

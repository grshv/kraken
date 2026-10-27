CREATE TABLE proxy_accounts (
 username TEXT PRIMARY KEY,
 password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
INSERT INTO schema_migrations(version,checksum,applied_at)
 VALUES(5,'proxy-accounts-v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));

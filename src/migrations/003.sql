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
INSERT INTO schema_migrations(version,checksum,applied_at)
 VALUES(3,'diagnostics-v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));

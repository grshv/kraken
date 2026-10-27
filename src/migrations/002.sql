-- Migration 002: standardize audit_events.created_at on INTEGER unix seconds.
--
-- Previously audit_events.created_at was TEXT RFC3339 while sessions.expires_at
-- and sessions.last_seen_at were INTEGER unix. Mixed types block range queries
-- and forced Prune to compare lexically-formatted RFC3339 strings instead of
-- integers. The kv-blob MVP established in 001.sql keeps TEXT for entity bodies
-- (JSON) but timestamps as a column should be integers.
--
-- SQLite cannot ALTER COLUMN type in place, so the standard rebuild pattern is:
-- 1. Create the new table with the desired shape.
-- 2. Copy rows from the old table, parsing RFC3339 to epoch seconds via
--    strftime('%s', ...). Rows with unparseable or NULL created_at fall back
--    to 0 so the migration is total rather than failing on legacy data.
-- 3. Drop the old table.
-- 4. Rename the new table into place.
-- 5. Recreate indexes (drop+create is safe because the table was just renamed).
CREATE TABLE IF NOT EXISTS audit_events_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
INSERT INTO audit_events_new (id, actor, action, entity_id, created_at)
  SELECT
    id,
    actor,
    action,
    entity_id,
    COALESCE(CAST(strftime('%s', created_at) AS INTEGER), 0)
  FROM audit_events;
DROP TABLE audit_events;
ALTER TABLE audit_events_new RENAME TO audit_events;
CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS audit_events_action_idx ON audit_events(action);
INSERT OR IGNORE INTO schema_migrations (version, checksum, applied_at)
  VALUES (2, '3f1a9c4e8b2d7f5a6e0c1b4d8a7f2e3c5b6a9d0e1f2a3b4c5d6e7f8a9b0c1d2e3', strftime('%Y-%m-%dT%H:%M:%SZ','now'));

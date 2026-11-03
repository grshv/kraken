-- Telemetry split: proxy events move out of the control DB into monthly
-- SQLite files under the telemetry directory (internal/telemetry). The
-- event_checkpoint kv key is now owned by the telemetry store. Drop the
-- proxy_events table and its indexes; audit_events, operations and kv stay.
DROP INDEX IF EXISTS proxy_events_client_ingested;
DROP INDEX IF EXISTS proxy_events_client_seq;
DROP INDEX IF EXISTS proxy_events_failed_seq;
DROP INDEX IF EXISTS proxy_events_ingested;
DROP TABLE IF EXISTS proxy_events;
DELETE FROM kv WHERE key='event_checkpoint';

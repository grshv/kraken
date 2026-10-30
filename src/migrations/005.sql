-- Notification marks. Notifications themselves are computed from telemetry;
-- only the shared "seen"/"ignored" mark is stored, keyed by notification id
-- ("denied/<client_id>/<host>" or "source/<ip>"). Prune drops marks older
-- than 90 days.
CREATE TABLE notification_marks (
 id TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK(status IN ('seen','ignored')),
 actor TEXT NOT NULL,
 marked_at INTEGER NOT NULL
);
CREATE INDEX notification_marks_marked_idx ON notification_marks(marked_at);

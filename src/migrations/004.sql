ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin' CHECK(role IN ('admin','viewer'));
ALTER TABLE users ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1));
INSERT INTO schema_migrations(version,checksum,applied_at)
 VALUES(4,'panel-users-roles-v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));

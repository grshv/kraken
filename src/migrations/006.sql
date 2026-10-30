-- Revision numbers: a counter in kv ('revision_seq'), assigned in the same
-- transaction as the revision. Existing revisions are numbered by insertion order.
UPDATE kv SET value = json_set(value, '$.number',
 (SELECT COUNT(*) FROM kv k WHERE k.key LIKE 'revision:%' AND k.rowid <= kv.rowid))
 WHERE key LIKE 'revision:%';
INSERT INTO kv(key, value) SELECT 'revision_seq', CAST(COUNT(*) AS TEXT) FROM kv WHERE key LIKE 'revision:%';

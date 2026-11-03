-- Audit detail: a short human summary of the change (untrusted display text).
ALTER TABLE audit_events ADD COLUMN detail TEXT NOT NULL DEFAULT '';

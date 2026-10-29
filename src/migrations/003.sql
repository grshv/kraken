-- Inspector rollups filter by client_id plus a coarse ingested_at bound before
-- decoding JSON bodies. The baseline (client_id, seq) index cannot apply the
-- ingested_at predicate, so a dedicated (client_id, ingested_at) index keeps the
-- 50k retained events worst case under the inspector's second budget.
CREATE INDEX proxy_events_client_ingested ON proxy_events(client_id, ingested_at);

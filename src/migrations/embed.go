package migrations

import _ "embed"

// Migration is one numbered SQL migration applied in order by store.Open.
// Checksum is recorded in schema_migrations; a mismatch means the database
// was created by an incompatible build.
type Migration struct {
	Version  int
	SQL      string
	Checksum string
}

//go:embed 001.sql
var sql001 string

//go:embed 003.sql
var sql003 string

// Ordered is the canonical list applied at store.Open. New migrations append
// here with increasing Version; never edit a shipped entry's SQL or Checksum.
// Version 2 is reserved for slice 9 (manager roles) and is intentionally
// absent here; migrate applies each entry by version, not by sequence.
var Ordered = []Migration{
	{Version: 1, SQL: sql001, Checksum: "baseline-v1"},
	{Version: 3, SQL: sql003, Checksum: "inspector-v1"},
}

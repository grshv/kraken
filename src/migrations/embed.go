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

//go:embed 002.sql
var sql002 string

//go:embed 003.sql
var sql003 string

//go:embed 004.sql
var sql004 string

//go:embed 005.sql
var sql005 string

//go:embed 006.sql
var sql006 string

// Ordered is the canonical list applied at store.Open. New migrations append
// here with increasing Version; never edit a shipped entry's SQL or Checksum.
var Ordered = []Migration{
	{Version: 1, SQL: sql001, Checksum: "baseline-v1"},
	{Version: 2, SQL: sql002, Checksum: "roles-v1"},
	{Version: 3, SQL: sql003, Checksum: "inspector-v1"},
	{Version: 4, SQL: sql004, Checksum: "telemetry-split-v1"},
	{Version: 5, SQL: sql005, Checksum: "notification-marks-v1"},
	{Version: 6, SQL: sql006, Checksum: "revision-numbers-v1"},
}

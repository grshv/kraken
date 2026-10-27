package migrations

import _ "embed"

// Migration is one numbered SQL migration applied in order by store.Open.
// Checksum is a stable identifier recorded in schema_migrations so a future
// schema drift can be detected; it is not a security tag.
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

// Initial preserves the original v1 entrypoint for callers that only need
// the first migration (kept as a stable alias for tests or external tools).
var Initial = sql001

// Ordered is the canonical list applied at store.Open. New migrations append
// here with monotonically increasing Version and a fresh checksum; never edit
// an already-shipped entry's SQL or Checksum.
var Ordered = []Migration{
	{Version: 1, SQL: sql001, Checksum: "7d2b8e0b2b2d2b27c2c8e3d2b27c2c8e3d2b27c2c8e3d2b27c2c8e3d2b27c2c8"},
	{Version: 2, SQL: sql002, Checksum: "3f1a9c4e8b2d7f5a6e0c1b4d8a7f2e3c5b6a9d0e1f2a3b4c5d6e7f8a9b0c1d2e3"},
	{Version: 3, SQL: sql003, Checksum: "diagnostics-v1"},
	{Version: 4, SQL: sql004, Checksum: "panel-users-roles-v1"},
	{Version: 5, SQL: sql005, Checksum: "proxy-accounts-v1"},
}

package main

import (
	"context"
	"log/slog"
	"time"

	"kraken.local/kraken/internal/httpapi"
	local "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/store"
)

// autoServiceUpdate re-renders the applied snapshot with the current
// installation options (the proxy certificate paths) and applies it through
// the normal journaled pipeline when the text differs, for example when an
// existing installation gets the issued proxy certificate. The draft is never
// read. A failure leaves the previous configuration serving.
func autoServiceUpdate(ctx context.Context, st *store.Store, m *local.Manager) {
	needed, e := httpapi.ServiceUpdateNeeded(m)
	if e != nil {
		slog.Warn("cannot check installation options against applied configuration", "error", e)
		return
	}
	if !needed {
		return
	}
	slog.Info("installation options changed; applying service update")
	c, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	op, e := httpapi.ApplyServiceUpdate(c, st, m, baseImage)
	if e != nil {
		slog.Error("automatic service update failed; previous configuration kept", "operation_id", op.ID, "error", e)
		return
	}
	slog.Info("automatic service update applied", "operation_id", op.ID, "status", op.Status)
}

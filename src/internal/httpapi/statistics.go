package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) statistics(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	result, err := s.Store.EventStatistics(ctx, time.Now())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "statistics_unavailable", "Статистика временно недоступна")
		return
	}
	respond(w, http.StatusOK, result)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request, v session) {
	minutes := 60
	if raw := r.URL.Query().Get("minutes"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != 15 && value != 60 && value != 1440 && value != 10080 && value != 43200) {
			fail(w, 400, "invalid_window", "Выберите 15 минут, час, день, неделю или месяц")
			return
		}
		minutes = value
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	result, err := s.Store.RecentActivity(ctx, time.Now(), minutes)
	if err != nil {
		fail(w, 503, "activity_unavailable", "Активность клиентов временно недоступна")
		return
	}
	cp, err := s.Store.EventCheckpoint()
	if err != nil {
		fail(w, 503, "activity_unavailable", "Состояние журнала недоступно")
		return
	}
	var applied any
	manifest, err := s.Runtime.ReadManifest()
	if err == nil {
		enabled, allowlist, allweb := 0, 0, 0
		for _, c := range manifest.Snapshot.Clients {
			if c.Enabled {
				enabled++
				for _, p := range manifest.Snapshot.Policies {
					if p.ID == c.PolicyID {
						if p.Mode == "all_web" {
							allweb++
						} else {
							allowlist++
						}
					}
				}
			}
		}
		applied = map[string]any{"mode": manifest.Snapshot.Mode, "enabled_clients": enabled, "allowlist_clients": allowlist, "all_web_clients": allweb}
	}
	respond(w, 200, map[string]any{"activity": result, "applied": applied, "ingestion": map[string]any{"observed_at": cp.ObservedAt, "source_available": cp.SourceAvailable, "gap": cp.Gap, "backlog": cp.Backlog, "dropped_records": cp.Dropped}})
}

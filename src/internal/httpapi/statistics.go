package httpapi

import (
	"context"
	"net/http"
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

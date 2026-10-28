package httpapi

import "net/http"

func (s *Server) squidMetrics(w http.ResponseWriter, r *http.Request, _ session) {
	w.Header().Set("Cache-Control", "no-store")
	respond(w, http.StatusOK, s.Runtime.Metrics(r.Context()))
}

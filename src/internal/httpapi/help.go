package httpapi

import (
	"net/http"

	"kraken.local/kraken/internal/help"
)

// helpIndex returns the sorted article index. Available to any
// authenticated user; the help tree is shown to managers with an empty
// scope as well, so the read permission is sufficient.
func (s *Server) helpIndex(w http.ResponseWriter, r *http.Request, _ session) {
	respond(w, 200, map[string]any{"articles": help.Index()})
}

// helpArticle returns one article by slug. Unknown slug surfaces as the
// standard not_found envelope used by other read endpoints.
func (s *Server) helpArticle(w http.ResponseWriter, r *http.Request, _ session) {
	a, ok := help.Article(r.PathValue("slug"))
	if !ok {
		fail(w, 404, "not_found", "Статья не найдена")
		return
	}
	respond(w, 200, a)
}

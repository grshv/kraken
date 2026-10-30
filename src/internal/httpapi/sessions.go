package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode"

	"kraken.local/kraken/internal/store"
)

// sessionIdle matches the idle cutoff in newSession: a session not seen for
// 30 minutes is no longer listed (and is deleted on the next login).
const sessionIdle = 30 * 60

// sessionClient records where a login came from. The User-Agent is bounded
// and stripped of control characters; it is display data, not identity.
func sessionClient(r *http.Request) store.SessionClient {
	ua := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, r.UserAgent())
	if runes := []rune(ua); len(runes) > 256 {
		ua = string(runes[:256])
	}
	return store.SessionClient{Remote: remoteHost(r.RemoteAddr), UserAgent: ua}
}

// publicSessionID derives the id shown in the API from the token hash. It is
// a one-way value: neither the token nor its stored hash can be recovered.
func publicSessionID(tokenHash string) string {
	sum := sha256.Sum256([]byte("kraken-session-id\x00" + tokenHash))
	return hex.EncodeToString(sum[:8])
}

type sessionItem struct {
	ID         string  `json:"id"`
	CreatedAt  *string `json:"created_at"`
	LastSeenAt string  `json:"last_seen_at"`
	ExpiresAt  string  `json:"expires_at"`
	Remote     *string `json:"remote"`
	UserAgent  *string `json:"user_agent"`
	Current    bool    `json:"current"`
}

// sessionOwner allows a user to manage their own sessions and an admin to
// manage anyone's. Others get 404 so usernames are not confirmed.
func sessionOwner(w http.ResponseWriter, r *http.Request, v session) (string, bool) {
	username := r.PathValue("username")
	if username != v.User && v.Role != "admin" {
		fail(w, 404, "not_found", "Пользователь не найден")
		return "", false
	}
	return username, true
}

func (s *Server) liveSessions(ctx context.Context, username string) ([]store.SessionInfo, error) {
	now := time.Now().Unix()
	return s.Store.UserSessions(ctx, username, now, now-sessionIdle)
}

func (s *Server) sessionsUnavailable(w http.ResponseWriter, e error) {
	if s.failStorageBusy(w, e) {
		return
	}
	fail(w, 503, "storage_unavailable", "Хранилище недоступно")
}

// listSessions returns the live sessions of a user; current marks the
// caller's own session.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, v session) {
	username, ok := sessionOwner(w, r, v)
	if !ok {
		return
	}
	rows, e := s.liveSessions(r.Context(), username)
	if e != nil {
		s.sessionsUnavailable(w, e)
		return
	}
	stamp := func(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) }
	items := []sessionItem{}
	for _, row := range rows {
		item := sessionItem{ID: publicSessionID(row.TokenHash), LastSeenAt: stamp(row.LastSeen), ExpiresAt: stamp(row.Expires), Current: row.TokenHash == v.Hash}
		if row.Created > 0 {
			item.CreatedAt = strPtr(stamp(row.Created))
		}
		if row.Remote != "" {
			item.Remote = strPtr(row.Remote)
		}
		if row.UserAgent != "" {
			item.UserAgent = strPtr(row.UserAgent)
		}
		items = append(items, item)
	}
	respond(w, 200, map[string]any{"items": items})
}

// revokeSession ends one session of a user by its public id.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, v session) {
	username, ok := sessionOwner(w, r, v)
	if !ok {
		return
	}
	rows, e := s.liveSessions(r.Context(), username)
	if e != nil {
		s.sessionsUnavailable(w, e)
		return
	}
	id := r.PathValue("id")
	for _, row := range rows {
		if publicSessionID(row.TokenHash) != id {
			continue
		}
		n, e := s.Store.RevokeUserSessions(r.Context(), username, row.TokenHash, "", v.User)
		if e != nil {
			s.sessionsUnavailable(w, e)
			return
		}
		respond(w, 200, map[string]any{"revoked": n})
		return
	}
	fail(w, 404, "not_found", "Сессия не найдена")
}

// revokeSessions ends every session of a user except the caller's own.
func (s *Server) revokeSessions(w http.ResponseWriter, r *http.Request, v session) {
	username, ok := sessionOwner(w, r, v)
	if !ok {
		return
	}
	n, e := s.Store.RevokeUserSessions(r.Context(), username, "", v.Hash, v.User)
	if e != nil {
		s.sessionsUnavailable(w, e)
		return
	}
	respond(w, 200, map[string]any{"revoked": n})
}

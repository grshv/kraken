package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"kraken.local/kraken/internal/credentials"
	"kraken.local/kraken/internal/store"
)

func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// PasswordHash persists only a salted one-way Argon2id PHC verifier.
func PasswordHash(password string) (string, error)  { return credentials.Hash(password) }
func passwordMatches(encoded, password string) bool { return credentials.Matches(encoded, password) }

// ResetPassword changes the verifier and revokes every session atomically.
// It never creates an unknown user and never persists the supplied plaintext.
func ResetPassword(s *store.Store, username, password string) error {
	hash, e := PasswordHash(password)
	if e != nil {
		return e
	}
	tx, e := s.BeginTx()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	n, e := s.UpdateUserPasswordHash(tx, username, hash)
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("administrator not found")
	}
	if e = s.DeleteSessionsByUser(tx, username); e != nil {
		return e
	}
	if e = s.AuditTx(tx, "local-cli", "password_reset", username); e != nil {
		return e
	}
	return tx.Commit()
}

// usernamePattern restricts bootstrap usernames to printable ASCII without
// control characters, whitespace or quote/backslash characters that would
// leak into audit logs as data. SQL is parameterized so this is not an
// injection defense — it is hygiene for downstream rendering.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func Bootstrap(s *store.Store, username, password string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("invalid username")
	}
	h, e := PasswordHash(password)
	if e != nil {
		return e
	}
	tx, e := s.BeginTx()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	n, e := s.UserCount(tx)
	if e != nil {
		return e
	}
	if n > 0 {
		return errors.New("bootstrap already completed")
	}
	if e = s.InsertUser(tx, username, h); e != nil {
		return e
	}
	return tx.Commit()
}

type session struct {
	User, CSRF, Hash, Role string
	Expires                int64
}

func (s *Server) readSession(r *http.Request) (session, error) {
	var v session
	c, e := r.Cookie("__Host-session")
	if e != nil {
		return v, e
	}
	if len(c.Value) > 128 {
		return v, errors.New("invalid cookie")
	}
	if strings.HasPrefix(c.Value, "anon.") {
		return s.readAnonymousSession(c.Value)
	}
	v.Hash = tokenHash(c.Value)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := time.Now().Unix()
	// Read session and user through the reader pool so a session check never
	// takes the write lock. last_seen_at is updated in a short write tx only
	// once per minute (touch throttle); the conditional WHERE closes the reset
	// race — a password reset that deleted the row between read and touch
	// produces 0 affected rows, and we re-check existence so a revoked session
	// can never be revived or reused.
	var last int64
	v.User, v.CSRF, v.Expires, last, e = s.Store.LookupSessionContext(ctx, v.Hash)
	if e != nil {
		return v, e
	}
	if v.User != "" {
		account, e := s.Store.UserContext(ctx, v.User)
		if e != nil {
			return v, e
		}
		if !account.Enabled {
			return v, errors.New("user disabled")
		}
		v.Role = account.Role
	}
	if now >= v.Expires || now-last > 1800 {
		return v, errors.New("session expired")
	}
	if now-last >= 60 {
		touched, e := s.Store.TouchSessionConditional(ctx, v.Hash, last, now)
		if e != nil {
			return v, e
		}
		if !touched {
			exists, e := s.Store.SessionExists(ctx, v.Hash)
			if e != nil {
				return v, e
			}
			if !exists {
				return v, errors.New("session revoked")
			}
			// Another concurrent request touched last_seen_at first; that is fine.
		}
	}
	return v, nil
}

// credentials holds the verified password hash and previous token hash. The
// transaction rechecks the hash before rotating into an authenticated session.
func (s *Server) newSession(w http.ResponseWriter, user string, credentials ...string) (session, error) {
	if user == "" {
		return s.newAnonymousSession(w), nil
	}
	token := rand.Text()
	v := session{User: user, CSRF: rand.Text(), Hash: tokenHash(token), Expires: time.Now().Add(12 * time.Hour).Unix()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, e := s.Store.BeginTxContext(ctx)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if user != "" {
		if len(credentials) != 2 {
			return v, errors.New("missing credential guard")
		}
		account, e := s.Store.User(tx, user)
		if e != nil {
			return v, e
		}
		if !account.Enabled {
			return v, errCredentialsChanged
		}
		v.Role = account.Role
		currentHash, e := s.Store.UserPasswordHash(tx, user)
		if e != nil {
			return v, e
		}
		if subtle.ConstantTimeCompare([]byte(currentHash), []byte(credentials[0])) != 1 {
			return v, errCredentialsChanged
		}
		if e = s.Store.DeleteSessionTx(tx, credentials[1]); e != nil {
			return v, e
		}
	}
	now := time.Now().Unix()
	if e = s.Store.DeleteExpiredSessions(tx, now, now-30*60); e != nil {
		return v, e
	}
	count, e := s.Store.SessionCount(tx)
	if e != nil {
		return v, e
	}
	// Only authenticated sessions are persisted. Anonymous CSRF holders cannot
	// exhaust this pool and all capacity-path writes use this same transaction.
	if count >= 256 {
		action := "session_capacity_reached"
		actor := user
		if e := s.Store.AuditTx(tx, actor, action, ""); e != nil {
			// Don't rollback the tx for an audit failure — the capacity
			// rejection still needs to commit any cleanup done above. Log
			// and continue; the gate remains enforced.
			slog.Warn("audit write failed", "action", action, "actor", actor, "error", e)
		}
		if ce := tx.Commit(); ce != nil {
			return v, ce
		}
		return v, errors.New("session capacity reached")
	}
	if e = s.Store.InsertSession(tx, v.Hash, user, v.CSRF, v.Expires, now); e != nil {
		return v, e
	}
	if e = s.Store.AuditTx(tx, user, "login", ""); e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	http.SetCookie(w, &http.Cookie{Name: "__Host-session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return v, nil
}

var errCredentialsChanged = errors.New("credentials changed while logging in")

func (s *Server) sessionView(w http.ResponseWriter, v session) {
	var username any
	var expires any
	if v.User != "" {
		username = v.User
		expires = time.Unix(v.Expires, 0).UTC().Format(time.RFC3339)
	}
	out := map[string]any{"authenticated": v.User != "", "username": username, "csrf_token": v.CSRF, "expires_at": expires, "role": func() any {
		if v.Role == "" {
			return nil
		}
		return v.Role
	}(), "permissions": permissions(v.Role)}
	// Managers see their scope; admins do not (null/absent).
	if v.Role == "manager" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		scope, se := s.Store.UserScopeContext(ctx, v.User)
		if se == nil {
			out["scope"] = map[string]any{"networks": scope.Networks, "client_ids": scope.ClientIDs}
		}
	}
	respond(w, 200, out)
}
func (s *Server) sessionGet(w http.ResponseWriter, r *http.Request) {
	v, e := s.readSession(r)
	if e != nil {
		v, e = s.newSession(w, "")
	}
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "session_unavailable", "Сессии временно недоступны")
		return
	}
	s.sessionView(w, v)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	v, e := s.readSession(r)
	if e != nil || !s.csrf(r, v) {
		// CSRF failure on the login form usually means a stale tab; record it so
		// security monitoring can correlate a CSRF spray across accounts.
		s.auditAudit(remoteHost(r.RemoteAddr), "csrf_failed", "")
		fail(w, 403, "csrf_failed", "Обновите страницу входа")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Password) > 1024 || len(input.Username) > 128 {
		fail(w, 400, "invalid_input", "Некорректные данные входа")
		return
	}
	host := remoteHost(r.RemoteAddr)
	// Two independent throttle counters share the same backing map but use
	// distinct key prefixes, so a distributed password-guessing attack that
	// rotates source IPs cannot bypass the per-account ceiling, and a single
	// host attacking multiple accounts cannot bypass the per-(host,account) one.
	hostKey := "host:" + host + ":" + input.Username
	acctKey := "acct:" + input.Username
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	s.throttleMu.Lock()
	// Drop attempts outside the sliding window and compact the map opportunistically.
	if len(s.attempts) > 512 {
		for k, a := range s.attempts {
			j := 0
			for _, t := range a.Times {
				if t.After(cutoff) {
					a.Times[j] = t
					j++
				}
			}
			if j == 0 {
				delete(s.attempts, k)
			} else {
				a.Times = a.Times[:j]
				s.attempts[k] = a
			}
		}
	}
	hostEntry := s.attempts[hostKey]
	hostEntry.Times = appendFresh(hostEntry.Times, cutoff, now)
	acctEntry := s.attempts[acctKey]
	acctEntry.Times = appendFresh(acctEntry.Times, cutoff, now)
	if len(s.attempts) >= 512 && len(hostEntry.Times) == 1 {
		// Map is at capacity. Reject this new fingerprint rather than evicting
		// an entry that might belong to a legitimate user under attack.
		s.throttleMu.Unlock()
		s.auditAudit(host, "rate_limited", input.Username)
		fail(w, 429, "rate_limited", "Повторите вход позже")
		return
	}
	s.attempts[hostKey] = hostEntry
	s.attempts[acctKey] = acctEntry
	s.throttleMu.Unlock()
	if len(hostEntry.Times) > 5 {
		s.auditAudit(host, "rate_limited", input.Username)
		fail(w, 429, "rate_limited", "Слишком много попыток. Повторите через минуту")
		return
	}
	if len(acctEntry.Times) > 20 {
		s.auditAudit(input.Username, "rate_limited", "")
		fail(w, 429, "rate_limited", "Слишком много попыток для этого имени. Повторите через минуту")
		return
	}
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		s.auditAudit(host, "rate_limited", input.Username)
		fail(w, 429, "rate_limited", "Повторите вход позже")
		return
	}
	var encoded string
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	encoded, e = s.Store.UserPasswordHashStandaloneContext(ctx, input.Username)
	if errors.Is(e, sql.ErrNoRows) {
		encoded = s.dummyHash
	} else if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if !passwordMatches(encoded, input.Password) || encoded == s.dummyHash {
		s.auditAudit(input.Username, "login_failed", "")
		fail(w, 401, "invalid_credentials", "Неверное имя или пароль")
		return
	}
	v, e = s.newSession(w, input.Username, encoded, v.Hash)
	if errors.Is(e, errCredentialsChanged) {
		fail(w, 401, "invalid_credentials", "Учётные данные изменились. Войдите заново")
		return
	}
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	s.sessionView(w, v)
}

// remoteHost extracts the client IP from RemoteAddr, falling back to the raw
// value if SplitHostPort fails (e.g. unix socket, missing port).
func remoteHost(addr string) string {
	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return addr
	}
	return host
}

// appendFresh returns the slice with t appended after dropping timestamps older
// than cutoff. Used by the throttle sliding window without reallocating when the
// slice head already expired.
func appendFresh(times []time.Time, cutoff, t time.Time) []time.Time {
	j := 0
	for _, x := range times {
		if x.After(cutoff) {
			times[j] = x
			j++
		}
	}
	times = append(times[:j], t)
	return times
}

// auditAudit records an event and logs (but does not propagate) persistence
// errors so a flaky SQLite write never masks a security control.
func (s *Server) auditAudit(actor, action, entity string) {
	if e := s.Store.Audit(actor, action, entity); e != nil {
		slog.Warn("audit write failed", "action", action, "actor", actor, "error", e)
	}
}

func (s *Server) csrf(r *http.Request, v session) bool {
	return r.Header.Get("Origin") == s.Origin && v.CSRF != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(v.CSRF)) == 1
}
func (s *Server) authenticated(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, e := s.readSession(r)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			// Non-storage error (expired, revoked, disabled): treat as unauthenticated.
			fail(w, 401, "unauthenticated", "Войдите в панель")
			return
		}
		if v.User == "" {
			fail(w, 401, "unauthenticated", "Войдите в панель")
			return
		}
		if r.Method != "GET" && !s.csrf(r, v) {
			s.auditAudit(v.User, "csrf_failed", r.URL.Path)
			fail(w, 403, "csrf_failed", "Запрос не прошёл проверку безопасности")
			return
		}
		next(w, r, v)
	}
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if e := s.Store.DeleteSessionContext(ctx, v.Hash); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	s.auditAudit(v.User, "logout", "")
	http.SetCookie(w, &http.Cookie{Name: "__Host-session", Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}

package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"squid-control.local/squid/internal/store"
)

func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// PasswordHash persists only a salted one-way Argon2id PHC verifier.
func PasswordHash(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must be 12–1024 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return "$argon2id$v=19$m=19456,t=2,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func passwordMatches(encoded, password string) bool {
	if len(password) > 1024 {
		return false
	}
	var saltText, keyText string
	var memory uint32 = 19456
	var iterations uint32 = 2
	var parallel uint8 = 1
	parts := strings.Split(encoded, "$")
	if len(parts) == 3 && parts[0] == "argon2id" {
		saltText, keyText = parts[1], parts[2]
	} else {
		if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
			return false
		}
		var trailing string
		if n, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d%s", &memory, &iterations, &parallel, &trailing); n != 3 || e != io.EOF {
			return false
		}
		if memory < 8192 || memory > 38912 || iterations < 1 || iterations > 4 || parallel < 1 || parallel > 2 {
			return false
		}
		saltText, keyText = parts[4], parts[5]
	}
	salt, e := base64.RawStdEncoding.DecodeString(saltText)
	if e != nil || len(salt) != 16 {
		return false
	}
	key, e := base64.RawStdEncoding.DecodeString(keyText)
	if e != nil || len(key) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(key, argon2.IDKey([]byte(password), salt, iterations, memory, parallel, 32)) == 1
}

// ResetPassword changes the verifier and revokes every session atomically.
// It never creates an unknown user and never persists the supplied plaintext.
func ResetPassword(s *store.Store, username, password string) error {
	hash, e := PasswordHash(password)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.Exec("UPDATE users SET password_hash=? WHERE username=?", hash, username)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("administrator not found")
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE username=?", username); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", "local-cli", "password_reset", username, time.Now().UTC().Format(time.RFC3339)); e != nil {
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
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return errors.New("bootstrap already completed")
	}
	if _, e = tx.Exec("INSERT INTO users VALUES(?,?)", username, h); e != nil {
		return e
	}
	return tx.Commit()
}

type session struct {
	User, CSRF, Hash string
	Expires          int64
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
	v.Hash = tokenHash(c.Value)
	// SELECT and UPDATE last_seen_at in one transaction so a parallel password
	// reset cannot delete the row between read and touch (the previous split-tx
	// version would silently noop the UPDATE and continue with stale state).
	tx, e := s.Store.DB.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var last int64
	e = tx.QueryRow("SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", v.Hash).Scan(&v.User, &v.CSRF, &v.Expires, &last)
	now := time.Now().Unix()
	if e != nil {
		return v, e
	}
	if now >= v.Expires || now-last > 1800 {
		return v, errors.New("session expired")
	}
	if _, e = tx.Exec("UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now, v.Hash); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Server) newSession(w http.ResponseWriter, user string, credentials ...string) (session, error) {
	token := rand.Text()
	v := session{User: user, CSRF: rand.Text(), Hash: tokenHash(token), Expires: time.Now().Add(12 * time.Hour).Unix()}
	if user == "" {
		v.Expires = time.Now().Add(10 * time.Minute).Unix()
	}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if user != "" {
		if len(credentials) != 2 {
			return v, errors.New("missing credential guard")
		}
		var currentHash string
		if e = tx.QueryRow("SELECT password_hash FROM users WHERE username=?", user).Scan(&currentHash); e != nil {
			return v, e
		}
		if subtle.ConstantTimeCompare([]byte(currentHash), []byte(credentials[0])) != 1 {
			return v, errCredentialsChanged
		}
		if _, e = tx.Exec("DELETE FROM sessions WHERE token_hash=?", credentials[1]); e != nil {
			return v, e
		}
		if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", user, "login", "", time.Now().UTC().Format(time.RFC3339)); e != nil {
			return v, e
		}
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE expires_at<? OR last_seen_at<?", time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()); e != nil {
		return v, e
	}
	var count, anonCount int
	if e = tx.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); e != nil {
		return v, e
	}
	if e = tx.QueryRow("SELECT COUNT(*) FROM sessions WHERE username=''").Scan(&anonCount); e != nil {
		return v, e
	}
	// Anonymous GET /session visitors only need a CSRF holder; cap them well below
	// the authenticated ceiling so an unauthenticated attacker cannot exhaust the
	// shared pool and lock the administrator out of login.
	if count >= 256 || (user == "" && anonCount >= 32) {
		return v, errors.New("session capacity reached")
	}
	if _, e = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?)", v.Hash, user, v.CSRF, v.Expires, time.Now().Unix()); e != nil {
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
	respond(w, 200, map[string]any{"authenticated": v.User != "", "username": username, "csrf_token": v.CSRF, "expires_at": expires})
}
func (s *Server) sessionGet(w http.ResponseWriter, r *http.Request) {
	v, e := s.readSession(r)
	if e != nil {
		v, e = s.newSession(w, "")
	}
	if e != nil {
		fail(w, 503, "session_unavailable", "Сессии временно недоступны")
		return
	}
	s.sessionView(w, v)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	v, e := s.readSession(r)
	if e != nil || !s.csrf(r, v) {
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
	host, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		host = r.RemoteAddr
	}
	// Two independent throttle counters share the same backing map but use
	// distinct key prefixes, so a distributed password-guessing attack that
	// rotates source IPs cannot bypass the per-account ceiling, and a single
	// host attacking multiple accounts cannot bypass the per-(host,account) one.
	hostKey := "host:" + host + ":" + input.Username
	acctKey := "acct:" + input.Username
	s.throttleMu.Lock()
	now := time.Now()
	hostEntry := s.attempts[hostKey]
	if now.Sub(hostEntry.Start) > time.Minute {
		hostEntry = attempt{Start: now}
	}
	hostEntry.Count++
	acctEntry := s.attempts[acctKey]
	if now.Sub(acctEntry.Start) > time.Minute {
		acctEntry = attempt{Start: now}
	}
	acctEntry.Count++
	if len(s.attempts) > 512 {
		for k, a := range s.attempts {
			if now.Sub(a.Start) > time.Minute {
				delete(s.attempts, k)
			}
		}
	}
	if len(s.attempts) >= 512 && hostEntry.Count == 1 {
		s.throttleMu.Unlock()
		fail(w, 429, "rate_limited", "Повторите вход позже")
		return
	}
	s.attempts[hostKey] = hostEntry
	s.attempts[acctKey] = acctEntry
	s.throttleMu.Unlock()
	if hostEntry.Count > 5 {
		fail(w, 429, "rate_limited", "Слишком много попыток. Повторите через минуту")
		return
	}
	if acctEntry.Count > 20 {
		fail(w, 429, "rate_limited", "Слишком много попыток для этого имени. Повторите через минуту")
		return
	}
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		fail(w, 429, "rate_limited", "Повторите вход позже")
		return
	}
	var encoded string
	e = s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username=?", input.Username).Scan(&encoded)
	if errors.Is(e, sql.ErrNoRows) {
		encoded = s.dummyHash
	} else if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if !passwordMatches(encoded, input.Password) || encoded == s.dummyHash {
		s.Store.Audit(input.Username, "login_failed", "")
		fail(w, 401, "invalid_credentials", "Неверное имя или пароль")
		return
	}
	v, e = s.newSession(w, input.Username, encoded, v.Hash)
	if errors.Is(e, errCredentialsChanged) {
		fail(w, 401, "invalid_credentials", "Пароль изменён. Войдите заново")
		return
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	s.sessionView(w, v)
}
func (s *Server) csrf(r *http.Request, v session) bool {
	return r.Header.Get("Origin") == s.Origin && v.CSRF != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(v.CSRF)) == 1
}
func (s *Server) authenticated(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, e := s.readSession(r)
		if e != nil || v.User == "" {
			fail(w, 401, "unauthenticated", "Войдите в панель")
			return
		}
		if r.Method != "GET" && !s.csrf(r, v) {
			fail(w, 403, "csrf_failed", "Запрос не прошёл проверку безопасности")
			return
		}
		next(w, r, v)
	}
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, v session) {
	if _, e := s.Store.DB.Exec("DELETE FROM sessions WHERE token_hash=?", v.Hash); e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "__Host-session", Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}

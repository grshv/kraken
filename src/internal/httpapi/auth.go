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
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"squid-control.local/squid/internal/store"
)

func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func PasswordHash(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must be 12–1024 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func passwordMatches(encoded, password string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 3 || p[0] != "argon2id" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[1])
	if e != nil || len(salt) != 16 {
		return false
	}
	key, e := base64.RawStdEncoding.DecodeString(p[2])
	if e != nil || len(key) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(key, argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)) == 1
}
func Bootstrap(s *store.Store, username, password string) error {
	if len(username) == 0 || len(username) > 128 || strings.TrimSpace(username) != username {
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
	var last int64
	e = s.Store.DB.QueryRow("SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", v.Hash).Scan(&v.User, &v.CSRF, &v.Expires, &last)
	now := time.Now().Unix()
	if e != nil {
		return v, e
	}
	if now >= v.Expires || now-last > 1800 {
		return v, errors.New("session expired")
	}
	_, e = s.Store.DB.Exec("UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now, v.Hash)
	return v, e
}
func (s *Server) newSession(w http.ResponseWriter, user string) (session, error) {
	token := rand.Text()
	v := session{User: user, CSRF: rand.Text(), Hash: tokenHash(token), Expires: time.Now().Add(12 * time.Hour).Unix()}
	if user == "" {
		v.Expires = time.Now().Add(10 * time.Minute).Unix()
	}
	// Bound persistent pre-auth contexts and remove expired sessions.
	if _, e := s.Store.DB.Exec("DELETE FROM sessions WHERE expires_at<? OR last_seen_at<?", time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()); e != nil {
		return v, e
	}
	var count int
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); e != nil {
		return v, e
	}
	if count >= 256 {
		return v, errors.New("session capacity reached")
	}
	_, e := s.Store.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?,?)", v.Hash, user, v.CSRF, v.Expires, time.Now().Unix())
	if e != nil {
		return v, e
	}
	http.SetCookie(w, &http.Cookie{Name: "__Host-session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return v, nil
}
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
	key := host + ":" + input.Username
	s.throttleMu.Lock()
	now := time.Now()
	entry := s.attempts[key]
	if now.Sub(entry.Start) > time.Minute {
		entry = attempt{Start: now}
	}
	entry.Count++
	if len(s.attempts) > 512 {
		for k, a := range s.attempts {
			if now.Sub(a.Start) > time.Minute {
				delete(s.attempts, k)
			}
		}
	}
	if len(s.attempts) >= 512 && entry.Count == 1 {
		s.throttleMu.Unlock()
		fail(w, 429, "rate_limited", "Повторите вход позже")
		return
	}
	s.attempts[key] = entry
	s.throttleMu.Unlock()
	if entry.Count > 5 {
		fail(w, 429, "rate_limited", "Слишком много попыток. Повторите через минуту")
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
	tx, e := s.Store.DB.Begin()
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM sessions WHERE token_hash=?", v.Hash); e == nil {
		_, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", input.Username, "login", "", time.Now().UTC().Format(time.RFC3339))
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	v, e = s.newSession(w, input.Username)
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
func safeError(e error) string { return fmt.Sprintf("%v", e) }

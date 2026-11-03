package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Anonymous visitors only need a short-lived, signed CSRF holder. Keeping it
// outside SQLite prevents unauthenticated traffic from occupying login slots.
// The key lives for one serve process; a restart requires refreshing the form.
func (s *Server) anonymousMAC(purpose, value string) string {
	h := hmac.New(sha256.New, s.anonymousKey)
	h.Write([]byte(purpose + "\x00" + value))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Server) newAnonymousSession(w http.ResponseWriter) session {
	expires := time.Now().Add(10 * time.Minute).Unix()
	body := "anon." + strconv.FormatInt(expires, 10) + "." + rand.Text()
	token := body + "." + s.anonymousMAC("cookie", body)
	http.SetCookie(w, &http.Cookie{Name: "__Host-session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return session{Hash: tokenHash(token), CSRF: s.anonymousMAC("csrf", token), Expires: expires}
}

func (s *Server) readAnonymousSession(token string) (session, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "anon" {
		return session{}, errors.New("invalid anonymous session")
	}
	body := strings.Join(parts[:3], ".")
	if !hmac.Equal([]byte(parts[3]), []byte(s.anonymousMAC("cookie", body))) {
		return session{}, errors.New("invalid anonymous signature")
	}
	expires, e := strconv.ParseInt(parts[1], 10, 64)
	now := time.Now().Unix()
	if e != nil || expires <= now || expires > now+600 {
		return session{}, errors.New("anonymous session expired")
	}
	return session{Hash: tokenHash(token), CSRF: s.anonymousMAC("csrf", token), Expires: expires}, nil
}

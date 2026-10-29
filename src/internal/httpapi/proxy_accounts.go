package httpapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"kraken.local/kraken/internal/credentials"
	"kraken.local/kraken/internal/proxyauth"
	"kraken.local/kraken/internal/store"
)

// refreshProxyExports atomically rewrites the proxy-account export the Squid
// helper reads. Called after every successful proxy-account mutation, at serve
// start and in service-update so a disabled or reset identity cannot be reused
// on a cached Basic credential. Errors are surfaced without hashes/passwords.
func (s *Server) refreshProxyAccounts() error {
	accounts, e := s.Store.EnabledProxyAccounts()
	if e != nil {
		return e
	}
	if e := proxyauth.WriteExport(s.Runtime.Options.ProxyAccounts, accounts); e != nil {
		slog.Error("proxy account export failed", "error", e)
		return e
	}
	return nil
}

func (s *Server) listProxyAccounts(w http.ResponseWriter, r *http.Request, v session) {
	items, e := s.Store.ProxyAccounts()
	if e != nil {
		fail(w, 503, "storage_unavailable", "Учётные записи прокси недоступны")
		return
	}
	respond(w, 200, map[string]any{"items": items})
}
func (s *Server) createProxyAccount(w http.ResponseWriter, r *http.Request, v session) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !usernamePattern.MatchString(input.Username) {
		fail(w, 400, "invalid_username", "Логин: латинские буквы, цифры, точка, дефис и подчёркивание")
		return
	}
	if strings.ContainsAny(input.Password, "\x00\r\n") {
		fail(w, 400, "invalid_password", "Пароль не должен содержать управляющие символы")
		return
	}
	release, ok := s.passwordWorkSlot(w)
	if !ok {
		return
	}
	defer release()
	hash, e := credentials.Hash(input.Password)
	if e != nil {
		fail(w, 400, "invalid_password", "Пароль должен содержать от 12 до 1024 байт")
		return
	}
	if e = s.Store.CreateProxyAccount(input.Username, hash, v.User); errors.Is(e, store.ErrProxyAccountExists) {
		fail(w, 409, "account_exists", "Такой логин уже существует")
		return
	} else if e != nil {
		fail(w, 503, "storage_unavailable", "Не удалось создать учётную запись")
		return
	}
	if e := s.refreshProxyAccounts(); e != nil {
		fail(w, 503, "storage_unavailable", "Экспорт учётных записей прокси недоступен")
		return
	}
	account, e := s.Store.ProxyAccount(input.Username)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Учётная запись недоступна")
		return
	}
	respond(w, 201, account)
}
func (s *Server) updateProxyAccount(w http.ResponseWriter, r *http.Request, v session) {
	username := r.PathValue("username")
	if !usernamePattern.MatchString(username) {
		fail(w, 400, "invalid_username", "Некорректный логин")
		return
	}
	var input struct {
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Enabled == nil && input.Password == nil {
		fail(w, 400, "invalid_input", "Нет изменений")
		return
	}
	var hash *string
	if input.Password != nil {
		if strings.ContainsAny(*input.Password, "\x00\r\n") {
			fail(w, 400, "invalid_password", "Пароль не должен содержать управляющие символы")
			return
		}
		release, ok := s.passwordWorkSlot(w)
		if !ok {
			return
		}
		defer release()
		h, e := credentials.Hash(*input.Password)
		if e != nil {
			fail(w, 400, "invalid_password", "Пароль должен содержать от 12 до 1024 байт")
			return
		}
		hash = &h
	}
	if e := s.Store.ChangeProxyAccount(username, input.Enabled, hash, v.User); errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Учётная запись не найдена")
		return
	} else if e != nil {
		fail(w, 503, "storage_unavailable", "Не удалось сохранить учётную запись")
		return
	}
	if e := s.refreshProxyAccounts(); e != nil {
		fail(w, 503, "storage_unavailable", "Экспорт учётных записей прокси недоступен")
		return
	}
	account, e := s.Store.ProxyAccount(username)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Учётная запись недоступна")
		return
	}
	respond(w, 200, account)
}

// passwordWorkSlot bounds Argon work shared by authenticated account mutations
// and login; authorised requests must not allocate unbounded hash memory either.
func (s *Server) passwordWorkSlot(w http.ResponseWriter) (func(), bool) {
	select {
	case s.loginSlots <- struct{}{}:
		return func() { <-s.loginSlots }, true
	default:
		fail(w, 429, "rate_limited", "Повторите операцию позже")
		return nil, false
	}
}

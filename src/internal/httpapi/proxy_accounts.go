package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kraken.local/kraken/internal/credentials"
	"kraken.local/kraken/internal/proxyauth"
	"kraken.local/kraken/internal/store"
)

// refreshProxyExports atomically rewrites the proxy-account export the Squid
// helper reads. Called after every successful proxy-account mutation, at serve
// start and in service-update so a disabled or reset identity cannot be reused
// on a cached Basic credential. Errors are surfaced without hashes/passwords.
func (s *Server) refreshProxyAccounts() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	accounts, e := s.Store.EnabledProxyAccounts(ctx)
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
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, e := s.Store.ProxyAccounts(ctx)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Учётные записи прокси недоступны")
		return
	}
	if v.Role == "manager" {
		scope, e := s.managerScope(ctx, v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		// Draft snapshot gives the proxy usernames referenced by clients.
		snap, _, e := s.draftSnapshot(ctx)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Черновик недоступен")
			return
		}
		mine := map[string]bool{}
		for _, c := range snap.Clients {
			if inScope(scope, c.ID) && c.ProxyUsername != "" {
				mine[c.ProxyUsername] = true
			}
		}
		filtered := []store.ProxyAccount{}
		for _, a := range items {
			owner := ""
			if a.Owner != nil {
				owner = *a.Owner
			}
			if owner == v.User || mine[a.Username] {
				filtered = append(filtered, a)
			}
		}
		items = filtered
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
	if s.storageFull(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// A proxy login already taken by another manager's account is hidden: report
	// it as a name clash without revealing the owner.
	existing, e := s.Store.ProxyAccount(ctx, input.Username)
	if e == nil {
		owner := ""
		if existing.Owner != nil {
			owner = *existing.Owner
		}
		if v.Role == "manager" && owner != v.User {
			fail(w, 409, "username_taken", "Это имя логина занято")
			return
		}
		fail(w, 409, "account_exists", "Такой логин уже существует")
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
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
	owner := ""
	if v.Role == "manager" {
		owner = v.User
	}
	if e = s.Store.CreateProxyAccount(ctx, input.Username, hash, owner, v.User); errors.Is(e, store.ErrProxyAccountExists) {
		fail(w, 409, "account_exists", "Такой логин уже существует")
		return
	} else if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Не удалось создать учётную запись")
		return
	}
	if e := s.refreshProxyAccounts(); e != nil {
		fail(w, 503, "storage_unavailable", "Экспорт учётных записей прокси недоступен")
		return
	}
	account, e := s.Store.ProxyAccount(ctx, input.Username)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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
	if s.storageFull(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if !s.proxyAccountVisible(ctx, w, v, username) {
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
	if e := s.Store.ChangeProxyAccount(ctx, username, input.Enabled, hash, v.User); errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Учётная запись не найдена")
		return
	} else if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Не удалось сохранить учётную запись")
		return
	}
	if e := s.refreshProxyAccounts(); e != nil {
		fail(w, 503, "storage_unavailable", "Экспорт учётных записей прокси недоступен")
		return
	}
	account, e := s.Store.ProxyAccount(ctx, username)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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

// proxyAccountVisible lets an admin reach any proxy account and a manager only
// accounts he owns or that his clients use. Otherwise it writes 404 (or 503)
// and returns false.
func (s *Server) proxyAccountVisible(ctx context.Context, w http.ResponseWriter, v session, username string) bool {
	if v.Role != "manager" {
		return true
	}
	account, e := s.Store.ProxyAccount(ctx, username)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Учётная запись не найдена")
		return false
	}
	if e != nil {
		if s.failStorageBusy(w, e) {
			return false
		}
		fail(w, 503, "storage_unavailable", "Учётная запись недоступна")
		return false
	}
	if account.Owner != nil && *account.Owner == v.User {
		return true
	}
	scope, e := s.managerScope(ctx, v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return false
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return false
	}
	snap, _, e := s.draftSnapshot(ctx)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return false
		}
		fail(w, 503, "storage_unavailable", "Черновик недоступен")
		return false
	}
	for _, c := range snap.Clients {
		if inScope(scope, c.ID) && c.ProxyUsername == username {
			return true
		}
	}
	fail(w, 404, "not_found", "Учётная запись не найдена")
	return false
}

// proxyLoginWindow is the period of the proxy login activity card.
const proxyLoginWindow = 24 * time.Hour

type loginSource struct {
	SourceIP string `json:"source_ip"`
	Requests int64  `json:"requests"`
	Failed   int64  `json:"failed_logins"`
	LastSeen string `json:"last_seen"`
}

// proxyAccountActivity reports how a proxy login was used over 24 hours:
// requests, policy denials, wrong passwords (TCP_DENIED/407 with this name)
// and the source addresses. Same visibility as updating the account.
func (s *Server) proxyAccountActivity(w http.ResponseWriter, r *http.Request, v session) {
	username := r.PathValue("username")
	if !usernamePattern.MatchString(username) {
		fail(w, 400, "invalid_username", "Некорректный логин")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if !s.proxyAccountVisible(ctx, w, v, username) {
		return
	}
	now := s.now().UTC()
	a, e := s.Telemetry.ProxyLoginActivity(ctx, username, now.Add(-proxyLoginWindow), now)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Статистика логина недоступна")
		return
	}
	sources := []loginSource{}
	for _, src := range a.Sources {
		sources = append(sources, loginSource{SourceIP: src.SourceIP, Requests: src.Requests, Failed: src.Failed, LastSeen: src.LastSeen.Format(time.RFC3339)})
	}
	var lastUsed *string
	if a.LastUsed != nil {
		lastUsed = strPtr(a.LastUsed.Format(time.RFC3339))
	}
	respond(w, 200, map[string]any{
		"window_hours":  int(proxyLoginWindow / time.Hour),
		"requests":      a.Requests,
		"denied":        a.Denied,
		"failed_logins": a.Failed,
		"last_used_at":  lastUsed,
		"sources":       sources,
	})
}

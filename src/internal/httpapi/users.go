package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"kraken.local/kraken/internal/store"
)

func permissions(role string) []string {
	if role == "admin" {
		return []string{"read", "configure", "apply", "users", "proxy_accounts", "audit"}
	}
	if role == "manager" {
		return []string{"read", "configure", "apply", "proxy_accounts", "audit"}
	}
	return []string{}
}
func hasPermission(v session, permission string) bool {
	for _, p := range permissions(v.Role) {
		if p == permission {
			return true
		}
	}
	return false
}

// adminOnly gates endpoints restricted to the admin role. Managers receive
// 403 admin_required rather than the generic forbidden code.
func (s *Server) adminOnly(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return s.authenticated(func(w http.ResponseWriter, r *http.Request, v session) {
		if v.Role != "admin" {
			fail(w, 403, "admin_required", "Требуется роль администратора")
			return
		}
		next(w, r, v)
	})
}
func (s *Server) authorized(permission string, next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return s.authenticated(func(w http.ResponseWriter, r *http.Request, v session) {
		if !hasPermission(v, permission) {
			fail(w, 403, "forbidden", "Недостаточно прав")
			return
		}
		next(w, r, v)
	})
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, e := s.Store.UsersContext(ctx)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Учётные записи недоступны")
		return
	}
	respond(w, 200, map[string]any{"items": items})
}
func userError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, store.ErrLastAdmin):
		fail(w, 409, "last_admin", "Нельзя отключить или понизить последнего администратора")
	case errors.Is(e, store.ErrNotFound):
		fail(w, 404, "not_found", "Пользователь не найден")
	case errors.Is(e, store.ErrExists):
		fail(w, 409, "user_exists", "Это имя уже занято")
	default:
		fail(w, 503, "storage_unavailable", "Не удалось сохранить пользователя")
	}
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request, v session) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !usernamePattern.MatchString(input.Username) || !store.ValidRole(input.Role) {
		fail(w, 422, "invalid_user", "Проверьте имя и роль пользователя")
		return
	}
	if s.storageFull(w) {
		return
	}
	release, ok := s.passwordWorkSlot(w)
	if !ok {
		return
	}
	defer release()
	h, e := PasswordHash(input.Password)
	if e != nil {
		fail(w, 422, "invalid_password", "Пароль должен содержать от 12 до 1024 байт")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	item, e := s.Store.CreateUserContext(ctx, input.Username, h, input.Role, v.User)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		userError(w, e)
		return
	}
	respond(w, 201, item)
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, v session) {
	var input struct {
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Role == nil && input.Enabled == nil && input.Password == nil {
		fail(w, 422, "invalid_user", "Выберите изменение пользователя")
		return
	}
	if input.Role != nil && !store.ValidRole(*input.Role) {
		fail(w, 422, "invalid_user", "Неизвестная роль")
		return
	}
	if s.storageFull(w) {
		return
	}
	var h *string
	if input.Password != nil {
		release, ok := s.passwordWorkSlot(w)
		if !ok {
			return
		}
		defer release()
		encoded, e := PasswordHash(*input.Password)
		if e != nil {
			fail(w, 422, "invalid_password", "Пароль должен содержать от 12 до 1024 байт")
			return
		}
		h = &encoded
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	item, e := s.Store.ChangeUserContext(ctx, r.PathValue("username"), input.Role, input.Enabled, h, v.User)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		userError(w, e)
		return
	}
	respond(w, 200, item)
}

// getScope returns the manager scope for one user. Admin-only.
func (s *Server) getScope(w http.ResponseWriter, r *http.Request, v session) {
	username := r.PathValue("username")
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	user, e := s.Store.UserContext(ctx, username)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Пользователь не найден")
		return
	}
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if user.Role != "manager" {
		fail(w, 422, "not_manager", "Область есть только у менеджера")
		return
	}
	scope, e := s.Store.UserScopeContext(ctx, username)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 200, scope)
}

// updateScope replaces the manager scope. Admin-only. CIDR are normalized.
func (s *Server) updateScope(w http.ResponseWriter, r *http.Request, v session) {
	username := r.PathValue("username")
	var input struct {
		Networks  []string `json:"networks"`
		ClientIDs []string `json:"client_ids"`
	}
	if !decode(w, r, &input) {
		return
	}
	normalized := []string{}
	for _, raw := range input.Networks {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil {
			if addr, ae := netip.ParseAddr(raw); ae == nil {
				prefix = netip.PrefixFrom(addr, addr.BitLen())
			} else {
				fail(w, 422, "invalid_cidr", "Некорректный CIDR")
				return
			}
		}
		normalized = append(normalized, prefix.Masked().String())
	}
	if s.storageFull(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	tx, e := s.Store.BeginTxContext(ctx)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	defer tx.Rollback()
	user, e := s.Store.User(tx, username)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Пользователь не найден")
		return
	}
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if user.Role != "manager" {
		fail(w, 422, "not_manager", "Область есть только у менеджера")
		return
	}
	scope := store.Scope{Networks: normalized, ClientIDs: input.ClientIDs}
	if e := s.Store.ReplaceScope(tx, username, scope); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if e := s.Store.AuditTx(tx, v.User, "update_scope", username); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if e := tx.Commit(); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 200, scope)
}

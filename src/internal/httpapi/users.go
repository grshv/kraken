package httpapi

import (
	"errors"
	"net/http"

	"squid-control.local/squid/internal/store"
)

func permissions(role string) []string {
	if role == "admin" {
		return []string{"read", "configure", "apply", "users", "proxy_accounts", "audit"}
	}
	if role == "viewer" {
		return []string{"read", "audit"}
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
	items, e := s.Store.Users()
	if e != nil {
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
	item, e := s.Store.CreateUser(input.Username, h, input.Role, v.User)
	if e != nil {
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
	item, e := s.Store.ChangeUser(r.PathValue("username"), input.Role, input.Enabled, h, v.User)
	if e != nil {
		userError(w, e)
		return
	}
	respond(w, 200, item)
}

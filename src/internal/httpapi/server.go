package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kraken.local/kraken/internal/policy"
	local "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/store"
	"kraken.local/kraken/internal/telemetry"
)

type Draft struct {
	Version  int             `json:"version"`
	Snapshot policy.Snapshot `json:"snapshot"`
	Updated  string          `json:"updated_at"`
}
type Revision struct {
	ID           string          `json:"id"`
	DraftVersion int             `json:"draft_version"`
	Hash         string          `json:"policy_hash"`
	Compiler     string          `json:"compiler_version"`
	Image        string          `json:"image_digest"`
	Created      string          `json:"created_at"`
	Snapshot     policy.Snapshot `json:"snapshot"`
}
type Operation struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"`
	Revision string   `json:"revision_id"`
	Status   string   `json:"status"`
	Findings []string `json:"findings"`
	Created  string   `json:"created_at"`
	Updated  string   `json:"updated_at"`
}
type operationInput struct {
	Action     string  `json:"action"`
	Revision   string  `json:"revision_id"`
	Expected   *string `json:"expected_applied_revision"`
	Validation string  `json:"validation_operation_id,omitempty"`
}
type attempt struct {
	// Sliding window of attempt timestamps within the last minute. A fixed
	// window with entry.Start would let an attacker fire 5 attempts at 0:59 and
	// 5 more at 1:01 = 10 inside 2 seconds — sliding window closes that by
	// expiring each attempt individually.
	Times []time.Time
}
type Server struct {
	persistenceFailed atomic.Bool
	PanelCert         string
	Store             *store.Store
	Telemetry         *telemetry.Store
	Runtime           *local.Manager
	Origin            string
	Static            http.Handler
	Image             string
	// now returns the current time for inspector windowing. Production uses
	// time.Now; tests override it to assert window boundaries deterministically.
	now          func() time.Time
	draftMu      sync.Mutex
	opsMu        sync.Mutex
	throttleMu   sync.Mutex
	attempts     map[string]attempt
	loginSlots   chan struct{}
	dummyHash    string
	anonymousKey []byte
	workers      sync.WaitGroup
	// certCache avoids re-parsing the same PEM file on every polling tick of
	// /api/v1/certificates. Entries are keyed by absolute path; the cached
	// payload is invalidated when mtime changes. The panel polls certificates
	// roughly every 10 s, and parsing a 1 MiB PEM chain each time is wasteful
	// when the underlying file has not changed since install.
	certCache   map[string]certCacheEntry
	certCacheMu sync.Mutex
}

// certCacheEntry holds the parsed result of one PEM file plus the mtime/size
// observed when it was produced. A mismatch on stat invalidates the entry.
type certCacheEntry struct {
	mtime time.Time
	size  int64
	item  map[string]any
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }
func New(st *store.Store, rt *local.Manager, origin string, static http.Handler, image string, tel *telemetry.Store) (*Server, error) {
	u, parseErr := url.Parse(origin)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("KRAKEN_PUBLIC_ORIGIN must be an https origin without trailing slash")
	}
	h, e := PasswordHash("dummy-" + store.ID())
	if e != nil {
		return nil, e
	}
	s := &Server{Store: st, Telemetry: tel, Runtime: rt, Origin: origin, Static: static, Image: image, now: time.Now, attempts: map[string]attempt{}, loginSlots: make(chan struct{}, 2), dummyHash: h, anonymousKey: []byte(rand.Text()), certCache: map[string]certCacheEntry{}}
	var d Draft
	e = st.Get("draft", &d)
	if errors.Is(e, sql.ErrNoRows) {
		e = st.Put("draft", Draft{Version: 1, Snapshot: policy.Empty(), Updated: stamp()})
	}
	if e != nil {
		return nil, e
	}
	// Reconcile abandoned operations after the runtime has recovered its filesystem state.
	allOps, e := st.ListOperationBodies()
	if e != nil {
		return nil, e
	}
	var abandoned []Operation
	for _, row := range allOps {
		var op Operation
		if e = json.Unmarshal([]byte(row.Body), &op); e != nil {
			return nil, e
		}
		if op.Status == "pending" || op.Status == "validating" || op.Status == "reloading" {
			abandoned = append(abandoned, op)
		}
	}
	j, je := rt.Journal()
	for _, op := range abandoned {
		op.Status = "rejected"
		op.Findings = []string{"Operation interrupted before a confirmed outcome"}
		if je == nil && j.Operation == op.ID && j.Stage == "terminal" {
			op.Status = j.Outcome
			op.Findings = []string{"Recovered from durable runtime journal"}
		}
		op.Updated = stamp()
		if e = s.saveOperation(op); e != nil {
			return nil, e
		}
	}
	if e := s.refreshProxyAccounts(); e != nil {
		return nil, e
	}
	return s, nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if e := json.NewEncoder(w).Encode(v); e != nil {
		// WriteHeader+headers already sent; the client will get a truncated body.
		// At least surface the encoding failure so the request_id chain is not
		// silently broken in operator logs.
		slog.Warn("http response encode failed", "status", status, "error", e)
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"code": code, "message": message, "request_id": store.ID()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "invalid_content_type", "Требуется application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "invalid_json", "Некорректный JSON")
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		fail(w, 400, "invalid_json", "Ожидался один JSON-документ")
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]bool{"alive": true}) })
	mux.HandleFunc("GET /api/v1/session", s.sessionGet)
	mux.HandleFunc("POST /api/v1/session", s.login)
	mux.HandleFunc("DELETE /api/v1/session", s.authenticated(s.logout))
	mux.HandleFunc("GET /api/v1/status", s.authorized("read", s.status))
	mux.HandleFunc("GET /api/v1/squid/metrics", s.authorized("read", s.squidMetrics))
	mux.HandleFunc("GET /api/v1/activity", s.authorized("read", s.activity))
	mux.HandleFunc("GET /api/v1/statistics", s.authorized("read", s.statistics))
	mux.HandleFunc("GET /api/v1/revisions", s.adminOnly(s.listRevisions))
	mux.HandleFunc("GET /api/v1/configuration/preview", s.adminOnly(s.configurationPreview))
	mux.HandleFunc("GET /api/v1/configuration/expert-template", s.adminOnly(s.expertTemplate))
	mux.HandleFunc("GET /api/v1/configuration/draft", s.authorized("configure", s.getDraft))
	mux.HandleFunc("PUT /api/v1/configuration/draft", s.authorized("configure", s.updateDraft))
	mux.HandleFunc("POST /api/v1/revisions", s.authorized("configure", s.createRevision))
	mux.HandleFunc("GET /api/v1/revisions/{id}", s.adminOnly(s.getRevision))
	mux.HandleFunc("GET /api/v1/revisions/{id}/preview", s.adminOnly(s.preview))
	mux.HandleFunc("POST /api/v1/operations", s.authorized("configure", s.createOperation))
	mux.HandleFunc("GET /api/v1/operations/{id}", s.authorized("configure", s.getOperation))
	mux.HandleFunc("GET /api/v1/events", s.authorized("read", s.events))
	mux.HandleFunc("GET /api/v1/events/clients", s.authorized("read", s.eventClients))
	mux.HandleFunc("GET /api/v1/inspector/clients/{id}/stats", s.authorized("read", s.inspectorClientStats))
	mux.HandleFunc("GET /api/v1/inspector/policies/{id}/hits", s.authorized("read", s.inspectorPolicyHits))
	mux.HandleFunc("GET /api/v1/inspector/unknown-sources", s.authorized("read", s.inspectorUnknownSources))
	mux.HandleFunc("GET /api/v1/inspector/events/{event_id}/explain", s.authorized("read", s.inspectorEventExplain))
	mux.HandleFunc("GET /api/v1/inspector/draft-impact", s.authorized("read", s.inspectorDraftImpact))
	mux.HandleFunc("GET /api/v1/inspector/domain-templates", s.authorized("read", s.inspectorDomainTemplates))
	mux.HandleFunc("GET /api/v1/audit", s.authorized("audit", s.auditEvents))
	mux.HandleFunc("GET /api/v1/users", s.adminOnly(s.listUsers))
	mux.HandleFunc("POST /api/v1/users", s.adminOnly(s.createUser))
	mux.HandleFunc("PATCH /api/v1/users/{username}", s.adminOnly(s.updateUser))
	mux.HandleFunc("GET /api/v1/users/{username}/scope", s.adminOnly(s.getScope))
	mux.HandleFunc("PUT /api/v1/users/{username}/scope", s.adminOnly(s.updateScope))
	mux.HandleFunc("GET /api/v1/proxy-accounts", s.authorized("configure", s.listProxyAccounts))
	mux.HandleFunc("POST /api/v1/proxy-accounts", s.authorized("proxy_accounts", s.createProxyAccount))
	mux.HandleFunc("PATCH /api/v1/proxy-accounts/{username}", s.authorized("proxy_accounts", s.updateProxyAccount))
	mux.HandleFunc("GET /api/v1/certificates", s.authorized("read", s.certificates))
	mux.HandleFunc("GET /api/v1/help", s.authorized("read", s.helpIndex))
	mux.HandleFunc("GET /api/v1/help/{slug}", s.authorized("read", s.helpArticle))
	// No /api/ catch-all: a method-agnostic prefix pattern absorbs wrong-method
	// requests and would surface them as 404 instead of the 405 Go 1.22 ServeMux
	// returns when a path matches a registered pattern with a different method.
	// Unknown /api/ paths fall through to the "/" handler below, which returns
	// the JSON 404 envelope.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fail(w, 404, "not_found", "Неизвестный endpoint")
			return
		}
		s.Static.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := store.ID()
		// Default policy: no-store for HTML, JSON and error bodies. Hashed
		// assets under /assets/ override this to immutable in web/embed.go once
		// the file is confirmed to exist (so a missing asset does not become a
		// perpetually cached 404).
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// includeSubDomains; preload: management panel is hosted on a dedicated
		// origin without other HTTP subdomains. preload listing is opt-in and
		// only effective once the operator submits the host to the HSTS preload
		// list; the directive itself is harmless for closed single-host deploys.
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("handler panic", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "request_id", rid, "recover", rv)
				fail(rw, 500, "internal_error", "Внутренняя ошибка сервера")
			}
		}()
		mux.ServeHTTP(rw, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration_ms", time.Since(start).Milliseconds(), "request_id", rid, "remote", r.RemoteAddr)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the final status code for
// access logging without buffering the body.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
func (s *Server) getDraft(w http.ResponseWriter, r *http.Request, v session) {
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if v.Role == "manager" {
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		d.Snapshot = managerDraftView(d.Snapshot, scope)
		// Manager view drops expert_config; settings are visible but edits ignored.
		d.Snapshot.Expert = ""
	}
	respond(w, 200, d)
}
func (s *Server) updateDraft(w http.ResponseWriter, r *http.Request, v session) {
	var input struct {
		Expected int             `json:"expected_version"`
		Snapshot policy.Snapshot `json:"snapshot"`
	}
	if !decode(w, r, &input) {
		return
	}
	for i, c := range input.Snapshot.Clients {
		for j, raw := range c.Networks {
			if address, e := netip.ParseAddr(raw); e == nil {
				input.Snapshot.Clients[i].Networks[j] = netip.PrefixFrom(address, address.BitLen()).String()
			}
		}
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Действующая конфигурация недоступна")
		return
	}
	// Manager: merge their visible slice onto the current full draft, then
	// enforce scope invariants. Admin: validate the input directly.
	finalSnapshot := input.Snapshot
	var newIDs []string
	if v.Role == "manager" {
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		var current Draft
		if e := s.Store.Get("draft", &current); e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		// Compute new client IDs: present in input, absent from current draft.
		// Used both to expand the scope for source validation (the assignment
		// is not yet persisted) and to assign the new clients atomically.
		currentIDs := map[string]bool{}
		for _, c := range current.Snapshot.Clients {
			currentIDs[c.ID] = true
		}
		newIDs = nil
		expandedScope := scope
		for _, c := range input.Snapshot.Clients {
			if !currentIDs[c.ID] {
				newIDs = append(newIDs, c.ID)
				expandedScope.ClientIDs = append(expandedScope.ClientIDs, c.ID)
			}
		}
		finalSnapshot = mergeManagerDraft(current.Snapshot, input.Snapshot, scope)
		// Source checks first (422) so client network errors are not masked by
		// a policy-edit error when the manager did not change any policy.
		if code, msg, ok := validateManagerDraft(finalSnapshot, expandedScope); !ok {
			fail(w, 422, code, msg)
			return
		}
		if code, msg, ok := validateManagerPolicyEdits(current.Snapshot, input.Snapshot, scope); !ok {
			fail(w, 403, code, msg)
			return
		}
	}
	if !sameInstallationPorts(manifest.Snapshot, finalSnapshot) {
		fail(w, 422, "installation_setting", "Порты подключения к прокси задаются при установке и не изменяются из панели")
		return
	}
	if e := policy.Validate(finalSnapshot, s.Runtime.Options); e != nil {
		fail(w, 422, "invalid_policy", e.Error())
		return
	}
	d := Draft{Version: input.Expected + 1, Snapshot: finalSnapshot, Updated: stamp()}
	if v.Role == "manager" {
		e = s.Store.ChangeDraftWithAssignments(d, v.User, "update_draft", input.Expected, v.User, newIDs)
	} else {
		e = s.Store.Change("draft", d, v.User, "update_draft", input.Expected)
	}
	if errors.Is(e, store.ErrConflict) {
		fail(w, 409, "revision_conflict", "Черновик уже изменён. Обновите страницу")
		return
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	out := d
	if v.Role == "manager" {
		scope, _ := s.managerScope(v)
		out.Snapshot = managerDraftView(d.Snapshot, scope)
		out.Snapshot.Expert = ""
	}
	respond(w, 200, out)
}
func (s *Server) createRevision(w http.ResponseWriter, r *http.Request, v session) {
	var input struct {
		Expected int `json:"expected_draft_version"`
	}
	if !decode(w, r, &input) {
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if d.Version != input.Expected {
		fail(w, 409, "revision_conflict", "Черновик уже изменён")
		return
	}
	// Manager may create a revision only if every changed object is in scope.
	if v.Role == "manager" {
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		active, _ := s.activeRevisionSnapshot()
		if draftChangesOutOfScope(d.Snapshot, active, scope) {
			fail(w, 403, "draft_out_of_scope", "Черновик содержит изменения вне вашей области")
			return
		}
	}
	rev := Revision{ID: store.ID(), DraftVersion: d.Version, Hash: policy.Hash(d.Snapshot), Compiler: policy.CompilerVersion, Image: s.Image, Created: stamp(), Snapshot: d.Snapshot}
	b, _ := json.Marshal(rev)
	if e := s.Store.InsertRevision(rev.ID, string(b), v.User, "create_revision"); e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 201, rev)
}
func (s *Server) revision(w http.ResponseWriter, id string) (Revision, bool) {
	var rev Revision
	e := s.Store.Get("revision:"+id, &rev)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Ревизия не найдена")
		return rev, false
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return rev, false
	}
	return rev, true
}
func (s *Server) getRevision(w http.ResponseWriter, r *http.Request, v session) {
	if rev, ok := s.revision(w, r.PathValue("id")); ok {
		respond(w, 200, rev)
	}
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request, v session) {
	rev, ok := s.revision(w, r.PathValue("id"))
	if !ok {
		return
	}
	result, e := s.previewSnapshot(rev.Snapshot)
	if e != nil {
		s.previewFailure(w, e)
		return
	}
	result["revision_id"] = rev.ID
	result["policy_hash"] = rev.Hash
	respond(w, 200, result)
}
func (s *Server) saveOperation(op Operation) error {
	b, e := json.Marshal(op)
	if e != nil {
		return e
	}
	return s.Store.UpdateOperation(op.ID, string(b))
}
func (s *Server) loadOperation(id string) (Operation, error) {
	var op Operation
	raw, e := s.Store.LoadOperation(id)
	if e != nil {
		return op, e
	}
	e = json.Unmarshal([]byte(raw), &op)
	return op, e
}
func (s *Server) getOperation(w http.ResponseWriter, r *http.Request, v session) {
	op, e := s.loadOperation(r.PathValue("id"))
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Операция не найдена")
		return
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 200, op)
}
func (s *Server) createOperation(w http.ResponseWriter, r *http.Request, v session) {
	// Idempotent replay must always win over the recovery gate — the operation
	// has already been accepted and may even be terminal, so returning 503
	// here would lock users out of legitimate replays after a transient
	// persistence failure on a separate operation.
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 128 {
		fail(w, 400, "invalid_key", "Нужен Idempotency-Key длиной 16–128 символов")
		return
	}
	var input operationInput
	if !decode(w, r, &input) {
		return
	}
	if input.Action != "validate" && input.Action != "apply" && input.Action != "rollback" {
		fail(w, 400, "invalid_action", "Неизвестное действие")
		return
	}
	// Rollback is admin-only; validate and apply are available to managers,
	// but apply additionally requires every changed object to be in scope.
	if input.Action == "rollback" && v.Role != "admin" {
		fail(w, 403, "admin_required", "Откат доступен только администратору")
		return
	}
	if input.Action != "validate" && !hasPermission(v, "apply") {
		fail(w, 403, "forbidden", "Недостаточно прав")
		return
	}
	b, _ := json.Marshal(input)
	hash := sha256.Sum256(b)
	requestHash := hex.EncodeToString(hash[:])
	oldID, oldHash, found, queryErr := s.Store.FindOperationByIdempotencyKey(key)
	if queryErr != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if found {
		// SHA256 hex output is fixed-length (64 bytes), so ConstantTimeCompare is
		// safe here. The hash itself is not secret, but keeping the compare path
		// uniform with csrf/password verifiers avoids a timing oracle that would
		// distinguish "key exists with mismatched hash" from "key absent" — both
		// surface to the client as 409, but the side channel would let an
		// attacker learn whether a victim's idempotency key is in use.
		if subtle.ConstantTimeCompare([]byte(oldHash), []byte(requestHash)) != 1 {
			fail(w, 409, "idempotency_conflict", "Ключ уже использован другим запросом")
			return
		}
		op, loadErr := s.loadOperation(oldID)
		if loadErr != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		respond(w, 202, op)
		return
	}
	if s.persistenceFailed.Load() {
		fail(w, 503, "recovery_required", "Не удалось сохранить исход предыдущей операции; требуется восстановление")
		return
	}
	if !s.opsMu.TryLock() {
		// A previous request may have inserted the operation between our SELECT and
		// this lock; re-check idempotency so a legitimate replay of the same key
		// returns the in-progress operation instead of a busy 409.
		retryID, retryHash, retryFound, retryErr := s.Store.FindOperationByIdempotencyKey(key)
		if retryErr != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		if retryFound {
			if subtle.ConstantTimeCompare([]byte(retryHash), []byte(requestHash)) != 1 {
				fail(w, 409, "idempotency_conflict", "Ключ уже использован другим запросом")
				return
			}
			op, loadErr := s.loadOperation(retryID)
			if loadErr != nil {
				fail(w, 503, "storage_unavailable", "Хранилище недоступно")
				return
			}
			respond(w, 202, op)
			return
		}
		fail(w, 409, "busy", "Другая операция ещё выполняется")
		return
	}
	locked := true
	defer func() {
		if locked {
			s.opsMu.Unlock()
		}
	}()
	rev, ok := s.revision(w, input.Revision)
	if !ok {
		return
	}
	if rev.Compiler != policy.CompilerVersion || rev.Image != s.Image {
		fail(w, 422, "version_mismatch", "Ревизия создана другой сборкой. Подготовьте новую")
		return
	}
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Нет рабочей конфигурации")
		return
	}
	if !sameInstallationPorts(m.Snapshot, rev.Snapshot) {
		fail(w, 422, "installation_setting", "Ревизия меняет порты подключения к прокси. Они задаются при установке")
		return
	}
	expected := ""
	if input.Expected != nil {
		expected = *input.Expected
	}
	if expected != m.Revision {
		fail(w, 409, "revision_conflict", "Действующая конфигурация уже изменилась")
		return
	}
	if input.Action == "apply" {
		validation, e := s.loadOperation(input.Validation)
		if e != nil || validation.Action != "validate" || validation.Revision != rev.ID || validation.Status != "validated" {
			fail(w, 422, "validation_required", "Сначала проверьте эту ревизию")
			return
		}
		// Manager apply: every changed client must be his, and a changed policy
		// must not be referenced by a foreign client (in draft or active).
		if v.Role == "manager" {
			scope, e := s.managerScope(v)
			if e != nil {
				fail(w, 503, "storage_unavailable", "Хранилище недоступно")
				return
			}
			active, _ := s.activeRevisionSnapshot()
			if draftChangesOutOfScope(rev.Snapshot, active, scope) {
				fail(w, 403, "draft_out_of_scope", "Черновик содержит изменения вне вашей области")
				return
			}
		}
	}
	if input.Action == "rollback" {
		n, e := s.Store.CountSucceededByRevision(rev.ID)
		if e != nil || n == 0 {
			fail(w, 422, "not_applied_revision", "Для отката нужна ранее успешно применённая ревизия")
			return
		}
	}
	op := Operation{ID: store.ID(), Action: input.Action, Revision: rev.ID, Status: "pending", Findings: []string{}, Created: stamp(), Updated: stamp()}
	raw, _ := json.Marshal(op)
	if e := s.Store.InsertOperation(op.ID, string(raw), key, requestHash, v.User, input.Action); e != nil {
		fail(w, 409, "operation_conflict", "Не удалось принять операцию")
		return
	}
	s.workers.Add(1)
	locked = false
	go func(op Operation) {
		defer s.workers.Done()
		defer s.opsMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		var runErr error
		if input.Action == "validate" {
			op.Status = "validating"
			if runErr = s.saveOperation(op); runErr == nil {
				runErr = s.Runtime.Validate(ctx, rev.Snapshot)
			}
			if runErr == nil {
				op.Status = "validated"
			} else {
				op.Status = "rejected"
			}
		} else {
			op.Status = "reloading"
			if runErr = s.saveOperation(op); runErr == nil {
				op.Status, runErr = s.Runtime.Apply(ctx, op.ID, rev.ID, expected, rev.Snapshot)
			} else {
				op.Status = "rejected"
			}
		}
		if runErr != nil {
			slog.Warn("operation failed", "operation_id", op.ID, "error", runErr)
			op.Findings = []string{"Проверка или применение не завершились успешно; смотрите локальный журнал процесса"}
			var pe *local.ParseError
			if errors.As(runErr, &pe) && pe.Line > 0 {
				// Surface only the rendered-config line Squid rejected; the
				// captured bungled-config body stays out of API responses.
				var n int
				var ok bool
				if rendered, rerr := policy.Render(rev.Snapshot, s.Runtime.Options); rerr == nil {
					n, ok = policy.UserSectionLine(rendered, pe.Line)
				}
				if rev.Snapshot.Mode == "expert" && ok {
					op.Findings = []string{fmt.Sprintf("Squid отклонил конфигурацию: строка %d экспертной секции", n)}
				} else {
					op.Findings = []string{fmt.Sprintf("Squid отклонил конфигурацию: строка %d итоговой конфигурации", pe.Line)}
				}
			}
		}
		op.Updated = stamp()
		if e := s.saveOperation(op); e != nil {
			s.persistenceFailed.Store(true)
			slog.Error("cannot persist operation outcome", "operation_id", op.ID, "error", e)
		}
	}(op)
	respond(w, 202, op)
}
func (s *Server) Wait() { s.workers.Wait() }
func (s *Server) certificates(w http.ResponseWriter, r *http.Request, v session) {
	items := []map[string]any{}
	for _, item := range []struct{ Ref, Purpose, Path string }{{"panel-server", "panel", s.PanelCert}, {"proxy-server", "proxy_server", s.Runtime.Options.ProxyCert}} {
		if item.Path == "" {
			continue
		}
		entry, ok := s.loadCachedCertificate(item)
		if !ok {
			fail(w, 503, "certificate_unavailable", "Метаданные сертификата недоступны")
			return
		}
		// Seal the response with the human-readable ref/purpose; the cache value
		// only carries parsed cert fields so the same cache entry can be reused
		// even if the caller relabels the ref/purpose in a future schema.
		out := map[string]any{}
		for k, val := range entry {
			out[k] = val
		}
		out["ref"] = item.Ref
		out["purpose"] = item.Purpose
		items = append(items, out)
	}
	respond(w, 200, items)
}

// loadCachedCertificate returns the parsed metadata for one PEM file, using a
// per-path cache invalidated by mtime/size. A cache miss parses the file from
// disk; a stale mtime evicts and re-parses. The stat-before-read sequence is
// preserved so an accidentally oversized PEM cannot spike memory.
func (s *Server) loadCachedCertificate(item struct{ Ref, Purpose, Path string }) (map[string]any, bool) {
	st, e := os.Stat(item.Path)
	if e != nil || st.Size() > 1024*1024 {
		return nil, false
	}
	s.certCacheMu.Lock()
	defer s.certCacheMu.Unlock()
	if cached, hit := s.certCache[item.Path]; hit && cached.mtime.Equal(st.ModTime()) && cached.size == st.Size() {
		return cached.item, true
	}
	b, e := os.ReadFile(item.Path)
	if e != nil || len(b) > 1024*1024 {
		return nil, false
	}
	rest := b
	public := []byte{}
	var leaf *x509.Certificate
	for len(rest) > 0 {
		block, tail := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = tail
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return nil, false
		}
		if leaf == nil {
			leaf = cert
		}
		public = append(public, pem.EncodeToMemory(block)...)
	}
	if leaf == nil {
		return nil, false
	}
	fp := sha256.Sum256(leaf.Raw)
	entry := map[string]any{
		"subject":            leaf.Subject.String(),
		"issuer":             leaf.Issuer.String(),
		"sans":               leaf.DNSNames,
		"fingerprint_sha256": hex.EncodeToString(fp[:]),
		"not_after":          leaf.NotAfter.UTC().Format(time.RFC3339),
		"public_chain_pem":   string(public),
	}
	s.certCache[item.Path] = certCacheEntry{mtime: st.ModTime(), size: st.Size(), item: entry}
	return entry, true
}

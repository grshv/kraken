package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"squid-control.local/squid/internal/policy"
	local "squid-control.local/squid/internal/runtime"
	"squid-control.local/squid/internal/store"
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
	Start time.Time
	Count int
}
type Server struct {
	persistenceFailed atomic.Bool
	PanelCert         string
	Store             *store.Store
	Runtime           *local.Manager
	Origin            string
	Static            http.Handler
	Image             string
	draftMu           sync.Mutex
	opsMu             sync.Mutex
	throttleMu        sync.Mutex
	attempts          map[string]attempt
	loginSlots        chan struct{}
	dummyHash         string
	workers           sync.WaitGroup
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }
func New(st *store.Store, rt *local.Manager, origin string, static http.Handler, image string) (*Server, error) {
	u, parseErr := url.Parse(origin)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("SC_PUBLIC_ORIGIN must be an https origin without trailing slash")
	}
	h, e := PasswordHash("dummy-" + store.ID())
	if e != nil {
		return nil, e
	}
	s := &Server{Store: st, Runtime: rt, Origin: origin, Static: static, Image: image, attempts: map[string]attempt{}, loginSlots: make(chan struct{}, 2), dummyHash: h}
	var d Draft
	e = st.Get("draft", &d)
	if errors.Is(e, sql.ErrNoRows) {
		e = st.Put("draft", Draft{Version: 1, Snapshot: policy.Empty(), Updated: stamp()})
	}
	if e != nil {
		return nil, e
	}
	// Reconcile abandoned operations after the runtime has recovered its filesystem state.
	rows, e := st.DB.Query("SELECT id,body FROM operations")
	if e != nil {
		return nil, e
	}
	var abandoned []Operation
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return nil, e
		}
		var op Operation
		if e = json.Unmarshal([]byte(raw), &op); e != nil {
			rows.Close()
			return nil, e
		}
		if op.Status == "pending" || op.Status == "validating" || op.Status == "reloading" {
			abandoned = append(abandoned, op)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
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
	mux.HandleFunc("GET /api/v1/status", s.authenticated(s.status))
	mux.HandleFunc("GET /api/v1/revisions", s.authenticated(s.listRevisions))
	mux.HandleFunc("GET /api/v1/configuration/expert-template", s.authenticated(s.expertTemplate))
	mux.HandleFunc("GET /api/v1/configuration/draft", s.authenticated(s.getDraft))
	mux.HandleFunc("PUT /api/v1/configuration/draft", s.authenticated(s.updateDraft))
	mux.HandleFunc("POST /api/v1/revisions", s.authenticated(s.createRevision))
	mux.HandleFunc("GET /api/v1/revisions/{id}", s.authenticated(s.getRevision))
	mux.HandleFunc("GET /api/v1/revisions/{id}/preview", s.authenticated(s.preview))
	mux.HandleFunc("POST /api/v1/operations", s.authenticated(s.createOperation))
	mux.HandleFunc("GET /api/v1/operations/{id}", s.authenticated(s.getOperation))
	mux.HandleFunc("GET /api/v1/events", s.authenticated(s.events))
	mux.HandleFunc("GET /api/v1/certificates", s.authenticated(s.certificates))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		// Go 1.22 ServeMux would otherwise 405 a registered path with the wrong
		// method; the generic catch-all here returns a clearer 404 to legacy
		// clients, while MethodNotAllowed from registered patterns still surfaces.
		fail(w, 404, "not_found", "Неизвестный endpoint")
	})
	mux.Handle("/", s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := store.ID()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
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
	if e := policy.Validate(input.Snapshot, s.Runtime.Options); e != nil {
		fail(w, 422, "invalid_policy", e.Error())
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	d := Draft{Version: input.Expected + 1, Snapshot: input.Snapshot, Updated: stamp()}
	e := s.Store.Change("draft", d, v.User, "update_draft", input.Expected)
	if errors.Is(e, store.ErrConflict) {
		fail(w, 409, "revision_conflict", "Черновик уже изменён. Обновите страницу")
		return
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 200, d)
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
	rev := Revision{ID: store.ID(), DraftVersion: d.Version, Hash: policy.Hash(d.Snapshot), Compiler: policy.CompilerVersion, Image: s.Image, Created: stamp(), Snapshot: d.Snapshot}
	tx, e := s.Store.DB.Begin()
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	defer tx.Rollback()
	b, _ := json.Marshal(rev)
	if _, e = tx.Exec("INSERT INTO kv VALUES(?,?)", "revision:"+rev.ID, string(b)); e == nil {
		_, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", v.User, "create_revision", rev.ID, stamp())
	}
	if e != nil || tx.Commit() != nil {
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
	conf, e := policy.Render(rev.Snapshot, s.Runtime.Options)
	if e != nil {
		fail(w, 422, "invalid_policy", e.Error())
		return
	}
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Нет рабочей конфигурации")
		return
	}
	var previous any
	if m.Revision != "" {
		previous = m.Revision
	}
	respond(w, 200, map[string]any{"revision_id": rev.ID, "policy_hash": rev.Hash, "summary": semanticDiff(m.Snapshot, rev.Snapshot), "config_diff": configDiff(m.Config, conf), "generated_config": conf, "warnings": []string{"Новые правила не гарантируют закрытие уже открытых CONNECT-туннелей"}, "expected_applied_revision": previous})
}
func (s *Server) saveOperation(op Operation) error {
	b, e := json.Marshal(op)
	if e != nil {
		return e
	}
	_, e = s.Store.DB.Exec("UPDATE operations SET body=? WHERE id=?", string(b), op.ID)
	return e
}
func (s *Server) loadOperation(id string) (Operation, error) {
	var op Operation
	var raw string
	e := s.Store.DB.QueryRow("SELECT body FROM operations WHERE id=?", id).Scan(&raw)
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
	b, _ := json.Marshal(input)
	hash := sha256.Sum256(b)
	requestHash := hex.EncodeToString(hash[:])
	var oldID, oldHash string
	if queryErr := s.Store.DB.QueryRow("SELECT id,request_hash FROM operations WHERE idempotency_key=?", key).Scan(&oldID, &oldHash); queryErr == nil {
		if oldHash != requestHash {
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
	} else if !errors.Is(queryErr, sql.ErrNoRows) {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
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
		var retryID, retryHash string
		if retryErr := s.Store.DB.QueryRow("SELECT id,request_hash FROM operations WHERE idempotency_key=?", key).Scan(&retryID, &retryHash); retryErr == nil {
			if retryHash != requestHash {
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
	if input.Action != "rollback" && (rev.Compiler != policy.CompilerVersion || rev.Image != s.Image) {
		fail(w, 422, "version_mismatch", "Ревизия создана другой сборкой. Подготовьте новую")
		return
	}
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Нет рабочей конфигурации")
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
	}
	if input.Action == "rollback" {
		var n int
		e = s.Store.DB.QueryRow("SELECT COUNT(*) FROM operations WHERE json_extract(body,'$.revision_id')=? AND json_extract(body,'$.status')='succeeded'", rev.ID).Scan(&n)
		if e != nil || n == 0 {
			fail(w, 422, "not_applied_revision", "Для отката нужна ранее успешно применённая ревизия")
			return
		}
	}
	op := Operation{ID: store.ID(), Action: input.Action, Revision: rev.ID, Status: "pending", Findings: []string{}, Created: stamp(), Updated: stamp()}
	raw, _ := json.Marshal(op)
	tx, e := s.Store.DB.Begin()
	if e != nil {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO operations VALUES(?,?,?,?)", op.ID, key, requestHash, string(raw)); e == nil {
		_, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", v.User, input.Action, op.ID, stamp())
	}
	if e != nil || tx.Commit() != nil {
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
func (s *Server) events(w http.ResponseWriter, r *http.Request, v session) {
	if r.URL.Query().Get("cursor") != "" || r.URL.Query().Get("client_id") != "" {
		fail(w, 422, "filter_not_implemented", "В первом срезе доступны только ограниченный хвост и фильтр отказов")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid_limit", "limit должен быть от 1 до 200")
			return
		}
		limit = n
	}
	items, e := s.Runtime.Events()
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			fail(w, 503, "logs_unavailable", "Журнал недоступен")
			return
		}
	}
	if r.URL.Query().Get("failures_only") == "true" {
		filtered := []map[string]any{}
		for _, item := range items {
			pr, _ := item["proxy_result"].(string)
			if strings.Contains(pr, "DENIED") || strings.Contains(pr, "FAIL") {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) > limit {
		items = items[:limit]
	}
	respond(w, 200, map[string]any{"items": items, "next_cursor": nil, "gap": true, "observed_at": stamp()})
}
func (s *Server) certificates(w http.ResponseWriter, r *http.Request, v session) {
	items := []map[string]any{}
	for _, item := range []struct{ Ref, Purpose, Path string }{{"panel-server", "panel", s.PanelCert}, {"proxy-server", "proxy_server", s.Runtime.Options.ProxyCert}} {
		if item.Path == "" {
			continue
		}
		// Stat before ReadFile so an accidentally over-sized PEM cannot spike memory.
		// 1 MiB is also the bound enforced for embedded bodies elsewhere in this handler.
		st, e := os.Stat(item.Path)
		if e != nil || st.Size() > 1024*1024 {
			fail(w, 503, "certificate_unavailable", "Метаданные сертификата недоступны")
			return
		}
		b, e := os.ReadFile(item.Path)
		if e != nil || len(b) > 1024*1024 {
			fail(w, 503, "certificate_unavailable", "Метаданные сертификата недоступны")
			return
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
				fail(w, 503, "certificate_unavailable", "Некорректный сертификат")
				return
			}
			if leaf == nil {
				leaf = cert
			}
			public = append(public, pem.EncodeToMemory(block)...)
		}
		if leaf == nil {
			fail(w, 503, "certificate_unavailable", "Сертификат не найден")
			return
		}
		fp := sha256.Sum256(leaf.Raw)
		items = append(items, map[string]any{"ref": item.Ref, "purpose": item.Purpose, "subject": leaf.Subject.String(), "issuer": leaf.Issuer.String(), "sans": leaf.DNSNames, "fingerprint_sha256": hex.EncodeToString(fp[:]), "not_after": leaf.NotAfter.UTC().Format(time.RFC3339), "public_chain_pem": string(public)})
	}
	respond(w, 200, items)
}

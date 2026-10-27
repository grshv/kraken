# Незакрытые находки ревью

Статус: Resolved. Дата: 2026-10-27.

Контекст: кодовый ревью через codebase-memory-mcp и 5 доменных ревью-агентов
обнаружил 0 Critical и 15 High-находок. Все High и ~20 Medium закрыты коммитом
`b75ca19`. Этот файл удерживал незакрытый остаток — Level-Low code-smell и
спецификационные расхождения — и теперь сам закрывается проходкой по всем
доменам. Подробности фиксируются ниже, чтобы при следующем ревью можно было
вернуться к истории решений.

## Закрытые находки

### Безопасность (4/4)

- [x] **HSTS preload** — `Strict-Transport-Security: max-age=31536000;
  includeSubDomains; preload` в `server.go`. Commit `d69f1c8`.
- [x] **Idempotency hash compare** — `subtle.ConstantTimeCompare` на обоих
  путях (initial SELECT и post-TryLock re-check). Commit `d69f1c8`.
- [x] **Login throttle sliding window** — `attempt.Times []time.Time`,
  индивидуальный expiry для каждой попытки. Граница 5-at-0:59 + 5-at-1:01
  закрыта. Commit `d69f1c8`.
- [x] **Audit scope** — добавлены `rate_limited`, `csrf_failed` (login + auth
  middleware), `logout`, `session_capacity_reached`. Helper `auditAudit`
  логирует persistence failure, не маскируя контроль. Commit `d69f1c8`.

### Runtime (3/3)

- [x] **Configurable timeouts** — `Manager.ParseTimeout`/`ReloadTimeout`/
  `VerifyBudget` + env `SC_PARSE_TIMEOUT` / `SC_RELOAD_TIMEOUT` /
  `SC_VERIFY_BUDGET` (диапазон (0, 5 m]). Commit `07684ef`.
- [x] **Events dedup ID с timestamp** — `sha256(timestamp + line)` вместо
  `sha256(line)`. Одинаковые log-строки в разное время больше не схлопываются.
  Commit `07684ef`.
- [x] **Log-парсер sanitize** — `proxy_result` (≤64) и `method` (≤16) фильтруются
  через `sanitizeLogToken` до ASCII subset (alnum / `_` / `-` / `/`).
  Commit `07684ef`.

### HTTP API & Server (4/4)

- [x] **405 vs 404** — `/api/` catch-all удалён, Go 1.22 ServeMux возвращает
  native 405 для wrong-method на зарегистрированном пути. Unknown `/api/`
  paths fall through к `/` handler, который отдаёт JSON 404 envelope.
  Commit `d69f1c8`.
- [x] **Cache-Control split** — HTML/JSON/errors `no-store`; hashed vite
  assets `/assets/*` `public, max-age=31536000, immutable` после успешного
  stat в `web/embed.go`. 404 на `/assets/*` наследует `no-store`. Commit
  `d69f1c8`.
- [x] **Certificates mtime cache** — `s.certCache[path]` с mtime+size
  инвалидацией; parsing выполняется только при изменении PEM-файла.
  Stat-before-read сохранён. Commit `d69f1c8`.
- [x] **Raw SQL extraction (operations + revisions)** — `store.InsertOperation`,
  `UpdateOperation`, `LoadOperation`, `FindOperationByIdempotencyKey`,
  `CountSucceededByRevision`, `ListOperationBodies`, `InsertRevision`,
  `ListRevisions`. HTTP handlers вызывают typed методы. Sessions/users raw
  SQL в `auth.go` оставлен — ties к multi-step tx в session state machine;
  буду жить с этим, пока multi-step session logic не расслоится. Commit
  `d69f1c8`.

### Policy & Store (7/7)

- [x] **3128/3129 reservation** — documented в `policies-and-tls.md`: 3128/3129
  конвенциональные Squid-порты (не зарезервированы), 3130 всегда эмитируется
  как probe listener, 8443 reserved для panel. Verify fallback target
  изменён с `127.0.0.1:3128` на канонический `127.0.0.1:3130`. Commit `54dda6f`
  (verify) + `1b23863` (doc).
- [x] **Time types consistency** — `audit_events.created_at` переведён из
  TEXT RFC3339 в INTEGER unix через migration 002 (table-rebuild pattern).
  `store.Audit`, `InsertOperation`, `InsertRevision`, `Change`, `ResetPassword`,
  `login`, `Prune` — все используют `time.Now().Unix()` / `.Unix()`. Range
  deletes на Prune теперь O(log n) на created_at index. Migration framework
  (`migrations.Ordered`) + `schema_migrations` versioning. Commits `54dda6f`
  (migration) + `d69f1c8` (audit calls).
- [x] **IPv4-mapped private clarification** — зафиксировано в
  `policies-and-tls.md`: `::ffff:0:0/96` в `sc_protected_dst` блокирует
  mapped-private даже при `AllowPrivateDestinations=true`. Это safe-side
  (строже, чем требует семантика). Commit `1b23863`.
- [x] **validateExpert vs Render property test** — `TestExpertValidateRender
  Equivalence` покрывает continuation, comment-hidden forbidden directives,
  trailing backslash, quoted hash. Commit `54dda6f`.
- [x] **draftMu/opsMu single-process assumption** — ADR 0009 фиксирует
  сознательное ограничение «один executable, один процесс» и последствия
  для multi-instance. Commit `1b23863`.
- [x] **Multi-file bundle vs single active.conf** — `configuration-lifecycle.md`
  отмечает divergention: реализация — single-file `active.conf` replaced
  wholesale; multi-file bundle — future TLS-profile rotation enhancement.
  Commit `1b23863`.
- [x] **Journal stage granularity** — `configuration-lifecycle.md` явно
  фиксирует implementation stages (`first_run`/`promoting`/`reloading`/
  `rolling_back`/`terminal`+outcome) vs spec StateDiagram (`accepted`/
  `staged`/`validating`/`verifying`). OpenAPI Operation.status enum
  trimmed до emitted subset. Alignment (emit more stages или spec trim)
  оставлен на future slice, расхождение явно documented. Commit `1b23863`.

### Frontend (9/9)

- [x] **Polling без AbortController** — events + status poll делят один
  AbortController; `run()`'s operation poll получает signal от `work()`.
  Cleanup abort'ит in-flight fetches. Commit `83f310b`.
- [x] **Глобальный `let csrf`** — `useState` в App с `setCsrf` на login/logout/
  session-expiry. api() читает токен из closure. Commit `83f310b`.
- [x] **`model.ts` full types** — `Session.expires_at`, `Status.observed_config
  _hash`, `Operation.created_at`/`updated_at`, `Event.id`/`client_id`/
  `tls_mode`/`proxy_http_status`/`duration_ms`/`bytes_observed`/`revision_id`,
  `Client.description`. Commit `83f310b`.
- [x] **`Client.description` UI** — `input maxLength=1024` в клиентской модалке
  + subtitle под именем в таблице. Commit `83f310b`.
- [x] **Labels covers all statuses** — enum расширен до `accepted`/`staged`/
  `draft`/`promoting`/`verifying`/`rolling_back`. Commit `83f310b`.
- [x] **`as never` type-escape** — `SettingsPanel.number()` принимает
  `NumberKeys` constraint (`Settings[K] extends number ? K : never`);
  `safe_ports` и `connect_ports` onChange handlers убрали cast. Commit
  `83f310b`.
- [x] **Playwright `/tmp` default** — `os.tmpdir()` + `path.join`. Commit
  `83f310b`.
- [x] **Playwright один гигантский `test()`** — split на `test.describe.serial`
  с beforeAll-общей page. Три теста: clients+apply / settings+rollback /
  expert+screenshots+logout. Commit `83f310b`.
- [x] **`web/embed.go` игнорирует ошибку `fs.Sub`** — package init() log.Fatals
  на пустой/отсутствующей dist. Asset Cache-Control переопределяется здесь же
  после успешного stat. Commit `d69f1c8`.

### Спецификационные расхождения (6/6)

- [x] **S1 `Operation.status` enum overspecified** — OpenAPI trimmed до 8
  actually-emitted values. Description объясняет divergention от state
  diagram. Commit `1b23863`.
- [x] **S4 `SquidSettings.maximum_object_kb` ceil** — OpenAPI description
  уточняет динамический потолок `<= cache_memory_mb * 1024`. Commit `1b23863`.
- [x] **S5 `EventPage.gap`** — context-aware: `gap = len(items) >= limit OR
  fileSize > 128 KiB`. Commit `1b23863`.
- [x] **S8 `image_digest` в Manifest** — `Manifest.ImageDigest` поле,
  populated from `m.Image` на first_run и Apply. Commit `1b23863`.
- [x] **S10 `data-model.md` vs `001.sql` schema divergence** — ADR 0008
  фиксирует kv-blob MVP как сознательный компромисс. Commit `1b23863`.
- [x] **S11 `degraded` не disable UI-кнопки** — mutating buttons
  (Проверить/Применить/Откатить) disable при `status.runtime.state ===
  "degraded"`. Commit `83f310b`.

### ADRs (2/2)

- [x] **ADR 0008: kv-blob MVP без реляционных таблиц** — `docs/adr/0008-kv-blob-
  mvp.md`. Зафиксировано как сознательный компромисс первого среза.
- [x] **ADR 0009: single-process concurrency assumption** — `docs/adr/0009-
  single-process-concurrency.md`. Зафиксировано ограничение «один executable,
  один процесс» с последствиями для multi-instance workloads.

## Явно оставленные code-smell (не блокеры)

- **Sessions/users raw SQL в `auth.go`** — `readSession` (SELECT + UPDATE last_
  seen_at в одной tx), `newSession` (DELETE INSERT in tx), `ResetPassword`
  (UPDATE + DELETE + INSERT in tx), `logout` (DELETE). Schema leak минимальный,
  но multi-step transaction logic плотно связана с session state machine.
  Вынесение в `store.Session...` methods — отдельный slice, когда session
  audit/scope расширится.
- **`audit_events.actor` для anonymous paths** — empty actor `""`. Не нарушает
  контракт (schema NOT NULL, пустая строка валидна), но monitoring-grade
  cleanup может потребовать `'anonymous'` или конкретного remote host как
  actor. Не критично.

## Что уже было закрыто ранее (для справки)

Коммит `b75ca19` закрыл все 15 High и ~20 Medium находок. Подробности — в
commit message; среди крупного: snmp/icp/htcp listener escape, dangerousPorts
denylist, anonymous session DoS, per-account throttle, Argon2id OOM-вектор, TLS
pin, idempotency race, recovery-middleware, apply rollback after LKG, verify
budget 30s, startup race, first-run journal marker, polling 50→180s, api()
content-type + 401 handling, Status TryLock, Atomic MkdirAll/cleanup,
ReadResponse LimitReader, events validation, statusRecorder + request-ID,
shutdown force-exit, readSession TOCTOU, bootstrap username regex,
persistenceFailed gate lift, certificates stat-before-read, store.Prune,
retention, schema_migrations + индексы, SettingsPanel key=remount, ARIA tab
keyboard, modal Escape/focus-trap, vite proxy, SPA fallback + dir-listing off,
findings `<li>`, limitedBuffer truncation marker, errors.Join dedup.

Проходка по Level-Low закрыта шестью коммитами:
`d69f1c8` (security + HTTP API + embed),
`07684ef` (runtime),
`54dda6f` (policy + store + migration 002),
`83f310b` (frontend + S11),
`1b23863` (specs + ADRs + docs).

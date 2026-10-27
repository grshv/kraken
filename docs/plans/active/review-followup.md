# Незакрытые находки ревью

Статус: Active. Дата: 2026-10-27.

Контекст: кодовый ревью через codebase-memory-mcp и 5 доменных ревью-агентов
обнаружил 0 Critical и 15 High-находок. Все High и большинство Medium закрыты
коммитом `b75ca19` «Security, apply pipeline and frontend resilience fixes» с
проходом `make check` + `make integration`. Этот файл удерживает незакрытый
остаток — Level-Low code-smell и спецификационные расхождения, не блокирующие
безопасность или контракт. Полный отчёт ревью не хранится в репозитории, чтобы
не плодить лежачие артефакты; носителем комментариев является этот файл.

Приоритет: закрыть при следующем регулярном срезе; ничего срочного. Любое
изменение здесь требует `make docs-check`.

## (Не закрывал — Level Low)

### Безопасность (vzorost)

- [ ] **HSTS preload (`src/cmd/control/main.go`, `src/internal/httpapi/server.go:192`).**
  Header `Strict-Transport-Security: max-age=31536000` без `includeSubDomains; preload`.
  Для closed-management домена с единым админ-панельным типом рекомендуется
  добавить оба токена после убеждения, что нет других HTTP-поддоменов. Не
  критично: панель обычно на выделенном хосте, origin check отдельно.
- [ ] **Idempotency hash сравнивается строкой (`src/internal/httpapi/server.go`).**
  `oldHash != requestHash` — обычное сравнение, не `subtle.ConstantTimeCompare`.
  SHA256 от request body — не секрет, и timing-атака на идентичный/неидентичный
  хэш exploitable слабо. Привести к constant-time ради консистентности с CSRF
  и password verifier.
- [ ] **Login throttle: fixed window (`src/internal/httpapi/auth.go`).**
  Окно 1 минута, throttle держит `entry.Start`. На границе окна можно сделать
  5 запросов в 0:59 + 5 в 1:01 = 10 за 2 секунды. Rolling/sliding window или
  decay-mitigation устойчивее. Per-account счётчик добавлен (20/min), но сам
  window-fixed подход остался.
- [ ] **Audit scope не покрывает все security-relevant events (`src/internal/httpapi/auth.go`).**
  Сейчас пишется `login`, `login_failed`, `password_reset`, `create_revision`,
  `update_draft`, action операции. Не пишется: `rate_limited`, `csrf_failed`,
  `logout`, `session_capacity_reached`. Для security monitoring было бы полезно.

### Runtime

- [ ] **Parse/reload timeout захардкожены (`src/internal/runtime/manager.go`).**
  `15 * time.Second` для parse, `10 * time.Second` для reload,
  `verifyBudget = 30 * time.Second`. `configuration-lifecycle.md:72` обещает
  «значения конфигурируемы». Вынести в поля `Manager` или env (как
  `SC_VERIFY_BUDGET`).
- [ ] **Events dedup ID на SHA256 лог-строки (`src/internal/runtime/manager.go:475-479`).**
  Одинаковые log-строки получают одинаковые ID и могут схлопнуться клиентом при
  дедупе. Для индексации событий хорошо, для уникальности — нет. Альтернатива:
  `sha256(line) + timestamp` или порядковый номер от cursor.
- [ ] **Log-парсер частично валидирует поля (`src/internal/runtime/manager.go:428-503`).**
  Source IP валидируется через `netip.ParseAddr`, duration/count clamped, но
  `proxy_result` (parts[3]) и `method` (parts[5]) берутся как есть. Логи —
  недоверенные данные per AGENTS.md. JSON-кодирование экранирует, CSP защищает,
  но sanitize на сервере был бы надёжнее.

### HTTP API & Server

- [ ] **405 маскируется в 404 (`src/internal/httpapi/server.go:183-185`).**
  Catch-all `/api/` поглощает запросы с неподдерживаемым методом к
  зарегистрированным путям. REST-клиенты видят 404 вместо 405 и не могут отличить
  «нет endpoint» от «не тот метод». Go 1.22 ServeMux умеет `MethodNotAllowed`,
  но паттерн `/api/` перекрывает. Альтернатива: опустить catch-all, отдать на
  откуп default 404 (менее информативно, но честнее по RFC).
- [ ] **`Cache-Control: no-store` на статике (`src/internal/httpapi/server.go:188`).**
  Wrapper ставит заголовок на все ответы, включая `assets/index-*.js` с
  content-hash в имени. Браузер рефетчит каждый JS/CSS на каждой навигации —
  performance, не security. Сделать: `no-store` для HTML, `immutable;
  max-age=31536000` для `/assets/*` с hash-именем.
- [ ] **`certificates` без кеширования (`src/internal/httpapi/server.go:517-557`).**
  Каждый запрос парсит PEM-файлы (до 1 MiB) и считает fingerprint. Для polling
  панели лишняя нагрузка. Кешировать на 10-30 s с инвалидацией по mtime.
- [ ] **Raw SQL в handler вместо методов store (`src/internal/httpapi/configuration.go:36-40`, `src/internal/httpapi/server.go:257,312-329,430`).**
  `listRevisions`, `createRevision`, `createOperation`, `saveOperation`,
  `loadOperation`, abandoned-ops reconciliation в `New` — raw `s.Store.DB`.
  `store.Store` экспортирует только `Get`/`Put`/`Change`/`Audit`. Схема
  `operations`/`kv`/`audit_events` утекает в HTTP-слой. Не нарушает границ
  доверия (httpapi уже доверенная зона), но мешает тестировать и менять схему.
  Вынести в `Store.ListRevisions`, `Store.InsertOperation` и т.д.

### Policy & Store

- [ ] **3128/3129 не зарезервированы явно (`src/internal/policy/settings.go:42`).**
  Резервированы только `3130` (probe) и `8443` (panel). Однако `manager.go:353,354`
  использует 3128 как legacy probe target — конфиг с `http_port 3128` создаст два
  listener-а. Реальной уязвимости нет (защитная проверка строки в `http_port
  127.0.0.1:3130\n` всё ещё срабатывает), но意图не очевиден. Документировать или
  зарезервировать 3128/3129 с понятным сообщением об ошибке. Caveat: default
  `Settings.HTTPPort=3128` уже занят, нужно либо переопределить дефолт, либо
  оставить как есть и только документировать.
- [ ] **Несогласованность типов времени (`src/migrations/001.sql:5-6` vs `src/internal/httpapi/auth.go:97,108,180`).**
  `audit_events.created_at` — TEXT RFC3339, `sessions.expires_at`/`last_seen_at`
  — INTEGER unix. Не уязвимость, но мешает будущим range-запросам. Привести к
  INTEGER unix везде в схеме.
- [ ] **`::ffff:0:0/96` vs IPv4-mapped private — непредсказуемо (`src/internal/policy/policy.go:210-211,213-215`).**
  `sc_protected_dst` включает `::ffff:0:0/96` (весь IPv4-mapped диапазон), а
  `sc_private_dst` содержит plain IPv4 приватные сети. При
  `AllowPrivateDestinations=true` приватные IPv4 destinations открываются, но их
  IPv4-mapped аналоги (`::ffff:10.0.0.5`) остаются заблокированными. Это
  safe-side (строже, чем нужно), но непредсказуемо. Либо явно исключить
  mapped-private из `sc_protected_dst`, либо документировать выбор.
- [ ] **`validateExpert` vs `Render` дивергенция (`src/internal/policy/settings.go:102-167` vs `src/internal/policy/policy.go:218-224`).**
  `validateExpert` анализирует нормализованную форму (вырезанные комментарии,
  merged line-continuations), `Render` пишет оригинальный `s.Expert`. Для
  проверенных случаев они дают тот же токен, что увидит Squid. Добавить
  property-тест на эквивалентность токенизации между merged/physical формами —
  explicit guard.
- [ ] **`draftMu`/`opsMu` — single-process assumption (`src/internal/store/store.go:81-112`, `src/internal/httpapi/server.go:205-237,312-318`).**
  Go-mutex'ы в `Change`, `createRevision`, `saveOperation` безопасны только в
  одном процессе. `SetMaxOpenConns(1)` тоже single-writer. Соответствует ADR
  0006 «один executable, один процесс», но при будущей горизонтальной
  масштабируемости потребует пересмотра. Зафиксировать как known-ограничение в
  новом ADR.
- [ ] **Многофайловый bundle vs single `active.conf` (`docs/architecture/configuration-lifecycle.md`).**
  Spec: «многофайловый bundle выбирается одной ссылкой/корневым файлом».
  Реализация — single-file `active.conf` заменяется целиком. Для MVP достаточно,
  но future TLS-profile rotation с собственным cert bundle потребует расширения.
  Не code-fix; документировать расхождение явно.
- [ ] **Journal stage granularity (`docs/architecture/configuration-lifecycle.md`).**
  Spec: стадии `staged`/`validating`/`verifying`. Реализация: `promoting`/
  `reloading`/`rolling_back`/`terminal`. Recovery корректен, observability
  granularity ниже. Либо привести код к spec-стадиям, либо спецификацию к коду.

### Frontend

- [ ] **Polling без `AbortController` (`src/web/src/main.tsx:69-76,285-304`).**
  `setInterval(refresh, 10000)` запускается без abort; `active`-флаг защищает
  `setState`, но pending fetch'и догоняют при быстрых tab-переключениях.
  То же для `/operations/{id}` poll в `run()`. Использовать `AbortController`
  на каждый poll и `clearInterval` + abort в cleanup.
- [ ] **Глобальный `let csrf` (`src/web/src/main.tsx:31`).**
  Module-level mutable state. В SPA с одним root это работает, но при
  multi-tab/multi-session работе или в тестах с пересозданием root возможны
  гонки. Перенести в `useState` в `App` и пробрасывать через props/context.
- [ ] **`model.ts` неполон vs openapi (`src/web/src/model.ts:38-67`).**
  `Status` без `observed_config_hash`; `Session` без `expires_at`;
  `Operation` без `created_at`/`updated_at`; `Event` без `id`/`client_id`/
  `tls_mode`/`proxy_http_status`/`duration_ms`/`bytes_observed`/`revision_id`.
  UI не использует эти поля, но type drift — будущий код упадёт в рантайме
  вместо compile-time. Расширить типы под openapi required.
- [ ] **`Client.description` не реализован в UI (`src/web/src/model.ts:1-9`, `src/web/src/main.tsx`).**
  OpenAPI + Go `policy.Client.Description` объявлены (optional, maxLength 1024),
  UI не отправляет и не показывает. Фича заявлена в контракте, недоступна.
- [ ] **`labels` не покрывает неиспользуемые статусы (`src/web/src/main.tsx:18-30`).**
  `Operation.status` enum в openapi включает `accepted`, `staged`, `promoting`,
  `verifying`, `rolling_back`, которые `server.go` и `manager.go` не эмиттят.
  Если `latest_status` исторической ревизии окажется `"draft"` (по умолчанию
  COALESCE), Pills покажет сырой токен. Либо дополнить labels, либо выкинуть
  лишние значения из openapi enum.
- [ ] **`as never` type-escape (`src/web/src/SettingsPanel.tsx:40`).**
  `Number(e.target.value) as never` — обход union `Settings[K]`. Типизировать
  `number`-функцию отдельно через constraint `extends number`.
- [ ] **Playwright `panel.spec.ts` дефолт `/tmp` (`src/web/tests/panel.spec.ts:108`).**
  `process.env.SC_SCREENSHOT_DIR || "/tmp/squid-control-ui"`. AGENTS проекта
  предпочитает pre-approved temp dir (см. opencode env). Заменить на
  `/private/var/folders/.../T/opencode` или аналогичный approved путь.
- [ ] **Playwright один гигантский `test()` (`src/web/tests/panel.spec.ts:9-126`).**
  ~120 строк на весь e2e flow. Любой ранний fail инвалидирует остальные шаги.
  Разбить на `test.describe.serial` с инстанцируемой fixture-state.
- [ ] **`web/embed.go` игнорирует ошибку `fs.Sub`.**
  `root, _ := fs.Sub(assets, "dist")`. Если `dist/` пуст (build не выполнен),
  `fs.Sub` возвращает nil; поведение `fs.Stat(nil, ...)` неочевидно. Явный
  `log.Fatal` на старте, если dist пуст.

## Спецификационные расхождения (S)

- [ ] **S1 `Operation.status` enum overspecified (`api/openapi.json` vs `server.go`, `manager.go`).**
  Контракт обещает 11 статусов (`accepted`, `staged`, `promoting`, `verifying`,
  `rolling_back` + 6 эмиттящихся). Реализация эмиттит 6; лишние значения —
  внутренние `Journal.Stage`, ошибочно поднятые в публичный enum. Либо дополнить
  enum только эмиттящимися, либо реализовать недостающие stage-записи.
  Дубль с Journal stage granularity выше.
- [ ] **S4 `SquidSettings.maximum_object_kb` ceil расходится (`docs/architecture/policies-and-tls.md`, `src/web/src/SettingsPanel.tsx:197`, `src/internal/policy/settings.go:62`).**
  openapi max = 1048576 (1 GiB); UI ставит `max={settings.cache_memory_mb *
  1024}`; сервер отвергает `MaximumObjectKB > CacheMemoryMB*1024`. Spec
  non-authoritative — привести openapi к фактическому динамическому потолку
  через описание, либо вынести в separate constraint.
- [ ] **S5 `EventPage.gap` всегда `true` (`src/internal/httpapi/configuration.go:67`).**
  openapi: «First slice returns a bounded tail with gap=true and no cursor; cursor
  filters return 422». Сервер всегда возвращает `"gap": true` и
  `"next_cursor": nil`. Технически соответствует (cursor не реализован), но
  фиксированный `true` не отражает реальное состояние. Привести к контекстному
  значению после реализации cursor pagination.
- [ ] **S8 `image_digest` в Manifest — отсутствует (`src/internal/runtime/manager.go:32-38`).**
  Spec: «image digest» и «capabilities profile» в immutable bundle. Компенсируется
  `policy.CompilerVersion` + проверкой `rev.Image == s.Image` в createOperation
  (`server.go:396`). Добавить `image_digest` в Manifest для полноты аудита.
- [ ] **S10 `data-model.md` vs `001.sql` schema divergence.**
  Spec описывает реляционные таблицы (`clients`/`client_networks`/`policies`/
  `destinations`/`tls_profiles`/...). Реализация — kv-blob для `draft`/
  `revision:*`. Обоснованный MVP-компромисс; `schema_migrations` + индексы
  добавлены коммитом b75ca19, но явный ADR «kv-blob MVP без реляционных таблиц»
  не написан. См. ADR-заметку ниже.
- [ ] **S11 `degraded` не disable UI-кнопки (`docs/product/interface.md:32` vs `src/web/src/main.tsx:440-445`).**
  Interface.md: «degraded запрещает новые runtime mutations». UI показывает alert,
  но кнопки save/freeze/apply не disable специально по `status.runtime.state ===
  "degraded"` — только по `busy`. Сервер отклонит, но UX не предупреждает.
  Добавить `disabled={busy || status?.runtime.state === "degraded"}` на мутирующие
  кнопки.

## Требует ADR

- [ ] **ADR: kv-blob MVP без реляционных таблиц.**
  Зафиксировать, что `001.sql` намеренно использует kv-blob для `draft`/
  `revision:*` вместо реляционной схемы из `data-model.md`. Целостность —
  application layer, FK на sessions не навешивается (anonymous CSRF holder).
  Срок: до любой миграции, меняющей схему ревизий.
- [ ] **ADR: single-process concurrency assumption.**
  Зафиксировать, что `draftMu`/`opsMu` Go-mutex'ы + `SetMaxOpenConns(1)` — это
  сознательное ограничение «один executable, один процесс» (ADR 0006).
  Multi-instance панель потребует пересмотра concurrency-модели store и
  idempotency-чека. Срок: до горизонтального масштабирования.

## Что уже закрыто (для справки)

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

# Проверки

## Команды

| Команда | Что проверяет | Когда обязательна |
| --- | --- | --- |
| `make docs-check` | Локальные ссылки, реестр ADR, карта docs, заголовки, OpenAPI (operationId, path params, CSRF), пример политики, статьи справки (front matter, уникальный slug, совпадение с именем файла, разрешимость `help:` ссылок), whitespace — реализован на Go (`src/tools/docs-check`) | Любое изменение |
| `make check` | docs-check, typecheck и сборка web, `npm test` (vitest), `go test -race ./...`, `go vet`, `gofmt`, `git diff --check` | Любое изменение кода |
| `make integration` | `smoke.py`, `network.py`, `apt.py`, `ops.py`: реальный Squid в образе `kraken:dev`/`kraken:ops`, локальные fixtures | Изменения policy, runtime, deploy, auth |
| `npm --prefix src/web run test` | vitest: чистая логика каркаса (пределы панелей, навигация по ролям, конечный автомат применения) | Изменения логики в `src/web/src/shell` и `src/web/src/draft` |

docs-check — не полная валидация OpenAPI. Ни одна команда не заменяет security audit.
docs-check портирован с Python на Go (`src/tools/docs-check`); поведение и сообщения
совпадают, тесты пакета покрывают ошибку каждого вида на фикстурах.
CI: `.github/workflows/docs.yml` запускает docs-check, `runtime.yml` — verify-сборку и smoke.

## Go-тесты по пакетам

| Пакет | Что покрыто |
| --- | --- |
| `policy` | Валидация CIDR, доменов, портов, settings; детерминированный рендер; порядок защитных ACL; expert allowlist, файлы-параметры, deny_info, cache_peer; эквивалентность разбора строк валидатором и Squid; proxy auth ACL |
| `runtime` | Parse/reload/verify, fingerprint, journal, LKG, recovery, rollback, drift, lock; ENOSPC на каждой записи; SIGKILL отдельного процесса на durable-границах; номер строки parse без вывода Squid; manager reports |
| `httpapi` | Auth, CSRF, Origin, throttle, роли и last-admin; stale draft conflict; идемпотентность операций; preview без ревизий; service-update из LKG; статистика и события; findings операции; индекс и статья справки (401 анонимно, 404 неизвестный slug); `GET /configuration/active` (admin/manager/anonymous); `DELETE /configuration/draft` (успех, 409, менеджер с чужими изменениями → 403 без изменения, менеджер со своими → 200); `GET /audit?entity_id=` (только свои записи, пустой список для чужого клиента менеджера, фильтр длиннее 256 → 400) |
| `store` | Миграции: атомарность при отказе, отказ для несовместимой БД; FK сессий; сессии и reset; события, checkpoint, cursor, фильтры, retention; `ChangeDraft`/`DiscardDraft` с per-entity audit по `changedEntityIDs` |
| `admin` | CLI last-admin (блокировка/понижение), отзыв сессий одного пользователя и всех, audit на каждое изменение; backup: состав архива (VACUUM INTO без -wal/-shm, runtime/**, certs/**, rollups), консистентность копии при параллельной записи, отказ перезаписи, права 0600, симлинки не следовать за пределы dataDir |
| `cmd/control` | Выбор сброса привилегий: squid-start/proxy-auth не сбрасывают root, остальные команды сбрасывают |
| `events` | Чтение access log: частичные/битые строки, rotation, truncation, возврат файла |
| `proxyauth` | Basic encoding и лимиты, отсутствующая БД, reset/disable, generation guard |
| `credentials` | Argon2id PHC; отказ для verifier без версии и параметров |
| `help` | Разбор front matter при init (panic на ошибку формата), индекс непустой и отсортирован, статья по slug, уникальность slug, разрешимость всех `help:` ссылок |

## Web unit-тесты

`npm --prefix src/web run test` (vitest) покрывает чистую логику каркаса и
экранов в `src/web/src/shell`, `src/web/src/draft`, `src/web/src/clients`,
`src/web/src/policies`, `src/web/src/log` и `src/web/src/audit`, без
браузера и DOM:

| Модуль | Что покрыто |
| --- | --- |
| `shell/layout` | `PANE_LIMITS` и `clampPane`: диапазоны list/detail, clamp по границам, NaN → min, округление дробных пикселей |
| `shell/nav` | `navItems(role)`: фиксированный порядок main-разделов, group `bottom`, отсутствие Настроек у manager, русские подписи |
| `draft/applyFlow` | `applyReducer`: start → running/revision, step, error 409 → conflict, иная error → failed с кодом и шагом, operation validated/succeeded/rolled_back/rejected, игнор вне running, reset |
| `clients/networks` | `parseNetwork`: bare address → /32 или /128, host bits и длина вне диапазона → null, IPv6 canonical; `networksOverlap` и `networkWithin` для v4/v6, разных семейств и мусора |
| `policies/domains` | `parseDomainInput`: схема, путь, порт, завершающая точка, `*.x`/`.x`; IP-литералы, пробелы, `_` → null; `mergeDestinations`: дедуп по domain, OR `include_subdomains`, без мутации |
| `draft/marks` | `entityMarks`: new/changed/deleted по глубокому равенству, неизменённые пропущены, порядок ключей не важен |
| `draft/edits` | `addDomainToClientPolicy`, `changeClientPolicy`, `createClientFromIP` (включая fresh own-policy из неописанного IP), `clientsOfPolicy`, no-op на missing inputs, без мутации |
| `log/query` | `parseLogQuery`: все токены (`client:`, `host:`, `method:`, `ip:`/`source:`, `result:`/`результат:` с `allowed|разрешено|denied|отказ`), пустое значение игнорируется, остальное — `free`, повтор выигрывает последний; `isDenied` (DENIED/FAIL, status ≥ 400); `facetCounts` (клиент группируется по `client_id`, без `client_id` под `""`, неуникальные имена и null-name при наличии id); `minuteHistogram` (пограничные минуты, события вне окна игнорируются, пустой ввод); `buildEventsParams` (client→client_id по резолверу, denied→failures_only, host/free→destination, сериализация) |
| `audit/actions` | каждый `action`, записываемый Go (`login`, `login_failed`, `logout`, `csrf_failed`, `rate_limited`, `session_capacity_reached`, `password_reset`, `create_user`, `update_user`, `update_scope`, `create_proxy_account`, `update_proxy_account`, `reset_proxy_password`, `update_draft`, `discard_draft`, `create_revision`), имеет русское название; категории (apply/login/account/draft/other) и фильтры (все/применения/входы/учётки); неизвестный action — fallback на код |
| `settings/units` | `parsePortList`: дедуп с сохранением порядка, диапазон 1–65535, целые, пусто → null; `parseDuration`: `с`/`мин`/`ч` и bare секунды, неотрицательные целые, мусор → null; `formatDuration`: наибольшая единица, в которой значение целое (`0` → `0 с`); round-trip format → parse; `parseNameservers`: IPv4/IPv6 через запятую/пробел, каноническая форма, дедуп, CIDR и host:port → null, пусто → [] |
| `accounts/rules` | `canDemote`/`canDisable`: false только для единственного включённого admin; `buildManagerScopeBody`: нормализация CIDR через `parseNetwork`, дедуп, дроп мусора, пусто → пустые массивы |
| `settings/edits` | `effectiveSettings` (defaults при отсутствии), `patchSettings` (merge без мутации), `setBlockedDomains`, `setUpstream` (вкл/выкл через null), `setExpertConfig` (mode + текст) |

E2e (Playwright) на ключевые потоки — отдельный срез; `@playwright/test` и
`tests/*.spec.ts` удалены вместе со старыми экранами.

## Integration smoke

`src/tests/integration/smoke.py` создаёт отдельные контейнеры, сеть и volume,
пароли передаёт только через stdin. Проверяет:

- неизвестный source, all_web, allowlist, raw IP, защитные запреты loopback/metadata;
- HTTPS CONNECT с проверкой сертификата origin; TLS к прокси, отказ plaintext downgrade;
- proxy auth: 407/403, разные identities за NAT, reset/disable на живом соединении;
- визуальные настройки и expert ACL на реальном трафике; отказ запрещённых
  expert-директив при сохранении черновика; отказ Squid parse с номером строки;
- apply, idempotency, rollback, восстановление pending journal после restart;
- неизменность listeners через draft и старые ревизии;
- CLI password-reset с отзывом сессий; отсутствие plaintext паролей в volume;
- изоляция UID: от `proxy` недоступны SQLite, ключ панели и токен manager.

`network.py` (часть `make integration`) с отдельного all_web-клиента и своим
DNS-сервером проверяет отказ по ACL (403, `ERR_ACCESS_DENIED`) для IPv4/IPv6
loopback, link-local, metadata, IPv4-mapped, 6to4, Teredo, NAT64 и private, для имён,
резолвящихся в них, для CONNECT; DNS rebinding после истечения TTL; поведение
`allow_private_destinations`; origin получает только контрольные запросы.

`apt.py` (часть `make integration`) поднимает подписанный локальный репозиторий:
`apt-get update/install` через прокси по HTTP и через CONNECT, отказ без политики и
без домена в allowlist, ошибка подписи при подменённом `InRelease`. Блок с настоящим
архивом Ubuntu включается `KRAKEN_TEST_REAL_APT=1` и использует DNS 1.1.1.1/8.8.8.8;
DNS Docker Desktop/OrbStack отвечает адресами `198.18.0.0/15`, которые прокси
закрывает как private, поэтому там блок не проходит.

`ops.py` (часть `make integration`) исполняет процедуры runbook на образе `kraken:ops`
(тег параметризуется через `KRAKEN_TEST_IMAGE`; образ собирается
`docker build -f deploy/Dockerfile -t kraken:ops .`). Создаёт только уникально
именованные disposable контейнеры/сети/volumes во временных подсетях `11.253.x.0/24`
и удаляет их в `finally`, не трогая пользовательский Compose и чужие ресурсы.
Каждый шаг печатает `PASS: …`; шаг, который невозможно проверить в среде, помечается
`SKIP` с причиной, а не ложным PASS. Проверяет:

- копия и восстановление `/data`: остановить контейнер, скопировать весь named volume
  с сохранением прав/UID (`tar | tar`), поднять новый контейнер на новом volume из
  копии — тот же пароль открывает admin, та же активная ревизия/fingerprint, политика
  действует (разрешённый — 200, запрещённый — 403), история событий и аудит на месте;
- замена сертификата панели и прокси по процедуре runbook: новый dev-CA и leaf-сертификаты
  генерируются во временном каталоге, заменяются с сохранением прав (панель — UID 10013
  0600, прокси — группа 13 0640), клиент с новым CA проходит TLS, со старым — нет,
  частный ключ не попадает в логи контейнера;
- обновление образа: второй тег того же образа (`kraken:ops-next`, тривиальное отличие
  через label без изменения кода) заменяет контейнер на том же volume — dirty draft
  не применён, активная ревизия та же, существующие сессии и новый вход работают;
- заполненный диск: `/data` на tmpfs с лимитом размера, заполнение до отказа записи —
  draft/apply завершаются понятной ошибкой без частичного применения, Squid продолжает
  обслуживать по последней рабочей конфигурации, после освобождения места panel
  восстанавливается и apply проходит; целостность — `kraken check` и успешные чтения
  БД (в образе нет `sqlite3` CLI).

Сертификаты и ключи генерируются во временный каталог и никогда не коммитятся.

## Обязательные сценарии

Изменение затронутой области требует негативных тестов:

- **Политика:** неизвестный и выключенный source → deny; точный домен, поддомен и
  похожее имя; пустой allowlist; raw IP; private/loopback/link-local/metadata; IPv6,
  IPv4-mapped, 6to4, Teredo; CONNECT на 22/25 и чужой порт; manager недоступен
  клиенту; overlap CIDR отклоняется; правка одного клиента не расширяет доступ другого.
- **Expert:** директива вне allowlist, файл-параметр, peer во внутреннюю сеть,
  попытка сослаться на `sc_` имена отклоняются до parse.
- **Apply:** invalid config, parse ok + reload fail, reload ok + не тот fingerprint,
  crash до и после каждой durable записи, повтор idempotency key с другим payload,
  rollback failure, drift, restart с pending candidate. Проверяется фактический
  доступ, а не только статус операции.
- **Вход:** нет сессии/CSRF/Origin → mutation отклонена; reset и блокировка отзывают сессии.

Сетевые правила проверяются с отдельных source-адресов: запрос с localhost
доказывает только исключение localhost.

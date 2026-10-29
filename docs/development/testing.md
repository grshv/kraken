# Проверки

## Команды

| Команда | Что проверяет | Когда обязательна |
| --- | --- | --- |
| `make docs-check` | Локальные ссылки, реестр ADR, карта docs, заголовки, OpenAPI (operationId, path params, CSRF), пример политики, статьи справки (front matter, уникальный slug, совпадение с именем файла, разрешимость `help:` ссылок), whitespace | Любое изменение |
| `make check` | docs-check, typecheck и сборка web, `npm test` (vitest), `go test -race ./...`, `go vet`, `gofmt`, `git diff --check` | Любое изменение кода |
| `make integration` | `smoke.py`, `network.py`, `apt.py`: реальный Squid в образе `kraken:dev`, локальные fixtures | Изменения policy, runtime, deploy, auth |
| `npm --prefix src/web run test` | vitest: чистая логика каркаса (пределы панелей, навигация по ролям, конечный автомат применения) | Изменения логики в `src/web/src/shell` и `src/web/src/draft` |

docs-check — не полная валидация OpenAPI. Ни одна команда не заменяет security audit.
CI: `.github/workflows/docs.yml` запускает docs-check, `runtime.yml` — verify-сборку и smoke.

## Go-тесты по пакетам

| Пакет | Что покрыто |
| --- | --- |
| `policy` | Валидация CIDR, доменов, портов, settings; детерминированный рендер; порядок защитных ACL; expert allowlist, файлы-параметры, deny_info, cache_peer; эквивалентность разбора строк валидатором и Squid; proxy auth ACL |
| `runtime` | Parse/reload/verify, fingerprint, journal, LKG, recovery, rollback, drift, lock; ENOSPC на каждой записи; SIGKILL отдельного процесса на durable-границах; номер строки parse без вывода Squid; manager reports |
| `httpapi` | Auth, CSRF, Origin, throttle, роли и last-admin; stale draft conflict; идемпотентность операций; preview без ревизий; service-update из LKG; статистика и события; findings операции; индекс и статья справки (401 анонимно, 404 неизвестный slug); `GET /configuration/active` (admin/manager/anonymous); `DELETE /configuration/draft` (успех, 409, менеджер с чужими изменениями → 403 без изменения, менеджер со своими → 200); `GET /audit?entity_id=` (только свои записи, пустой список для чужого клиента менеджера, фильтр длиннее 256 → 400) |
| `store` | Миграции: атомарность при отказе, отказ для несовместимой БД; FK сессий; сессии и reset; события, checkpoint, cursor, фильтры, retention; `ChangeDraft`/`DiscardDraft` с per-entity audit по `changedEntityIDs` |
| `events` | Чтение access log: частичные/битые строки, rotation, truncation, возврат файла |
| `proxyauth` | Basic encoding и лимиты, отсутствующая БД, reset/disable, generation guard |
| `credentials` | Argon2id PHC; отказ для verifier без версии и параметров |
| `help` | Разбор front matter при init (panic на ошибку формата), индекс непустой и отсортирован, статья по slug, уникальность slug, разрешимость всех `help:` ссылок |

## Web unit-тесты

`npm --prefix src/web run test` (vitest) покрывает чистую логику каркаса и
экранов в `src/web/src/shell`, `src/web/src/draft`, `src/web/src/clients` и
`src/web/src/policies`, без браузера и DOM:

| Модуль | Что покрыто |
| --- | --- |
| `shell/layout` | `PANE_LIMITS` и `clampPane`: диапазоны list/detail, clamp по границам, NaN → min, округление дробных пикселей |
| `shell/nav` | `navItems(role)`: фиксированный порядок main-разделов, group `bottom`, отсутствие Настроек у manager, русские подписи |
| `draft/applyFlow` | `applyReducer`: start → running/revision, step, error 409 → conflict, иная error → failed с кодом и шагом, operation validated/succeeded/rolled_back/rejected, игнор вне running, reset |
| `clients/networks` | `parseNetwork`: bare address → /32 или /128, host bits и длина вне диапазона → null, IPv6 canonical; `networksOverlap` и `networkWithin` для v4/v6, разных семейств и мусора |
| `policies/domains` | `parseDomainInput`: схема, путь, порт, завершающая точка, `*.x`/`.x`; IP-литералы, пробелы, `_` → null; `mergeDestinations`: дедуп по domain, OR `include_subdomains`, без мутации |
| `draft/marks` | `entityMarks`: new/changed/deleted по глубокому равенству, неизменённые пропущены, порядок ключей не важен |
| `draft/edits` | `addDomainToClientPolicy`, `changeClientPolicy`, `createClientFromIP` (включая fresh own-policy из неописанного IP), `clientsOfPolicy`, no-op на missing inputs, без мутации |

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

`network.py` (`make integration-network`) с отдельного all_web-клиента и своим
DNS-сервером проверяет отказ по ACL (403, `ERR_ACCESS_DENIED`) для IPv4/IPv6
loopback, link-local, metadata, IPv4-mapped, 6to4, Teredo, NAT64 и private, для имён,
резолвящихся в них, для CONNECT; DNS rebinding после истечения TTL; поведение
`allow_private_destinations`; origin получает только контрольные запросы.

`apt.py` (`make integration-apt`) поднимает подписанный локальный репозиторий:
`apt-get update/install` через прокси по HTTP и через CONNECT, отказ без политики и
без домена в allowlist, ошибка подписи при подменённом `InRelease`. Блок с настоящим
архивом Ubuntu включается `KRAKEN_TEST_REAL_APT=1` и использует DNS 1.1.1.1/8.8.8.8;
DNS Docker Desktop/OrbStack отвечает адресами `198.18.0.0/15`, которые прокси
закрывает как private, поэтому там блок не проходит.

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

## Демонстрационный трафик

`python3 src/tools/demo_traffic.py` наполняет локальную Compose-установку
синтетическими HTTP/HTTPS-запросами через настоящий Squid в отдельной Docker-сети.
Временно добавляет demo-клиентов в confirmed snapshot штатным apply и затем
восстанавливает исходный snapshot; черновик и пароли не меняются. Нужны Go, Python,
OpenSSL и запущенный контейнер `kraken-kraken-1`. Expert mode не
поддерживается. Это не проверка APT.

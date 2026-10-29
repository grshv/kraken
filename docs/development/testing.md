# Проверки

## Команды

| Команда | Что проверяет | Когда обязательна |
| --- | --- | --- |
| `make docs-check` | Локальные ссылки, реестр ADR, карта docs, заголовки, OpenAPI (operationId, path params, CSRF), пример политики, whitespace | Любое изменение |
| `make check` | docs-check, typecheck и сборка web, `go test -race ./...`, `go vet`, `git diff --check` | Любое изменение кода |
| `make integration` | Реальный Squid в образе `squid-control:dev`, локальные fake origins | Изменения policy, runtime, deploy, auth |
| `npm --prefix src/web run test:e2e` | Playwright в отдельном контейнере | Изменения UI; нужен `make image` и Chromium |

docs-check — не полная валидация OpenAPI. Ни одна команда не заменяет security audit.
CI: `.github/workflows/docs.yml` запускает docs-check, `runtime.yml` — verify-сборку и smoke.

## Go-тесты по пакетам

| Пакет | Что покрыто |
| --- | --- |
| `policy` | Валидация CIDR, доменов, портов, settings; детерминированный рендер; порядок защитных ACL; expert allowlist, файлы-параметры, deny_info, cache_peer; эквивалентность разбора строк валидатором и Squid; proxy auth ACL |
| `runtime` | Parse/reload/verify, fingerprint, journal, LKG, recovery, rollback, drift, lock; ENOSPC на каждой записи; SIGKILL отдельного процесса на durable-границах; номер строки parse без вывода Squid; manager reports |
| `httpapi` | Auth, CSRF, Origin, throttle, роли и last-admin; stale draft conflict; идемпотентность операций; preview без ревизий; service-update из LKG; статистика и события; findings операции |
| `store` | Миграции и их атомарность при отказе; сессии и reset; события, checkpoint, cursor, фильтры, retention |
| `events` | Чтение access log: частичные/битые строки, rotation, truncation, возврат файла |
| `proxyauth` | Basic encoding и лимиты, отсутствующая БД, reset/disable, generation guard |
| `credentials` | Argon2id PHC и чтение legacy hashes |

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
- CLI password-reset с отзывом сессий; отсутствие plaintext паролей в volume.

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
OpenSSL и запущенный контейнер `squid-control-squid-control-1`. Expert mode не
поддерживается. Это не проверка APT.

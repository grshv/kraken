# Проверки и CI

## Что проверяется уже сейчас

`make docs-check`: существование локальных Markdown links, синтаксис JSON,
внутренние `$ref`, базовая структура API, policy example и текстовый whitespace.
`make check`: docs, npm typecheck/build, Go race tests, vet и `git diff --check`.
`make integration`: real-image smoke из src/tests/integration/.
Docs-check не является полноценной валидацией OpenAPI. Сетевой smoke проверяет
реальный Squid; ни одна из этих команд не заменяет security audit.
CI документации запускает тот же target, без деплоя и секретов.

Unit/HTTP/runtime fault tests и сетевой smoke реализованы. Остальные пункты
ниже — целевая матрица; её наличие не означает выполнения каждого сценария.

## Целевая пирамида

| Уровень | Что проверять | Среда |
| --- | --- | --- |
| Unit | CIDR intersections, domain normalization, deterministic compiler, enums | Go, без Docker |
| Property/fuzz | CRLF/config injection, Unicode, nested domains, parser bounds | Fuzz corpus, ограниченное время CI |
| Store | Concurrent draft writes, rollback migrations, session revocation | Temp SQLite local disk |
| Contract | OpenAPI request/response и внутренний runtime interface | Generated validator + fake Squid adapter |
| Integration | Настоящий parse/reload/ACL/TLS на pinned image | Изолированная Linux CI-среда с Docker |
| Recovery | Crash на каждой durable границе, restart и idempotency | Fault injection + filesystem |
| E2E | Login, client policy, preview, apply, conflict, diagnostics | Браузер + backend + fake origins |
| Client | APT/curl, TLS trust, direct-egress controls | Изолированные клиентские VM/namespaces |

## Обязательные policy fixtures

Неизвестный src → deny; выключенный client → deny; обе policy modes;
точный домен/поддомены/похожее имя; пустой allowlist; raw IP и PTR;
private/loopback/link-local/metadata; IPv6 и IPv4-mapped;
CONNECT к 25/22 и чужому port → deny; manager недоступен клиенту;
DNS rebinding не открывает private destination; CIDR overlaps отклоняются.
Изменение профиля одного клиента не расширяет доступ другой.

Нужны отдельные сетевые источники. Запрос только с localhost доказывает
исключение localhost, а не правила пользовательских устройств.

## Apply и failures

Invalid syntax/semantic guard; missing cert/helper; disk full; Squid control недоступен;
parse success + reload failure; reload exit 0 + runtime не обновился;
Go crash до/после сохранения intent и перед/после rename/LKG;
повтор idempotency key с тем же и другим payload; два apply;
rollback failure; drift; VM/container restart с pending candidate;
независимый restart Squid в Pebble, startup gate и гонка с worker lock;
restart только Go без разрыва существующего CONNECT.
Проверяется фактический доступ, а не только статус operation в DB.

## TLS matrix

- Панель: SAN/chain/expiry, cookies, invalid origin/CSRF.
- VM → proxy TLS: валидное доверие, неизвестный CA, wrong SAN, expiry.
- HTTPS CONNECT: исходный issuer сайта сохраняется, URL-path не «наблюдается».
- APT: update + download тестового пакета, signature/hash checks включены,
  Range/redirect/metadata freshness при включении cache.

## Безопасность процессов внутри контейнера

Unauthorized endpoints, CSRF, login throttling, session expiry/reset,
escaped XSS logs, limits на request/log records, path traversal и произвольные
process command/path attempts. В единственном контейнере отсутствуют Docker socket,
privileged/host namespaces. Проверить UID/GID, права на PID/reload/config и ключи;
локальный parse не меняет live cache/log/PID. Проверить, что Go не root
ради reload и private keys доступны только нужным процессам.

## Источники тестов

Primary integration suite использует локальные origin HTTP/HTTPS/DNS с test-only
сертификатами, чтобы интернет и сторонние mirrors не делали CI случайным.
Отдельный необязательный smoke подтверждает реальный внешний ресурс и
подписывается как network-dependent. Нельзя подменить deterministic suite им.

## Release gate

Pinned toolchains/lockfiles/images; build, lint/typecheck, unit/contract/integration,
критические e2e и recovery; secret scan, dependency/container scan;
SBOM и provenance согласно выбранному CI; backup/restore drill и release notes.
GitHub Actions pin по полному commit SHA, минимальные permissions; не выполнять
недоверенный PR-код в workflow с secrets или `pull_request_target` mutation.

Не требовать бессмысленного 100% coverage. Требовать проверку важных инвариантов
и реальных отказов. Добавлять pipeline jobs только когда есть исполняемые команды.

## Расширение панели и восстановления

Go race suite проверяет PHC и legacy hashes, атомарный reset/revoke, гонку входа
со сменой пароля, настройки и expert guard. Fault tests инъецируют ENOSPC при
записи candidate/journal/active/LKG и убивают отдельный test process через SIGKILL
на durable границах. Проверяется rollback после отмены forward context и блокировка
новых операций при ошибке сохранения terminal outcome. Реальный полный диск и
отключение питания этим не воспроизводятся.

`make integration` проверяет визуальные настройки/expert ACL на настоящем трафике,
перенос listener с сохранением private probe, отказ parse без изменения active
и CLI password-reset с отзывом старых сессий.

Браузер: `npm --prefix src/web run test:e2e` после `make image` и установки
совместимого Chromium (`npx playwright install chromium` из src/web). При необходимости
`SC_CHROMIUM_PATH` указывает исполняемый файл браузера. Fixture сам создаёт отдельные
контейнер/volume и случайный пароль в памяти; credentials files/env не используются.
Он проверяет клиента, настройки, экспертный шаблон, diff/apply/rollback и mobile
overflow. Self-signed доверие отключено только в этом disposable browser fixture;
сетевая suite отдельно проверяет TLS chain.

## Регрессии повторного ревью

Сценарии и результаты: [review reliability](../plans/active/review-reliability.md).
HTTP suite проверяет stateless anonymous CSRF, целостность/expiry/смену ключа,
вход после потока anonymous GET и отсутствие deadlock при полном DB pool.
Migration suite инъецирует ошибку перед version insert после переноса данных:
проверяет rollback схемы/данных и успешный повтор. Runtime tests покрывают каждую
запись first_run и сохранение pending journal при отказе reload/verify.
Real-image suite восстанавливает previous ACL из pending journal после restart
контейнера и проверяет фактические запросы. Browser suite проверяет GET/mutation
expiry, обновление CSRF holder и вход после неверного пароля без reload страницы.

## История диагностики

[Критерии](../plans/active/diagnostics-history.md). Reader tests проверяют bounded
batch/record, частичные строки, oversized/malformed/invalid UTF-8, rename rotation,
truncation/anchor rewrite и возвращение временно исчезнувшего файла без replay.
Store tests проверяют общую транзакцию событий/checkpoint с injected failure,
reopen, дедупликацию, cursor при новых строках, client/failure/audit filters,
некорректные/cross-filter cursors и retention по TTL/количеству.
HTTP tests подтверждают auth, privacy, неподменяемые старые client labels,
неизвестную историческую revision и stale/unavailable при отказе источника.
Real-image suite проверяет ingestion настоящих Squid logs, cursor/client filter,
rotation, отдельный restart control и сохранённый audit reset. Browser suite
добавляет клиентский фильтр, более ранние записи, audit, безопасный текст вместо
HTML и сохранение строк с предупреждением при API 503, desktop/mobile screenshots.

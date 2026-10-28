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

## Проверки по объёму поставки

Матрица выше — перечень рисков, не бесконечная задача проверить все сочетания.
Для текущего среза сохраняются make check, real-image и критические browser/recovery
сценарии. IPv6/mapped, DNS rebinding и private destination tests обязательны перед
соответствующим обещанием защиты; непроверенное не объявляется Supported.
Проверка APT требует самого APT, а не успешного Python HTTP запроса.

Перед распространяемым релизом: выбранная целевая архитектура, pinned image,
короткие release notes, dependency/container/secret scans с разбором результатов,
одно изолированное восстановление остановленной копии состояния. Для локальной
установки отдельная платформа SBOM/signing/provenance не требуется; такие артефакты
добавляются под требования распространения/CI. Полноценный schema validator можно
подключить готовым инструментом при необходимости, без генерации всей платформы.
Существующий runtime workflow уже строит verify/image и запускает smoke; его
наличие не означает подтверждённый успешный CI run. Browser/scans jobs в нём пока нет.
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

## Users/roles, proxy passwords и shadcn/ui

Migration/HTTP/store tests проверяют сохранение прежнего admin/hash/sessions,
viewer/API403, last-admin guard при конкурентных demotions, атомарный audit/revoke,
password work slots. Credential Hash/Matches сохраняют PHC/legacy совместимость.
Helper tests проверяют Basic encoding/limits, read-only missing DB, recheck после
Argon при reset/disable и generation state guard против старых кешированных creds.
Generated config parse отдельно проверен на Squid 6.13; это не заменяет HTTP smoke.

Real-image suite выполняет verified TLS proxy login, 407, plaintext403, NAT с
разными policies, exact REQUIRED/case-sensitive username, пароль с пробелом/+/%
и reset/disable на повторно используемом TLS connection. Отдельный viewer login
проверяет 403 и отзыв sessions. Пароли передаются fixture только stdin; проверяется
отсутствие plaintext/Basic values в persistent volume. Browser suite расширена
users/roles и proxy accounts, новая компоновка shadcn/ui проверяется на 1440/390 px.
Мобильные переходы открывают Sheet и закрывают его после выбора; повторный вход
проверяется и при скрытой боковой панели. Вкладки и диалог используют Radix.

## Согласованность UI-потоков (0.6.0)

`workflow.spec.ts` использует отдельный real-image fixture на сценарий. Проверяет
system preference до ручного override и сохранение темы после reload, расположение
sidebar trigger и mobile Sheet, реальные /statistics из логов, network abort/recovery,
единые Dialog/focus/password clearing/no-op submit, fixed listener UI, загрузку
expert template и полного config, read-only preview без POST revisions, dirty
navigation confirm и восстановление polling без повторной операции. Все 15
browser cases вместе с `panel.spec.ts` пройдены 28.10.2026.

Go tests покрывают bounded статистику/временные границы/DB errors, no-op/default
normalization, runtime-only delta и guards listeners в draft/revision operations.
Native smoke проверяет не только HTTP422, но и сохранение actual traffic/canary.
[Критерии, найденные расхождения и результат](../plans/active/ui-workflows.md).

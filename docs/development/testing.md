# Проверки и CI

## Что проверяется уже сейчас

`make docs-check`: существование локальных Markdown links, синтаксис JSON,
внутренние `$ref`, базовая структура API, policy example и текстовый whitespace.
`make check`: те же проверки и `git diff --check`.
Это не полноценная валидация OpenAPI, не runtime-тест Squid и не security audit.
CI документации запускает тот же target, без деплоя и секретов.

## Будущая пирамида

| Уровень | Что проверять | Среда |
| --- | --- | --- |
| Unit | CIDR intersections, domain normalization, deterministic compiler, enums | Go, без Docker |
| Property/fuzz | CRLF/config injection, Unicode, nested domains, parser bounds | Fuzz corpus, ограниченное время CI |
| Store | Concurrent draft writes, rollback migrations, session revocation | Temp SQLite local disk |
| Contract | OpenAPI request/response и внутренний runtime interface | Generated validator + fake Squid adapter |
| Integration | Настоящий parse/reload/ACL/TLS на pinned image | Изолированная Linux CI-среда с Docker |
| Recovery | Crash на каждой durable границе, restart и idempotency | Fault injection + filesystem |
| E2E | Login, VM policy, preview, apply, conflict, diagnostics | Браузер + backend + fake origins |
| Client | APT/curl, TLS trust, direct-egress controls | Изолированные клиентские VM/namespaces |

## Обязательные policy fixtures

Неизвестный src → deny; выключенный client → deny; обе policy modes;
точный домен/поддомены/похожее имя; пустой allowlist; raw IP и PTR;
private/loopback/link-local/metadata; IPv6 и IPv4-mapped;
CONNECT к 25/22 и чужому port → deny; manager недоступен клиенту;
DNS rebinding не открывает private destination; CIDR overlaps отклоняются.
Изменение профиля одной VM не расширяет доступ другой.

Нужны отдельные сетевые источники. Запрос только с localhost доказывает
исключение localhost, а не правила пользовательских VM.

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
- Inspect: тестовый issuer, клиентский trust, upstream validation,
  bypass/splice, SNI/CONNECT mismatch, pinning/mTLS fixture, cert DB restart.
- Несколько профилей одновременно; комбинация TLS proxy + inspection проверяется
  отдельно. Её поддержка не следует из поддержки функций по отдельности.
- APT: update + download тестового пакета, signature/hash checks включены,
  Range/redirect/metadata freshness при включении cache.

## Безопасность процессов внутри контейнера

Unauthorized endpoints, CSRF, login throttling, session expiry/reset,
escaped XSS logs, limits на request/log records, path traversal и произвольные
process command/path attempts. В единственном контейнере отсутствуют Docker socket,
privileged/host namespaces. Проверить UID/GID, права на PID/reload/config и ключи;
локальный parse не меняет live cache/log/PID/cert DB. Проверить, что Go не root
ради reload и signing key не выдаётся сверх подтверждённой необходимости.

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

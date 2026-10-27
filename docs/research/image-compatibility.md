# Проверка реального image, 27.10.2026

Проверено локально через Docker Engine на Linux/arm64. Это измерения текущего
рабочего среза; amd64 и production сеть ещё не проверены.

## Зафиксированная база

`ubuntu/squid:6.6-24.04_beta@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029`

arm64 manifest: `sha256:eecf35336788c1ab143566bca99ac58f213e9f17906a9a684346d7b323b6696c`.
Фактический `/usr/sbin/squid -v`: **6.13**, `--with-gnutls`, без OpenSSL.
Image entrypoint — `/usr/local/bin/entrypoint.sh`, shell, **Pebble отсутствует**.
Это расходится с overview Docker Hub; `7.2-26.04_beta` registry не находит.
Tag name не является надёжным источником фактической Squid version.

В производный image добавляется Pebble **v1.33.2** из pinned Go build.
Go builder **1.27.3-bookworm**, Node builder **24-alpine** закреплены digest в
[Dockerfile](../../deploy/Dockerfile). Версии Go dependencies и npm пакетов/шрифтов
зафиксированы в `src/go.mod`, go.sum и web/package-lock.json.

## UID, управление и процессы

Squid и squid-control запускаются от `proxy:13:13`, без root web и Docker socket.
Общий UID позволяет локальный parse/reconfigure и чтение persistent LKG/config.
Он не защищает конфиг/ключи от скомпрометированного Go-процесса; изоляция CA от
web внутри одного UID не заявляется.
Pebble PID 1 управляет сервисами отдельно; `run --hold` не запускает сервисы,
поэтому production command нашего image — `run --verbose`.

Перед запуском Squid подрежим того же executable восстанавливает journal/LKG,
затем exec `/usr/sbin/squid -N -f /data/runtime/active.conf`.
Apply перед изменением проверяет old runtime canary; в startup gap запрос не
может примениться к ещё не запущенному Squid. Общий filesystem lock защищает gate.

## Проверенная матрица

| Возможность | Результат | Проверка |
| --- | --- | --- |
| HTTP и deny unknown source | Supported | Реальный client fixture |
| Per-source all_web/allowlist | Supported | Разные src, allowed/denied fake domains |
| Loopback/metadata защита | Supported для проверенных cases | Отказы 127.0.0.1 и 169.254.169.254 |
| HTTPS CONNECT | Supported | TLS origin с проверкой сертификата |
| TLS до proxy | Supported | GnuTLS listener, trusted server cert |
| Защита transport от downgrade | Supported | TLS-профиль на HTTP listener получает 403 |
| TLS до proxy + TLS CONNECT в комбинации | Not tested | Не выводится из отдельных проверок |
| Parse / reload / canary | Supported | Локальный бинарник и revision response header |
| Independent Go restart | Supported | PID Squid сохраняется, открытый CONNECT проверяется smoke |
| Squid/container restart | Supported для штатных cases | Gate и persistent applied policy |
| Fault/crash boundaries | Частично | ENOSPC injection на записи; SIGKILL отдельного Go test process на durable границах; parse/reload/rollback/drift/lock |
| Визуальные настройки и expert section | Supported в проверенном объёме | Реальный parse/apply и трафик; неизвестная директива не меняет active |
| Изменение HTTP-порта | Supported | 3128 → 8080 → 3128, canary остаётся на 3130 |
| CLI password-reset | Supported | Старые сессия/пароль отклоняются, новый вход успешен |

Runtime canary — HTTP 403 обязательного loopback deny с fingerprint compiler,
snapshot и installed options. Это проверяет перечитывание конфигурации и защитный
запрет, не доказывает каждый ACL; поведение клиентских политик проверяется отдельно.
Начиная с compiler v3 используется отдельный listener 127.0.0.1:3130, который не публикуется
Docker. Для сохранённых конфигураций прежнего compiler probe совместим с 3128.

## Исполняемые доказательства

- `make check`: Go race tests (auth/CSRF/revoke, stale draft, policy guards,
  parse/reload/rollback/crash/drift, PHC/reset, expert guards), frontend typecheck/build и docs.
- `make image`: multi-stage image из [deploy](../../deploy/Dockerfile).
- `make integration`: [реальные сетевые fixtures](../../src/tests/integration/smoke.py),
  без default password, с проверкой цепочки сертификатов.
- [Browser fixture](../../src/web/tests/panel.spec.ts): вход, создание клиента,
  visual settings/expert template, validation/apply, история/rollback, desktop/mobile
  overflow и logout. Каждый запуск создаёт и удаляет отдельный контейнер/volume;
  случайный пароль существует только в памяти и передаётся CLI по stdin.
  Только браузерный fixture принимает свой self-signed сертификат без проверки
  доверия; trust/chain проверяется отдельным сетевым smoke.

## Открытые работы

Полная TLS/client matrix, DNS rebinding и IPv6 network suite, rotation/truncation
logs, полная disk-full/process-kill/reboot fault matrix, backup/restore и migrations,
исторические события с cursor/retention, UI certificates, security scan
и релизный manifest. Один контейнер — единая точка обновления/отказа.
Статус Supported относится к измеренной функции, не к общей production readiness.

ENOSPC здесь инъецируется в слой атомарной записи, а SIGKILL выполняется над
отдельным тестовым процессом. Это не испытание переполненного реального диска,
отключения питания или reboot всей VM. Expert guard не является полным sandbox
Squid; helper/storage provisioning остаётся отдельной работой. HTTPS сайтов сквозной.

Финальная проверка расширения: `make check`, `make image`, Docker build target
`verify` (Go race/vet на Linux/arm64), real-image smoke и Playwright пройдены.
Сохранённый локальный Compose volume поднят новой версией: `/healthz` и `/`
возвращают 200 при проверке доверия к panel certificate, оба Pebble services active,
`check` успешен. Bootstrap/reset существующего администратора не выполнялись.

## Сквозной HTTPS, уточнение 27.10.2026

Единственный TLS mode для сайтов — `tunnel`. API 0.3.0 исключает прежние
неиспользуемые поля TLS profile. Compiler v4 учитывает новую сериализацию;
сохранённый runtime bundle продолжает работать до нового apply. Проверены
`make check`, image build, реальные HTTP/CONNECT/TLS proxy и browser suite.
Существующий локальный volume успешно поднят без bootstrap/reset.
[Критерии и результат](../plans/active/tunnel-only.md).

## Исправления повторного ревью, 27.10.2026

Проверены anonymous CSRF без DB pool, атомарная миграция, first_run recovery и
подтверждённый online rollback. Устранён регресс адреса панели: :8443 внутри
контейнера, ограничение host exposure задаёт Compose. Real-image smoke прошёл,
включая pending-journal restart с возвратом прежних ACL и реальными запросами;
Playwright — 4 сценария, включая повторную авторизацию после GET/mutation expiry.
[Полные критерии, результаты и ограничения](../plans/active/review-reliability.md).
Тестовые контейнеры/volumes отдельные; существующая установка не обновлялась.

## История и аудит, 27.10.2026

API 0.4.0, migration 003: фоновый bounded reader, нормализованная SQLite-история,
checkpoint, client/failure filters и cursor; отдельный audit API/UI. Retention
событий 7 дней по сбору/50 000 строк, audit 90 дней/5000 строк. Проверены atomic
retry, source stale/unavailable, rotation/truncation, реальные Squid запросы,
отдельный restart control с сохранением истории, audit CLI reset и 5 browser
сценариев. make check и image build прошли; screenshots desktop/mobile проверены.
Compiler v4 и правила трафика не изменены; существующая установка не обновлялась.
[Критерии и оставшиеся ограничения](../plans/active/diagnostics-history.md).

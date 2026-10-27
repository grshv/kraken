# Проверка реального image, 27.10.2026

Проверено локально через Docker Engine на Linux/arm64. Это измерения первого
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
web внутри одного UID не заявляется. CA inspection пока не используется.
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
| SSL Bump | Unsupported | Нет OpenSSL/helper; API отклоняет inspect |
| TLS до proxy + TLS CONNECT в комбинации | Not tested | Не выводится из отдельных проверок |
| Parse / reload / canary | Supported | Локальный бинарник и revision response header |
| Independent Go restart | Supported | PID Squid сохраняется, открытый CONNECT проверяется smoke |
| Squid/container restart | Supported для штатных cases | Gate и persistent applied policy |
| Fault/crash boundaries | Частично | Unit injection: parse, reload, rollback, pending journal, drift/lock |

Runtime canary — HTTP 403 обязательного loopback deny с fingerprint compiler,
snapshot и installed options. Это проверяет перечитывание конфигурации и защитный
запрет, не доказывает каждый ACL; per-VM поведение проверяется отдельно.

## Исполняемые доказательства

- `make check`: Go race tests (auth/CSRF/revoke, stale draft, policy guards,
  parse/reload/rollback/crash/drift), frontend typecheck/build и docs.
- `make image`: multi-stage image из [deploy](../../deploy/Dockerfile).
- `make integration`: [реальные сетевые fixtures](../../src/tests/integration/smoke.py),
  без default password, без TLS verification bypass.
- [Browser fixture](../../src/web/tests/panel.spec.ts): вход, создание VM,
  validation/apply, desktop/mobile overflow и logout.

## Открытые работы

Полная TLS/client matrix, DNS rebinding и IPv6 network suite, rotation/truncation
logs, disk-full/process-kill/reboot fault matrix, backup/restore и migrations,
исторические события с cursor/retention, UI rollback/certificates, security scan
и релизный manifest. Один контейнер — единая точка обновления/отказа.
Статус Supported относится к измеренной функции, не к общей production readiness.

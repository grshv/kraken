# Базовый образ и матрица возможностей

Измерения на Docker Engine, Linux/arm64, 27–28.10.2026. amd64 и production-сеть
не проверялись.

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

Squid работает от `proxy:13:13`, kraken — от `kraken:10013` с доп. группой
`proxy`, без root web и Docker socket. На Squid 6.13 `kill -HUP` из другого UID
даёт EPERM, а `cache_object://127.0.0.1/reconfigure@<token>` на loopback-listener
перечитывает конфиг. Pebble 1.33.2 (CGO выключен) при `user:`/`group:` сбрасывает
дополнительные группы, поэтому сервис `control` понижает права через
`setpriv --reuid=kraken --regid=kraken --init-groups`.
Pebble PID 1 управляет сервисами отдельно; `run --hold` не запускает сервисы,
поэтому production command нашего image — `run --verbose`.

Перед запуском Squid подрежим того же executable восстанавливает journal/LKG,
затем exec `/usr/sbin/squid -N -f /dev/shm/kraken/squid.conf`.
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
| TLS до proxy + TLS CONNECT в комбинации | Supported в проверенном сценарии | Verified TLS к proxy и отдельная проверка сертификата origin внутри CONNECT, включая proxy auth |
| Parse / reload / canary | Supported | Локальный бинарник и revision response header |
| Independent Go restart | Supported | PID Squid сохраняется, открытый CONNECT проверяется smoke |
| Squid/container restart | Supported для штатных cases | Gate и persistent applied policy |
| Fault/crash boundaries | Частично | ENOSPC injection на записи; SIGKILL отдельного Go test process на durable границах; parse/reload/rollback/drift/lock |
| Визуальные настройки и expert section | Supported в проверенном объёме | Реальный parse/apply и трафик; неизвестная директива не меняет active |
| Listener ports контейнера | Installation setting | Draft и старые revisions не могут менять listeners; реальный 3128 и canary3130 сохраняются после отказа API |
| CLI password-reset | Supported | Старые сессия/пароль отклоняются, новый вход успешен |

Runtime canary — HTTP 403 обязательного loopback deny с fingerprint compiler,
snapshot и installed options. Это проверяет перечитывание конфигурации и защитный
запрет, не доказывает каждый ACL; поведение клиентских политик проверяется отдельно.
Probe использует отдельный listener 127.0.0.1:3130, который Docker не публикует.

Проверки, которые подтверждают эти факты, перечислены в [testing](../development/testing.md).

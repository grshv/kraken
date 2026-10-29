# Базовый образ и матрица возможностей

Измерения на Docker Engine, Linux/arm64, 27–30.10.2026. amd64 и production-сеть
не проверялись.

## База и сборка

Runtime — `alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`
([ADR 0019](../adr/0019-alpine-upstream-squid.md)). Squid **7.7** собирается в
отдельной стадии из tarball релиза `squid-cache/squid` на GitHub, sha256 закреплён
в [Dockerfile](../../deploy/Dockerfile). Исходники не патчатся; `configure` получает
только пути установки, `--with-openssl` (OpenSSL 3.5), `--with-default-user=proxy` и
`--disable-strict-error-checking`: без него GCC 15 останавливает сборку на
предупреждении в `unlinkd.cc`. `make install-strip` убирает только отладочные символы.
Компилятора и заголовков в runtime нет. Итоговый образ — около 90 MB.

Pebble **v1.33.2** собирается из pinned Go build. Go builder **1.27.3-trixie**,
Node builder **26-alpine** (Active LTS) закреплены digest. Версии Go dependencies и
npm пакетов зафиксированы в `src/go.mod`, go.sum и web/package-lock.json.

Образы `ubuntu/squid` не подошли: тег `6.6-24.04` содержал Squid 6.13 (ветка закрыта
upstream), тег `7.2-26.04` — Squid 7.7 только в канале edge и chiseled-сборке без
утилит запуска. Тег Canonical не является надёжным источником версии Squid.

Trivy видит пакеты apk и Go-модули, но не Squid, собранный из исходников:
уязвимости Squid отслеживаются по advisories upstream и версии в Dockerfile.

## UID, управление и процессы

Squid работает от `proxy` 13:13, kraken — от `kraken` 10013 с доп. группой `proxy`,
без root web и Docker socket (в Alpine GID 13 был у `news`, сборка его удаляет).
Контейнер запускается с `cap_drop: ALL` и только `SETUID`, `SETGID`, `KILL`, с
`no-new-privileges`: root остаётся только у Pebble, у Squid и панели CapEff = 0.
Root без `DAC_OVERRIDE` не читает каталоги `kraken` и `proxy`, поэтому CLI
сбрасывает root сам, а ручная диагностика идёт через `docker exec --user kraken`.
`kill -HUP` из другого UID даёт EPERM, поэтому reload и rotate — запрос
`GET /squid-internal-mgr/<action>` с Basic-паролем менеджера на loopback 3131;
`cache_object://` в Squid 7 удалён. Pebble 1.33.2 (CGO выключен) при `user:`/`group:`
сбрасывает дополнительные группы, поэтому сервис `control` понижает права через
`/bin/setpriv --reuid=kraken --regid=kraken --init-groups`.
Pebble PID 1 управляет сервисами отдельно; `run --hold` не запускает сервисы,
поэтому production command нашего image — `run --verbose`.

Squid 7 хранит IPv4-адреса как IPv4-mapped IPv6 и сливает перекрывающиеся диапазоны
в ACL `dst`, поэтому `::ffff:0:0/96` в запретах совпал бы со всем IPv4. Этого
диапазона в защитном списке нет; mapped-формы loopback и metadata закрывают IPv4-записи
(проверено мутацией `TestNetwork`).

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
| TLS до proxy | Supported | OpenSSL listener, trusted server cert |
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

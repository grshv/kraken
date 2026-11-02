# Целевое развёртывание

Практические команды запуска — в [local development](local-development.md).
Здесь требования к установке на выбранном узле, не отдельный deployment product.
Остаток проверок перед установкой — в [status](../status.md).
Один собственный image на Alpine со Squid из upstream-релиза
([ADR 0019](../adr/0019-alpine-upstream-squid.md)); один Compose service. Внутри Pebble запускает Squid и Go-приложение
со встроенным UI. Host runner и отдельный reverse proxy не устанавливаются.

## До установки

- Linux VM, Docker Engine и Compose plugin; systemd приложения не требуется.
- DNS и адреса management/client interfaces, сохранение исходных IP VM.
- Проверенный base tag+digest+architecture и digest собственного итогового image.
- Server TLS certificates панели/proxy и доверие к их issuer у клиентов.
- Локальное постоянное хранилище для тома `/var/lib/kraken` и место для внешней копии.
- Внутренние репозитории и закрытые адреса, которые клиентам нужно разрешить: защита
  RFC 1918 их закрывает, нужен `allow_private_destinations` или точные исключения.
- Firewall matrix, включая IPv6, metadata, DNS/NTP и возможный direct-egress запрет.

## Сборка и установка

1. Образ: готовый `ghcr.io/grshv/kraken` (amd64 и arm64; `main` — последний
   проверенный коммит main, `X.Y.Z` и `latest` — релизы по тегу `vX.Y.Z`, `sha-<коммит>`)
   или локальная сборка `make image`. Multi-stage build: frontend → embed в Go;
   Squid — готовый образ `ghcr.io/grshv/kraken-squid` по digest (собран из
   подписанного upstream-релиза, ADR 0020), принимается только с provenance
   attestation от `squid.yml`; бинарники и Pebble plan копируются в
   pinned Alpine.
   Node и toolchain в runtime отсутствуют. Compose берёт образ из `KRAKEN_IMAGE`,
   по умолчанию `ghcr.io/grshv/kraken:main`; для установки достаточно одного файла
   `deploy/compose.yaml` и `docker compose up -d`, локальная сборка запускается с
   `KRAKEN_IMAGE=kraken:dev`. Пакеты GHCR публичные,
   вход не нужен. Образ публикуется только после Go-, web- и integration-тестов
   своего коммита и несёт подписанные Sigstore attestations GitHub: provenance
   на multi-arch digest и SBOM (CycloneDX) на образ каждой архитектуры.
   Происхождение проверяется до установки:
   `gh attestation verify oci://ghcr.io/grshv/kraken:main --repo grshv/kraken
   --signer-workflow grshv/kraken/.github/workflows/image.yml`; для фиксации
   версии в Compose брать тег `sha-<коммит>` или digest.
2. Постоянное хранилище для `/var/lib/kraken`: named volume (по умолчанию) или
   каталог хоста `KRAKEN_DATA=./data`. Пустой каталог с любым владельцем entrypoint
   готовит сам (раздел «Файлы и права»). Внешние TLS-файлы подключить вне image/Git.
3. Задать адреса публикации (раздел «Сеть»). `KRAKEN_PUBLIC_ORIGIN` — адрес, по которому открывают панель.
4. Go сам завершает TLS панели; mandatory edge container нет. Без своих
   сертификатов `serve` выпускает CA установки и сертификаты панели и прокси.
5. При первом запуске recovery gate создаёт deny-all/LKG, затем Pebble запускает
   Squid. Gate обязателен и при последующих автоматических restart Squid.
6. Создать администратора командой `kraken` внутри контейнера,
   через stdin/TTY без пароля в argv: `docker exec -it <контейнер> kraken bootstrap`.
   Выдать клиентам и браузерам `kraken cert ca` (или свой CA).
7. Проверить HTTPS/login, deny-all, source IP, первую политику и реальный запрос VM.
8. Только затем выдать клиентские настройки и включить нужные сетевые запреты.

## Сеть

Management (8443) только для VPN/management network; 3128/3129 только для client
networks, ненужный HTTP listener закрыть firewall.

Сеть — Docker bridge; host network не используется ([ADR 0021](../adr/0021-install-volume-and-certificates.md)).
Один адрес хоста задают `KRAKEN_MANAGEMENT_BIND` (панель 8443) и `KRAKEN_PROXY_BIND`
(3128 и 3129), `0.0.0.0` — все адреса. Несколько адресов в разных подсетях —
дополнительные записи `ports` в `compose.override.yaml` рядом с `compose.yaml`;
Compose читает его автоматически и добавляет записи к основным:

```yaml
services:
  kraken:
    ports:
      - "192.168.10.5:8443:8443"
      - "10.30.0.5:3128:3128"
      - "10.30.0.5:3129:3129"
    environment:
      KRAKEN_CERT_NAMES: 192.168.10.5,10.30.0.5
```

Внутри контейнера панель и Squid слушают все адреса своего интерфейса; какие адреса
хоста открыты, решает только публикация портов. Адреса из override, по которым
клиенты обращаются к панели или TLS-прокси, нужно добавить в `KRAKEN_CERT_NAMES` и
выполнить `kraken cert renew`. Порты прокси из Settings в UI должны совпадать с
публикацией. Docker NAT сохраняет source IP внешних клиентов IPv4; запросы с самого
хоста через docker-proxy приходят с адреса шлюза. Между частями приложения нет
опубликованного IPC endpoint. Docker socket и Docker TCP API не предоставляются.

Docker published ports проверяются с другой машины: ufw INPUT сам по себе
недостаточен. Учитывать iptables/nftables backend и IPv6. Если нужен запрет direct
egress VM, реализовать на контролируемой сетевой границе, включая UDP/443.

## Файлы и права

Пользователи в image: `kraken` 10013:10013 (Go, доп. группа `proxy`) и
`proxy` 13:13 (Squid и helpers). Права по [ADR 0015](../adr/0015-separate-squid-uid.md):

Постоянное состояние — один том `/var/lib/kraken`; всё производное Go пересоздаёт из
управляющей БД в tmpfs `/dev/shm/kraken` при старте и при каждом применении.

| Путь | Владелец | Режим |
| --- | --- | --- |
| `/var/lib/kraken` (том) | kraken:proxy | 0750 |
| `/var/lib/kraken/data` (`control.sqlite3`, `telemetry/`) | kraken:kraken | 0700 |
| `/var/lib/kraken/certs` | kraken:proxy | 2750; `panel.key`, `ca.key` 0600 kraken, `proxy.*` 0640, `panel.crt` и `ca.crt` 0644 |
| `/var/lib/kraken/log` (access.log и ротации, cache.log) | proxy:proxy | 2750 |
| `/var/lib/kraken/backups` (создаёт `kraken backup`) | kraken | 0700, архивы 0600 |
| `/dev/shm/kraken` (tmpfs: `squid.conf`, `proxy-accounts.json`, `manager-auth.conf`, `apply.lock`) | kraken:proxy | 2750, файлы для Squid 0640; Squid только читает |
| `/dev/shm/kraken-squid` (tmpfs: PID Squid) | proxy | 0700, создаёт `squid-start` |

`serve` отказывается стартовать, если `KRAKEN_RUN_DIR` не на tmpfs или каталог
принадлежит другому пользователю. Пароль к менеджеру Squid (`manager-auth.conf`)
новый на каждый старт контейнера, не хранится на томе и в бэкапе; при перезапуске
только панели переиспользуется из tmpfs. Корни переопределяются `KRAKEN_STATE_DIR` и
`KRAKEN_RUN_DIR`.

Подготовка тома: PID 1 — `kraken entrypoint` от root, затем `exec` Pebble. Корень
`10013:13 0750` означает готовый том: ничего не меняется (named volume копирует
раскладку из образа). Иначе, например для свежего `./data`, entrypoint с `CAP_CHOWN`
создаёт подкаталоги и раздаёт владельцев и режимы по таблице, включая уже лежащие
файлы копии. Каталог с другими именами верхнего уровня (кроме `lost+found`), symlink,
hard link или специальные файлы отклоняются без изменений; контейнер завершается с
сообщением и командой `chown` для хоста. Root без `DAC_OVERRIDE` не читает чужие
каталоги, поэтому сначала становится их владельцем, а последним шагом отдаёт их.

Свой сертификат панели или прокси ставит `kraken cert install panel|proxy` из stdin:
команда проверяет пару и выставляет владельцев и режимы по таблице. Выпуск и
доверие к сертификатам установки — в
[authentication-and-certificates](../security/authentication-and-certificates.md#серверные-сертификаты). Нет обещания, что внутренний модуль скроет секрет от
скомпрометированного Go-процесса.

Конфиг Squid генерируется в tmpfs `/dev/shm/kraken` и заменяется rename в том же
каталоге; монтировать его не нужно. Не перекрывать `/etc/squid` целиком: там
defaults upstream-сборки.
Compose снимает все capabilities, кроме `CHOWN` (подготовка тома entrypoint),
`SETUID`, `SETGID` и `KILL` (их требует Pebble от root для запуска сервисов под
`proxy` и `kraken`), и включает `no-new-privileges`;
у Squid и панели эффективных capabilities нет. Rootless Docker или userns-remap на
хосте совместимы с образом и настраиваются при установке. Read-only rootfs не
включён. Cache init не выполняется при каждом apply.

Телеметрия (события прокси и агрегаты) по умолчанию лежит в `/var/lib/kraken/data/telemetry`
(тот же volume и владелец 10013:10013), отдельная от управляющей `control.sqlite3`.
Перенос каталога — `KRAKEN_TELEMETRY_DIR`; retention сырых событий —
`KRAKEN_EVENTS_RETENTION_DAYS` (90), бюджет диска — `KRAKEN_EVENTS_BUDGET_MB`
(5120). Повреждение или потеря телеметрии не ломает панель; backup — в
[runbooks](runbooks.md).

## Restart, health и upgrade

Панель и Squid имеют независимый restart в Pebble. Restart Go не останавливает
Squid; новый Go процесс выполняет reconciliation до mutations. Любой запуск Squid
проходит recovery gate, используя общий lock с apply. Container restart/VM reboot
проходит те же проверки, не доверяет pending candidate.

Health показывает состояния раздельно. Watchdog не пересоздаёт весь контейнер
только из-за отказа web, если Squid работает. Перезапуск сервисов и fail closed
при испорченном journal покрыты Go-тестами runtime и integration.

Обновление приложения или Squid меняет общий image и может разорвать CONNECT:
нужно окно. Для первой установки достаточно записать source commit, image ID/digest,
architecture, schema compatibility и известные ограничения в коротких release notes.
Отдельный manifest service и SBOM/provenance platform не требуются: SBOM и
provenance публикуются attestations GitHub рядом с образом в GHCR.
Откат image согласуется со схемой DB и secret refs. HA не предусмотрено.

## Привязка панели в контейнере

По умолчанию `KRAKEN_PANEL_LISTEN=:8443` внутри контейнера: Docker port forwarding
должен достигать container interface. Ограничение внешнего доступа задаётся host
binding в Compose (по умолчанию 127.0.0.1:8443), management firewall/VPN и
HTTPS-аутентификацией. `127.0.0.1:8443` внутри контейнера отключает опубликованный
порт и не подходит этому deployment.

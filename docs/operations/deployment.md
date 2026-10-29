# Целевое развёртывание

Практические команды запуска — в [local development](local-development.md).
Здесь требования к установке на выбранном узле, не отдельный deployment product.
Остаток проверок перед установкой — в [status](../status.md).
Один собственный image на базе
`ubuntu/squid`; один Compose service. Внутри Pebble запускает Squid и Go-приложение
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

1. Multi-stage build: frontend static assets → embed в Go → copy executable и
   проверенного Pebble plan в pinned Canonical base. Node/toolchain отсутствуют
   в runtime. Изменение upstream startup path описывается и проверяется.
2. Создать один постоянный том для `/var/lib/kraken` (раскладка — «Файлы и права»).
   Внешние TLS-файлы подключить вне image/Git.
3. Права на volume и внешние ключи — по разделу «Файлы и права».
4. Настроить management IP:443 → container:8443 для HTTPS Go и client listeners.
   Go сам завершает TLS панели; mandatory edge container нет.
5. При первом запуске recovery gate создаёт deny-all/LKG, затем Pebble запускает
   Squid. Gate обязателен и при последующих автоматических restart Squid.
6. Создать администратора командой `kraken` внутри контейнера,
   через stdin/TTY без пароля в argv: `docker exec -it <контейнер> kraken bootstrap`.
7. Проверить HTTPS/login, deny-all, source IP, первую политику и реальный запрос VM.
8. Только затем выдать клиентские настройки и включить нужные сетевые запреты.

## Сеть

Management 443 только для VPN/management network; 3128/3129 только для client
networks, ненужный HTTP listener отключить. Между частями приложения нет
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
| `/var/lib/kraken/certs` | kraken:proxy | 2750; `panel.*` 0600 kraken, `proxy.*` 0640 |
| `/var/lib/kraken/log` (access.log и ротации, cache.log) | proxy:proxy | 2750 |
| `/var/lib/kraken/backups` (создаёт `kraken backup`) | kraken | 0700, архивы 0600 |
| `/dev/shm/kraken` (tmpfs: `squid.conf`, `proxy-accounts.json`, `manager-auth.conf`, `apply.lock`) | kraken:proxy | 2750, файлы для Squid 0640; Squid только читает |
| `/dev/shm/kraken-squid` (tmpfs: PID Squid) | proxy | 0700, создаёт `squid-start` |

`serve` отказывается стартовать, если `KRAKEN_RUN_DIR` не на tmpfs или каталог
принадлежит другому пользователю. Пароль к менеджеру Squid (`manager-auth.conf`)
новый на каждый старт контейнера, не хранится на томе и в бэкапе; при перезапуске
только панели переиспользуется из tmpfs. Корни переопределяются `KRAKEN_STATE_DIR` и
`KRAKEN_RUN_DIR`.

Внешние TLS-файлы прокси (`compose.tls.yaml`) должны читаться UID или группой 13,
ключ панели — UID 10013. Нет обещания, что внутренний модуль скроет секрет от
скомпрометированного Go-процесса.

Config монтируется каталогом, atomic rename должен быть виден всем процессам.
Не перекрывать `/etc/squid` целиком без проверки defaults upstream image.
Read-only rootfs/capabilities/non-root проверяются на реальном image, не включаются
наугад ценой неработающего Pebble/helper. Cache init не выполняется при каждом apply.

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
Отдельный manifest service, SBOM/provenance platform не требуются; артефакты SBOM
и подписи добавляются под требования распространения, не выдаются за уже созданные.
Откат image согласуется со схемой DB и secret refs. HA не предусмотрено.

## Привязка панели в контейнере

По умолчанию KRAKEN_PANEL_LISTEN=:8443 внутри контейнера: Docker port forwarding
должен достигать container interface. Ограничение внешнего доступа задаётся
host binding в Compose (по умолчанию 127.0.0.1:8443), management firewall/VPN и
HTTPS-аутентификацией. KRAKEN_PANEL_LISTEN=127.0.0.1:8443 внутри контейнера отключает
доступ через обычный опубликованный Docker-порт и не подходит этому deployment.

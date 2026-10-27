# Целевое развёртывание

**План, не готовая инструкция запуска.** Один собственный image на базе
`ubuntu/squid`; один Compose service. Внутри Pebble запускает Squid и Go-приложение
со встроенным UI. Host runner и отдельный reverse proxy не устанавливаются.

## До установки

- Linux VM, Docker Engine и Compose plugin; systemd приложения не требуется.
- DNS и адреса management/client interfaces, сохранение исходных IP VM.
- Проверенный base tag+digest+architecture и digest собственного итогового image.
- Server TLS certificates панели/proxy и доверие к их issuer у клиентов.
- Local persistent storage, UID/GID, права на key/config/journal и backup location.
- Firewall matrix, включая IPv6, metadata, DNS/NTP и возможный direct-egress запрет.

## Сборка и установка

1. Multi-stage build: frontend static assets → embed в Go → copy executable и
   проверенного Pebble plan в pinned Canonical base. Node/toolchain отсутствуют
   в runtime. Изменение upstream startup path описывается и проверяется.
2. Создать persistent volumes для SQLite, journal/LKG/revisions, managed config,
   logs и необязательного cache. Секреты подключить вне image/Git.
3. Настроить UID/GID: минимум прав для Go, Squid/helper и startup gate.
   Подтвердить reload без запуска web root; доступ к private keys ограничен нужными процессами.
4. Настроить management IP:443 → container:8443 для HTTPS Go и client listeners.
   Go сам завершает TLS панели; mandatory edge container нет.
5. При первом запуске recovery gate создаёт deny-all/LKG, затем Pebble запускает
   Squid. Gate обязателен и при последующих автоматических restart Squid.
6. Создать администратора локальным подрежимом Go-бинарника внутри контейнера,
   через stdin/TTY без пароля в argv. Точная команда появится с кодом.
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

Go: SQLite, runtime journal и managed config RW, Squid logs read; собственный
panel TLS key read. Squid: managed config read, logs/cache write и
соответствующие server TLS keys read. Секреты монтируются read-only, но
какие UID имеют доступ, определяется Unix permissions, а не отдельным контейнером.
Нет обещания, что внутренний модуль скроет секрет от скомпрометированного Go-процесса.

Config монтируется каталогом, atomic rename должен быть виден всем процессам.
Не перекрывать `/etc/squid` целиком без проверки defaults upstream image.
Read-only rootfs/capabilities/non-root проверяются на реальном image, не включаются
наугад ценой неработающего Pebble/helper. Cache init не выполняется при каждом apply.

## Restart, health и upgrade

Панель и Squid имеют независимый restart в Pebble. Restart Go не останавливает
Squid; новый Go процесс выполняет reconciliation до mutations. Любой запуск Squid
проходит recovery gate, используя общий lock с apply. Container restart/VM reboot
проходит те же проверки, не доверяет pending candidate.

Health показывает состояния раздельно. Watchdog не пересоздаёт весь контейнер
только из-за отказа web, если Squid работает. Конкретный Pebble plan и fallback
при испорченном journal — обязательные тесты spike.

Обновление приложения или Squid меняет общий image и может разорвать CONNECT:
нужно окно. Manifest содержит source commit, base/final digest, architecture,
compiler/schema versions, toolchains, checksums, SBOM и capability evidence.
Откат image согласуется со схемой DB и secret refs. HA не предусмотрено.

## Привязка панели в контейнере

По умолчанию SC_PANEL_LISTEN=:8443 внутри контейнера: Docker port forwarding
должен достигать container interface. Ограничение внешнего доступа задаётся
host binding в Compose (по умолчанию 127.0.0.1:8443), management firewall/VPN и
HTTPS-аутентификацией. SC_PANEL_LISTEN=127.0.0.1:8443 внутри контейнера отключает
доступ через обычный опубликованный Docker-порт и не подходит этому deployment.

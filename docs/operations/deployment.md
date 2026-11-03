# Установка

Один образ `ghcr.io/grshv/kraken` (amd64 и arm64) и один сервис Compose. Внутри
Pebble запускает Squid и панель. Отдельный reverse proxy, runner на хосте и
systemd не нужны. Быстрый старт — в [README](../../README.md#быстрый-старт).

## Перед установкой

- Linux-хост с Docker Engine и Compose plugin.
- Адреса хоста для панели (сеть управления) и прокси (сети клиентов); клиенты должны
  приходить на прокси со своими исходными IP.
- Постоянное хранилище для `/var/lib/kraken` и место для внешней копии.
- Внутренние ресурсы, которые нужно открыть клиентам: частные адреса закрыты по
  умолчанию, нужен `allow_private_destinations`.
- Правила firewall для панели, прокси и, если клиенты обязаны ходить через прокси,
  запрет прямого egress, включая IPv6 и UDP/443.

## Образ

| Тег | Что это |
| --- | --- |
| `X.Y.Z` | Релиз по git-тегу `vX.Y.Z` |
| `latest` | Последний релиз; по умолчанию в `deploy/compose.yaml` |
| `main` | Последний проверенный коммит `main` |
| `sha-<коммит>` | Конкретный коммит |

Образ публикуется только после Go-, web- и integration-тестов своего коммита и несёт
подписанные attestations GitHub: provenance на multi-arch digest и SBOM (CycloneDX)
на образ каждой архитектуры. Пакеты GHCR публичные. Проверка происхождения:

```sh
gh attestation verify oci://ghcr.io/grshv/kraken:latest --repo grshv/kraken \
  --signer-workflow grshv/kraken/.github/workflows/image.yml
```

Версия внутри образа: `docker exec kraken kraken version`.

## Настройка

Compose читает `.env` рядом с `compose.yaml` ([пример](../../.env.example)):

| Переменная | По умолчанию | Что задаёт |
| --- | --- | --- |
| `KRAKEN_IMAGE` | `ghcr.io/grshv/kraken:latest` | Образ; версия фиксируется тегом `X.Y.Z` |
| `KRAKEN_PUBLIC_ORIGIN` | `https://localhost:8443` | Адрес, по которому открывают панель; проверяется в Origin, host идёт в сертификат |
| `KRAKEN_MANAGEMENT_BIND` | `127.0.0.1` | IP хоста для панели 8443 |
| `KRAKEN_PROXY_BIND` | `127.0.0.1` | IP хоста для прокси 3128 и 3129 |
| `KRAKEN_CERT_NAMES` | — | Дополнительные имена сертификата (DNS или IP) через запятую |
| `KRAKEN_DATA` | том `control-data` | Каталог хоста вместо тома, например `./data` |

Переменные внутри контейнера:

| Переменная | По умолчанию | Что задаёт |
| --- | --- | --- |
| `KRAKEN_INSTANCE_NAME` | hostname | Имя экземпляра в панели |
| `KRAKEN_EVENTS_RETENTION_DAYS` | 90 | Срок событий журнала |
| `KRAKEN_EVENTS_BUDGET_MB` | 5120 | Бюджет диска для событий |
| `KRAKEN_PARSE_TIMEOUT`, `KRAKEN_RELOAD_TIMEOUT`, `KRAKEN_VERIFY_BUDGET` | 15s, 10s, 30s | Таймауты применения |
| `KRAKEN_PANEL_LISTEN` | `:8443` | Адрес панели внутри контейнера; `127.0.0.1` сделает опубликованный порт недоступным |

## Порядок установки

1. Положить `compose.yaml` и `.env`, выполнить `docker compose up -d`.
2. Entrypoint готовит том, `serve` выпускает CA и сертификаты, восстанавливает
   конфигурацию (на первом запуске — deny-all), затем стартует Squid.
3. Создать администратора: `docker exec -it kraken kraken bootstrap --username admin`.
4. Импортировать CA установки (`kraken cert ca`) в браузеры и клиентов TLS-прокси
   или поставить свои сертификаты (`kraken cert install`).
5. Проверить вход, отказ неизвестному источнику, первого клиента и реальный запрос
   с него.
6. Только затем раздать клиентам настройки прокси и включить сетевые запреты.

## Сеть

Сеть — Docker bridge, host network не используется. Один адрес хоста задают
`KRAKEN_MANAGEMENT_BIND` и `KRAKEN_PROXY_BIND`; `0.0.0.0` — все адреса. Несколько
адресов — дополнительные `ports` в `compose.override.yaml` рядом с `compose.yaml`;
Compose читает его сам:

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

Адреса, по которым клиенты обращаются к панели или TLS-прокси, должны быть в
сертификате: добавить их в `KRAKEN_CERT_NAMES` и выполнить `kraken cert renew`.

- Внутри контейнера панель и Squid слушают все адреса; какие адреса хоста открыты,
  решает публикация портов.
- Docker NAT сохраняет source IP внешних клиентов IPv4. Запросы с самого хоста через
  docker-proxy приходят с адреса шлюза bridge.
- Опубликованные порты проверяются с другой машины: правила Docker обходят INPUT ufw.
  Учитывайте backend iptables/nftables и IPv6.
- Служебные порты 3130 и 3131 не публикуются.

## Файлы и права

Пользователи образа: `kraken` 10013:10013 (панель, доп. группа `proxy`) и `proxy`
13:13 (Squid и helper). Обоснование — [ADR 0009](../adr/0009-separate-squid-uid.md).

| Путь | Владелец | Режим |
| --- | --- | --- |
| `/var/lib/kraken` | `kraken:proxy` | 0750 |
| `data/` (`control.sqlite3`, `telemetry/`) | `kraken` | 0700 |
| `certs/` | `kraken:proxy` | 2750; `ca.key`, `panel.key` 0600; `proxy.*` 0640; `ca.crt`, `panel.crt` 0644 |
| `log/` (access.log и ротации, cache.log) | `proxy` | 2750, файлы 0640 |
| `backups/` (создаёт `kraken backup`) | `kraken` | 0700, архивы 0600 |
| `/dev/shm/kraken` (tmpfs) | `kraken:proxy` | 2750, файлы 0640 |
| `/dev/shm/kraken-squid` (tmpfs) | `proxy` | 0700 |

- `serve` не стартует, если `KRAKEN_RUN_DIR` не на tmpfs или принадлежит другому
  пользователю.
- Пароль менеджера Squid новый на каждый старт контейнера; при перезапуске только
  панели он берётся из tmpfs.
- Подготовка тома: корень `10013:13 0750` — готовый том, ничего не меняется (named
  volume получает раскладку из образа). Иначе, например для свежего `./data`,
  entrypoint создаёт подкаталоги и раздаёт владельцев и режимы, включая файлы
  распакованной копии. Каталог с посторонними именами верхнего уровня (кроме
  `lost+found`), symlink, hard link или специальными файлами отклоняется без
  изменений: контейнер завершается с сообщением и командой `chown` для хоста.
- Compose снимает все capabilities, кроме `CHOWN`, `SETUID`, `SETGID`, `KILL`, и
  включает `no-new-privileges`. Rootless Docker и userns-remap совместимы. Read-only
  rootfs не включён.
- `/etc/squid` не перекрывать: там файлы upstream-сборки. Конфиг Squid живёт в tmpfs
  и не монтируется.

## Перезапуск и обновление

- Pebble перезапускает панель и Squid независимо. Перезапуск панели не останавливает
  Squid; новая панель согласует журнал применения до приёма изменений. Любой запуск
  Squid проходит восстановление ([configuration-lifecycle](../architecture/configuration-lifecycle.md#запуск-и-восстановление)).
- Обновление образа пересоздаёт контейнер и рвёт открытые туннели: нужно окно.
  Порядок — в [runbooks](runbooks.md#обновление-образа).
- Отката схемы нет: откат образа возможен только на совместимую схему, иначе —
  восстановление копии. HA нет.

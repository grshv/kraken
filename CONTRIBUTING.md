# Изменения проекта

Правила работы, команды, инварианты и таблица «какой документ обновлять» —
в [AGENTS.md](AGENTS.md); они одинаковы для людей и агентов.

Коротко: один логический срез на коммит, `make check` зелёный, документы из таблицы
обновлены в том же коммите, статус — только в [docs/status.md](docs/status.md).
Изменения policy, runtime и deploy дополнительно проходят `make integration`.
Не коммитьте private keys, `.env`, SQLite, реальные access logs и персональные
данные инфраструктуры; `make scan` ищет секреты по всей истории.

Kraken — личный проект: лицензия не выбрана, отдельного процесса review нет.

## Устройство

Один контейнер: Alpine, Squid из upstream-релиза, Pebble и Go-бинарник со
встроенным React UI. Без Docker socket, privileged mode и shell API.

| Каталог | Содержимое |
| --- | --- |
| `src/cmd`, `src/internal` | Go: HTTP API, runtime Squid, политика, сертификаты, хранилище, телеметрия |
| `src/web` | React/TypeScript UI на shadcn/ui, встраивается в бинарник |
| `src/prototype` | React-мок согласованного интерфейса и его сборка в автономный HTML |
| `src/migrations`, `src/tests`, `src/tools` | Миграции SQLite, integration-тесты на реальном Squid, docs-check и SBOM |
| `api` | OpenAPI и контракт runtime |
| `deploy` | Dockerfile панели, `deploy/squid` (образ Squid), Compose, слой Pebble, правила grype |
| `docs` | Требования, архитектура, ADR, эксплуатация, статус |
| `examples` | Пример снимка конфигурации и настройки клиентов |

## Команды

```sh
make check         # docs-check, сборка и тесты web, go test -race, vet, gofmt, diff-check
make image         # контейнер kraken:dev; запуск: KRAKEN_IMAGE=kraken:dev docker compose -f deploy/compose.yaml up -d
make squid-image   # Squid из подписанного релиза локально; затем make image SQUID_IMAGE=kraken-squid:local
make integration   # Go-тесты на реальном Squid в одноразовых контейнерах (нужен image)
make scan          # govulncheck, SBOM образа + web + Squid под grype, gitleaks (нужен image)
```

Go-модуль — `src/` (Go 1.27), npm — `src/web`. Версии зафиксированы в `go.mod` и
lockfile, базовые образы и actions — по digest и SHA; обновления предлагает Renovate.
CI разделён по частям и запускается только при их изменении
([testing](docs/development/testing.md)). Подробности локального запуска — в
[local development](docs/operations/local-development.md).

## Образ

`ghcr.io/grshv/kraken` публикуется для amd64 и arm64 только после тестов своего
коммита: `main` — последний проверенный коммит, `sha-<коммит>` — фиксированная версия
(после первого релиза — `X.Y.Z`). Образ подписан attestation GitHub, происхождение
проверяет `gh attestation verify oci://ghcr.io/grshv/kraken:main --repo grshv/kraken`.

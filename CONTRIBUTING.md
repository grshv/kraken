# Разработка

Правила работы, команды, инварианты и таблица «какой документ обновлять» — в
[AGENTS.md](AGENTS.md); они одинаковы для людей и агентов.

Коротко: один логический срез на коммит, `make check` зелёный, документы из таблицы
обновлены в том же коммите, статус — только в [docs/status.md](docs/status.md).
Изменения policy, runtime и deploy дополнительно проходят `make integration`.
В `main` — только через PR с зелёными проверками. Не коммитьте ключи, `.env`, SQLite,
настоящие access logs и сведения о своей инфраструктуре; `make scan` ищет секреты по
всей истории.

Kraken — личный проект: лицензия не выбрана, отдельного процесса ревью нет.

## Устройство

| Каталог | Содержимое |
| --- | --- |
| `src/cmd`, `src/internal` | Go: API, применение конфигурации, политика, сертификаты, хранилище, телеметрия |
| `src/web` | React/TypeScript UI на shadcn/ui, встраивается в бинарник |
| `src/migrations`, `src/tests`, `src/tools` | Миграции SQLite, integration-тесты, docs-check и слияние SBOM |
| `api` | OpenAPI |
| `deploy` | Dockerfile панели, `deploy/squid` (образ Squid), Compose, слой Pebble, правила grype |
| `docs` | [Документация](docs/README.md) |
| `examples` | Пример снимка конфигурации и настройки клиентов |

## Команды

```sh
make check         # docs-check, сборка и тесты web, go test -race, vet, gofmt, diff-check
make image         # образ kraken:dev
make squid-image   # Squid из подписанного релиза; затем make image SQUID_IMAGE=kraken-squid:local
make integration   # Go-тесты на реальном Squid (нужен kraken:dev)
make scan          # govulncheck, SBOM образа под grype, gitleaks (нужен kraken:dev)
```

Go-модуль — `src/`, npm — `src/web`. Версии зафиксированы в `go.mod` и lockfile,
базовые образы и actions — по digest и SHA; обновления предлагает Renovate. CI
разделён по частям ([testing](docs/development/testing.md#ci)). Локальный запуск —
[local-development](docs/development/local-development.md).

## Релиз

1. В `main` всё зелёное, [status](docs/status.md) отражает факт.
2. Тег `vX.Y.Z` на коммите `main`: `git tag -a vX.Y.Z -m "Kraken X.Y.Z" && git push origin vX.Y.Z`.
3. `image.yml` собирает образ с версией `X.Y.Z`, прогоняет integration и
   сканирование и публикует `ghcr.io/grshv/kraken:X.Y.Z` и `latest` с attestations.
4. GitHub Release на тег: версия, коммит, digest образа, архитектуры, версия схемы
   БД, версия Squid, известные ограничения.

Проверка опубликованного образа:

```sh
gh attestation verify oci://ghcr.io/grshv/kraken:X.Y.Z --repo grshv/kraken \
  --signer-workflow grshv/kraken/.github/workflows/image.yml
```

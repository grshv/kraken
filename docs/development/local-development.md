# Локальная разработка

Нужны Docker с Compose, Go (версия из `src/go.mod`) и Node 26 для сборки UI.
Python в проекте не используется.

## Команды

```sh
make check         # docs-check, сборка и vitest web, go test -race, vet, gofmt, diff-check
make image         # образ kraken:dev
make squid-image   # Squid из подписанного релиза: kraken-squid:local
make integration   # Go-тесты на реальном Squid (нужен kraken:dev)
make scan          # govulncheck, SBOM образа под grype, gitleaks (нужен kraken:dev)
```

## Запуск

```sh
make image
KRAKEN_IMAGE=kraken:dev docker compose -f deploy/compose.yaml up -d
docker exec -it kraken kraken bootstrap --username admin
docker exec kraken kraken cert ca > /tmp/kraken-ca.crt
curl --cacert /tmp/kraken-ca.crt https://localhost:8443/
```

Compose читает `.env` рядом с `compose.yaml`, то есть `deploy/.env`, а не из корня.
Панель — `127.0.0.1:8443`, прокси — `127.0.0.1:3128` и `127.0.0.1:3129`. Каталог
хоста вместо тома — `KRAKEN_DATA=./data` в `deploy/.env`. `docker compose down`
сохраняет том, `down -v` его удаляет.

Squid закрыт для всех неописанных источников. Запросы с самого хоста через Docker
приходят с адреса шлюза bridge, а не с `127.0.0.1`: чтобы проверить клиента,
опишите этот адрес или запускайте запросы из соседнего контейнера.

Образ Squid по умолчанию — `ghcr.io/grshv/kraken-squid` по digest из `ARG SQUID_IMAGE`.
Собрать Squid локально: `make squid-image`, затем
`make image SQUID_IMAGE=kraken-squid:local`; релиз проверяется подписью ключей из
`deploy/squid/squid-keys.asc`.

## Диагностика

```sh
docker exec kraken pebble services
docker exec kraken pebble logs -n 50
docker exec kraken kraken status
docker exec kraken pebble restart control    # только панель; Squid продолжает работать
docker exec --user kraken kraken sh          # ручная диагностика без root
```

Root в контейнере без `DAC_OVERRIDE` не читает каталоги `kraken` и `proxy`; CLI
`kraken` сам сбрасывает root, для остального используйте `--user kraken`.

## Integration

`make integration` — `go test -tags integration ./tests/integration` на образе
`kraken:dev` (другой — `KRAKEN_TEST_IMAGE`). Фильтр — `RUN=TestOps`, параллельность —
`PARALLEL=1` (по умолчанию 4). Тесты создают уникально названные контейнеры, сети и
тома и удаляют их в `t.Cleanup`, пользовательский Compose не трогают. Нужны
свободные подсети `11.250.0.0/16`–`11.253.0.0/16`. Интернет нужен только для pull и
сборки. Состав тестов — [testing](testing.md#integration).

## Сканирование

`make scan` (нужен `kraken:dev`, другой — `IMAGE=`):

- `scan-go` — govulncheck по исходникам Go с достижимостью вызовов;
- `scan-sbom` — govulncheck по `pebble` из образа; syft строит CycloneDX образа и
  production-зависимостей `src/web/package-lock.json` (они вшиты в бандл);
  `src/tools/sbom` сливает их и добавляет Squid с CPE версии из `squid -v` образа;
  grype падает на high;
- `scan-secrets` — gitleaks по всей истории.

Сканеры запускаются из образов по digest; образ передаётся syft архивом, без Docker
socket. Принятые находки с причиной — `deploy/grype.yaml` и `.gitleaksignore`.
Результат — `dist/scan/`.

# Kraken

Web-панель для управления исходящим доступом клиентов через Squid: весь HTTP/HTTPS
либо только разрешённые домены. Один собственный контейнер на Alpine со Squid 7.7 из upstream-релиза,
Pebble и отдельные процессы Squid и Go. React/TypeScript UI на shadcn/ui встроен в Go-бинарник.
Node и Go toolchain нужны только при сборке. Docker socket приложению не выдаётся.

Рабочая локальная версия, не production-релиз. Что реализовано, проверено и
осталось — в [status](docs/status.md).

## Локальный запуск

Нужны Docker Engine/Compose и свободные localhost:8443 и localhost:3128. Из корня:

```sh
make image
docker compose -f deploy/compose.yaml up -d
docker exec -it kraken-kraken-1 kraken dev-cert
docker exec -it kraken-kraken-1 kraken bootstrap --username admin
```

Панель: [https://localhost:8443](https://localhost:8443).
`kraken dev-cert` создаёт самоподписанный localhost-сертификат на 30 дней только для
разработки. Браузеру нужно доверие к этому сертификату; для реальной установки
подключите свой сертификат. Пароль bootstrap вводится без echo и передаётся по stdin,
без argv/env; default password нет. В БД только salted Argon2id PHC hash. Повторный bootstrap отклоняется.

```sh
docker compose -f deploy/compose.yaml logs -f
docker compose -f deploy/compose.yaml down
```

`down` сохраняет named volume. Адреса публикуются только на loopback; для клиентов
выберите interfaces/firewall осознанно. [Установка и TLS](docs/operations/local-development.md).
Неизвестные источники запрещены; сохранение черновика само по себе не меняет Squid.

## Пароль и сброс

Исходный пароль не сохраняется; hash односторонний, старый пароль из него
не восстанавливается. Сменить пароль и отозвать сессии — командой внутри контейнера:

```sh
docker exec -it <контейнер> kraken password-reset --username admin
```

Атомарно отзывает sessions и работает также при остановленной панели.
[Подробности](docs/security/authentication-and-certificates.md#пароли-и-сброс).

## Разработка и проверки

Исходники целиком в `src/`: Go-модуль, `cmd/`, `internal/`, `migrations/`, `web/`,
`tests/`, `tools/`. Go 1.27.3 зафиксирован go.mod; npm dependencies — lockfile.
Для локальной сборки нужен совместимый Node (в Docker используется pinned Node 26 LTS).

```sh
make check         # docs, web typecheck/build, Go race tests, vet, whitespace
make build         # bin/kraken со встроенным frontend
make image         # pinned multi-stage build
make integration   # реальный Squid, отдельные временные Docker resources
```

Первый frontend build требует `npm ci` (Makefile выполняет его автоматически).
Integration использует локальные fake HTTP/HTTPS origins, проверяет доверие TLS,
all_web/allowlist, защитные запреты, apply/idempotency, restart и rollback.
Go unit tests дополнительно проверяют faults и crash recovery.

## Документация

- [Статус](docs/status.md) — реализовано, проверено, осталось.
- [Карта документов](docs/README.md): требования, архитектура, ADR, эксплуатация.
- [API](api/openapi.json) и [внутренний runtime contract](api/runtime-contract.md).
- Агенты и участники начинают с [AGENTS.md](AGENTS.md). Remote и публикация не настроены.

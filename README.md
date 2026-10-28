# Squid Control

Web-панель для управления исходящим доступом клиентов через Squid: весь HTTP/HTTPS
либо только разрешённые домены. Один собственный контейнер на базе `ubuntu/squid`,
Pebble и отдельные процессы Squid и Go. React/TypeScript UI на shadcn/ui встроен в Go-бинарник.
Node и Go toolchain нужны только при сборке. Docker socket приложению не выдаётся.

**Состояние на 28.10.2026: первый рабочий срез, для локальной проверки.**
Реализованы HTTPS-панель, bootstrap admin, Argon2id, серверные сессии/CSRF,
SQLite-черновик, индивидуальные политики, настройки Squid и expert-конфиг,
preview/diff/validate/apply, история и rollback из панели/API,
журнал применения/LKG, recovery gate, история запросов с фильтрами/cursor и аудит.
Обзор показывает состояние Squid, применённые правила, активность источников и
сгруппированные проблемы доступа за выбранный период до 30 дней; UI имеет светлую
начальную тему и ручную тёмную,
единые редакторы и read-only preview без создания ревизий от просмотра.
Базовый образ проверен на Linux/arm64: Squid 6.13/GnuTLS, TLS proxy и CONNECT.
HTTPS сайтов передаётся сквозным CONNECT с исходными сертификатами сайтов.

Результаты и ограничения: [capability report](docs/research/image-compatibility.md).
Оставшиеся проверки перед установкой: реальный APT, целевой Linux/сеть,
адресные сетевые запреты и отказы записи. Инструкции копирования volume,
замены сертификатов и обновления описаны, но требуют эксплуатационного прогона.
Дополнительные пользователи с ролями admin/viewer и парольная
аутентификация клиентов proxy по TLS реализованы. Остальные
предложенные расширения исключены из объёма. [Пересмотренный план](docs/plans/roadmap.md). Это не объявление готового
production-релиза.

## Локальный запуск

Нужны Docker Engine/Compose, Python 3.10+ для безопасного ввода пароля; свободные
localhost:8443 и localhost:3128. Из корня:

```sh
make image
make dev-cert
make bootstrap
make up
```

Панель: [https://localhost:8443](https://localhost:8443).
`dev-cert` создаёт самоподписанный localhost-сертификат на 30 дней только для
разработки. Браузеру нужно доверие к этому сертификату; для реальной установки
подключите свой сертификат. Пароль bootstrap вводится без echo и передаётся по stdin,
без argv/env; default password нет. В БД только salted Argon2id PHC hash. Повторный bootstrap отклоняется.

```sh
docker compose -f deploy/compose.yaml logs -f
make down
```

`down` сохраняет named volume. Адреса публикуются только на loopback; для клиентов
выберите interfaces/firewall осознанно. [Установка и TLS](docs/operations/local-development.md).
Неизвестные источники запрещены; сохранение черновика само по себе не меняет Squid.

## Пароль и сброс

Исходный пароль не сохраняется; hash односторонний, старый пароль из него
не восстанавливается. `make password-reset` задаёт новый через local CLI,
атомарно отзывает sessions и работает также при остановленной панели.
[Подробности и прямой CLI](docs/security/password-lifecycle.md).

## Разработка и проверки

Исходники целиком в `src/`: Go-модуль, `cmd/`, `internal/`, `migrations/`, `web/`,
`tests/`, `tools/`. Go 1.27.3 зафиксирован go.mod; npm dependencies — lockfile.
Для локальной сборки нужен совместимый Node (в Docker используется pinned Node 24).

```sh
make check         # docs, web typecheck/build, Go race tests, vet, whitespace
make build         # bin/squid-control со встроенным frontend
make image         # pinned multi-stage build
make integration   # реальный Squid, отдельные временные Docker resources
```

Первый frontend build требует `npm ci` (Makefile выполняет его автоматически).
Integration использует локальные fake HTTP/HTTPS origins, проверяет доверие TLS,
all_web/allowlist, защитные запреты, apply/idempotency, restart и rollback.
Go unit tests дополнительно проверяют faults и crash recovery.

## Документация

- [Архитектура](docs/architecture/overview.md) и [Accepted ADR 0006](docs/adr/0006-single-container.md).
- [Требования](docs/product/requirements.md), [политики/TLS](docs/architecture/policies-and-tls.md), [интерфейс](docs/product/interface.md).
- [Все документы](docs/README.md), [план](docs/plans/roadmap.md), [исходное исследование](docs/research/2026-10-27.md).
- [API](api/openapi.json) и [внутренний runtime contract](api/runtime-contract.md).

Агент начинает с [AGENTS.md](AGENTS.md). Remote и публикация не настроены.

# Карта документации

Каждый документ отвечает на один вопрос и описывает текущее состояние.
Статус реализации ведётся только в [status](status.md). Какой документ обновлять
при изменении кода — таблица в [AGENTS.md](../AGENTS.md#что-обновлять-вместе-с-кодом).
Каждый Markdown-файл в `docs/` должен быть указан здесь: это проверяет `make docs-check`.

| Документ | Вопрос |
| --- | --- |
| [status](status.md) | Что реализовано, проверено, осталось и исключено |
| [requirements](product/requirements.md) | Что должно работать; критерии приёмки AC-* |
| [decisions](product/decisions.md) | Что подтвердил владелец; открытые вопросы |
| [interface](product/interface.md) | Страницы, состояния и визуальная система UI |
| [overview](architecture/overview.md) | Процессы, компоненты, порты, структура кода |
| [policies-and-tls](architecture/policies-and-tls.md) | ACL, идентификация клиентов, expert-секция, CONNECT и TLS |
| [configuration-lifecycle](architecture/configuration-lifecycle.md) | Apply, проверка, аварии, rollback |
| [data-model](architecture/data-model.md) | Таблицы, инварианты, retention, миграции |
| [threat-model](security/threat-model.md) | Активы, границы доверия, меры защиты |
| [authentication-and-certificates](security/authentication-and-certificates.md) | Вход, сессии, пароли, TLS-сертификаты |
| [deployment](operations/deployment.md) | Целевая установка и prerequisites |
| [local-development](operations/local-development.md) | Локальный запуск и сертификаты |
| [runbooks](operations/runbooks.md) | Аварии, копия данных, восстановление, обновление |
| [observability](operations/observability.md) | Логи, метрики, диагностика |
| [testing](development/testing.md) | Какие проверки есть и что они покрывают |
| [image-compatibility](research/image-compatibility.md) | Факты о базовом образе и матрица возможностей |
| [squid-live-metrics](research/squid-live-metrics.md) | Источники и формат live-метрик Squid |
| [ADR](adr/README.md) | Почему принято каждое существенное решение |

Контракты: [OpenAPI](../api/openapi.json), [runtime contract](../api/runtime-contract.md),
[пример политики](../examples/policy.json).

## Правила

- Требования описывают ожидаемое поведение; архитектура — как устроено;
  ADR — почему; runbooks — что делать оператору.
- История изменений — в git log, а не в документах. Даты допустимы в decisions,
  ADR и research.
- Новый внешний факт сопровождается первичной ссылкой и датой проверки.
- `make docs-check` не обращается в интернет и не подтверждает актуальность внешних ссылок.

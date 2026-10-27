# Карта документации

Документы разделены по вопросам читателя. Требования отвечают «что нужно»,
архитектура — «как устроено», ADR — «почему так», runbooks — «что делать».
Это адаптация C4, ADR и Diátaxis для небольшого инфраструктурного продукта;
формальное соблюдение отдельного стандарта не является целью.

| Файл | Содержание | Когда обновлять |
| --- | --- | --- |
| [requirements](product/requirements.md) | Сценарии, границы MVP, критерии приёмки | Меняется ожидаемое поведение |
| [decisions-and-questions](product/decisions-and-questions.md) | Подтверждённые вводные, предложения, открытые вопросы | После обсуждений |
| [interface](product/interface.md) | Страницы, состояния, визуальная система | Изменяются UI и пользовательские потоки |
| [overview](architecture/overview.md) | Контекст, компоненты, deployment, структура кода | Меняются связи и сервисы |
| [policies-and-tls](architecture/policies-and-tls.md) | ACL, идентификация клиентов, CONNECT и TLS к прокси | Меняются сетевые правила |
| [configuration-lifecycle](architecture/configuration-lifecycle.md) | Apply, проверка, аварии, rollback | Любое изменение применения |
| [data-model](architecture/data-model.md) | Таблицы, инварианты, retention, миграции | Меняются данные |
| [OpenAPI](../api/openapi.json) | Начальный API критичного пути | До изменения обработчика |
| [runtime contract](../api/runtime-contract.md) | Внутренний модуль Go, без IPC | Меняются операции управления Squid |
| [threat-model](security/threat-model.md) | Активы, границы доверия, меры защиты | Новая поверхность атаки |
| [authentication-and-certificates](security/authentication-and-certificates.md) | Вход, сессии, TLS, CA lifecycle | Меняются секреты и доверие |
| [deployment](operations/deployment.md) | Целевая установка и prerequisites | Реализуется установка |
| [runbooks](operations/runbooks.md) | Аварии, backup, restore, обновления | Обнаружен новый отказ |
| [observability](operations/observability.md) | Логи, метрики, диагностика | Меняются источники сигналов |
| [agent-workflow](development/agent-workflow.md) | Как агент получает задачу и передаёт результат | Меняются команды и процесс |
| [testing](development/testing.md) | Проверки, фикстуры, CI, критерии релиза | Меняются риски и тестовые средства |
| [repository-files](development/repository-files.md) | Что необходимо сейчас и позже, без лишних файлов | Появляются инструменты |
| [roadmap](plans/roadmap.md) | Этапы и границы работ | Завершается этап |
| [diagnostics-history](plans/active/diagnostics-history.md) | История событий, фильтры, курсоры, retention и аудит | Меняется диагностика |
| [review-reliability](plans/active/review-reliability.md) | Повторное ревью: sessions, миграции, recovery и повторный вход | Меняются проверки надёжности |
| [review-followup](plans/active/review-followup.md) | Незакрытые Level-Low находки кодового ревью | Закрывается очередной задачей |
| [image-compatibility](plans/image-compatibility.md) | Обязательное исследование образа | Проверен digest |
| [research](research/2026-10-27.md) | Актуальные первичные источники | Новое исследование |
| [ADR](adr/README.md) | Реестр архитектурных решений и предложений | Меняется существенное решение |

Шаблоны: [задача](templates/task.md), [план исполнения](templates/execution-plan.md),
[ADR](templates/adr.md), [передача контекста](templates/handoff.md).

Реализованный запуск: [local development](operations/local-development.md).
Измеренные возможности/ограничения: [image report](research/image-compatibility.md).

Пароль и локальный reset: [password lifecycle](security/password-lifecycle.md).
Экспертная конфигурация: [ADR 0007](adr/0007-general-clients-expert-configuration.md).

## Правила достоверности

Документы требований являются спецификацией будущего поведения. Runbooks и
архитектурные команды, обозначенные как целевые, ещё не являются работающей
инструкцией установки. Реализованный статус фиксируется по проверкам и коммиту,
а не по появлению документа. Внешние ссылки проверялись исследованием; локальный
проверяющий скрипт не обращается в интернет и не подтверждает актуальность сайтов.

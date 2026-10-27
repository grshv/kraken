# Реестр ADR

[ADR 0006](0006-single-container.md) **Accepted**: владелец подтвердил один
контейнер и один Go-бинарник управления. ADR 0002 **Superseded**, ADR 0004 и 0007 также Accepted; остальные
предложения **Proposed** до согласования/проверки. Подтверждённые вводные:
[product decisions](../product/decisions-and-questions.md).

| ADR | Решение | Статус |
| --- | --- | --- |
| [0001](0001-single-node-control-plane.md) | Один узел, модульный backend и независимый data plane | Proposed |
| [0002](0002-restricted-local-runner.md) | Исторический host runner; заменён ADR 0006 | Superseded |
| [0003](0003-policy-revisions.md) | Typed per-VM policies, immutable revisions и LKG | Proposed |
| [0004](0004-tls-profiles.md) | Сквозной HTTPS и TLS transport | Accepted |
| [0005](0005-agent-documentation.md) | Короткие инструкции, контракты и docs как код | Proposed |
| [0006](0006-single-container.md) | Один image/контейнер, Go со static UI и Squid под Pebble | Accepted |
| [0007](0007-general-clients-expert-configuration.md) | Общие клиенты, визуальные настройки и expert squid.conf | Accepted |
| [0008](0008-kv-blob-mvp.md) | kv-blob MVP без реляционных таблиц (фактическая схема 001.sql) | Accepted |
| [0009](0009-single-process-concurrency.md) | Single-process concurrency: mutex + SetMaxOpenConns(1) + Flock | Accepted |

Новая запись использует [шаблон](../templates/adr.md). Accepted запись не переписывают
так, будто прежнего решения не было: при замене создаётся новая и Superseded ссылка.

# Реестр ADR

ADR фиксирует существенное решение: границы доверия, хранение, TLS, apply pipeline,
упаковку. Proposed не является согласием владельца. Accepted не переписывается:
при замене создаётся новый ADR, а старый получает статус Superseded со ссылкой.
Таблицу проверяет `make docs-check`: статус в ней совпадает со строкой «Статус:» в файле.

| ADR | Решение | Статус |
| --- | --- | --- |
| [0001](0001-single-node-control-plane.md) | Один узел, модульный backend и независимый data plane | Accepted |
| [0002](0002-restricted-local-runner.md) | Исторический host runner; заменён ADR 0006 | Superseded |
| [0003](0003-policy-revisions.md) | Typed policies, immutable revisions и LKG | Accepted |
| [0004](0004-tls-profiles.md) | Сквозной HTTPS и TLS transport | Accepted |
| [0005](0005-agent-documentation.md) | Документы как контракт; заменён ADR 0014 | Superseded |
| [0006](0006-single-container.md) | Один image/контейнер, Go со static UI и Squid под Pebble | Accepted |
| [0007](0007-general-clients-expert-configuration.md) | Общие клиенты, визуальные настройки и expert squid.conf с allowlist | Accepted |
| [0008](0008-kv-blob-mvp.md) | kv-blob MVP без реляционных таблиц | Accepted |
| [0009](0009-single-process-concurrency.md) | Одно соединение с SQLite; заменён ADR 0018 | Superseded |
| [0010](0010-session-and-recovery-safety.md) | Stateless anonymous CSRF, atomic migrations и verified recovery | Accepted |
| [0011](0011-durable-diagnostics.md) | История запросов в управляющей БД; заменён ADR 0017 | Superseded |
| [0012](0012-local-users-and-proxy-passwords.md) | Proxy accounts и управляемый auth helper (роли заменены ADR 0016) | Accepted |
| [0013](0013-read-only-squid-metrics.md) | Read-only метрики Squid manager на loopback | Accepted |
| [0014](0014-documentation-discipline.md) | Одна инструкция агентам, один статус, проверяемые правила docs | Accepted |
| [0015](0015-separate-squid-uid.md) | Отдельный UID для Squid, reload через manager, экспорт proxy-верификаторов | Accepted |
| [0016](0016-admin-and-manager-roles.md) | Роли admin и manager, область менеджера | Accepted |
| [0017](0017-telemetry-storage.md) | Телеметрия в месячных файлах SQLite и агрегатах на 3 года | Accepted |
| [0018](0018-storage-concurrency.md) | Пулы чтения и записи управляющей БД, CLI как второй процесс, дедлайны и защита диска | Accepted |

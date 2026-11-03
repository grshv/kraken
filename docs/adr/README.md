# Реестр ADR

ADR фиксирует решение о границе доверия, хранении, TLS, применении конфигурации или
упаковке. Proposed не означает согласия владельца. Принятый ADR не переписывается:
заменяющий получает новый номер, а старый — статус Superseded со ссылкой.
`make docs-check` сверяет статус в таблице со строкой «Статус:» в файле.

| ADR | Решение | Статус |
| --- | --- | --- |
| [0001](0001-single-container.md) | Один узел и один контейнер: Pebble, Squid и Go-бинарник с UI | Accepted |
| [0002](0002-revisions-and-apply.md) | Типизированная политика, ревизии и проверяемое применение с откатом | Accepted |
| [0003](0003-tunnel-only-https.md) | Сквозной HTTPS через CONNECT и TLS к прокси | Accepted |
| [0004](0004-expert-allowlist.md) | Клиенты прокси и expert-секция с allowlist директив | Accepted |
| [0005](0005-control-database.md) | Управляющая БД: снимки в kv, пулы, дедлайны, защита диска | Accepted |
| [0006](0006-panel-sessions.md) | Сессии панели, CSRF и ограничение входа | Accepted |
| [0007](0007-admin-and-manager-roles.md) | Роли администратора и менеджера, область менеджера | Accepted |
| [0008](0008-proxy-logins.md) | Логины прокси и fixed-argv helper | Accepted |
| [0009](0009-separate-squid-uid.md) | Squid и панель под разными UID, раскладка тома и tmpfs | Accepted |
| [0010](0010-squid-manager.md) | Manager Squid только на loopback: метрики, reload, rotate | Accepted |
| [0011](0011-telemetry-storage.md) | Телеметрия в месячных файлах SQLite и агрегатах | Accepted |
| [0012](0012-squid-image.md) | Alpine и Squid из подписанного upstream-релиза отдельным образом | Accepted |
| [0013](0013-install-and-certificates.md) | Подготовка тома и собственный CA установки | Accepted |
| [0014](0014-documentation.md) | Одна инструкция, один статус, проверяемые правила документации | Accepted |

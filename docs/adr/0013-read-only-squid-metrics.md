# ADR 0013: агрегированные метрики Squid

Статус: Accepted. Дата: 2026-10-28.

## Причина

SQLite access log содержит завершённые события и не описывает живое состояние
Squid. Обзор должен различать отсутствие наблюдения и измеренный ноль.

## Решение

Go runtime читает только фиксированные reports `info`, `counters`, `5min`,
`active_requests` по HTTP на `127.0.0.1:3131`. Squid разрешает только GET,
loopback source, этот localport и точный allowlist paths; остальные запросы на
listener и любой manager на proxy listeners запрещаются до пользовательских ACL.
`cachemgr_passwd` разрешает перечисленные read-only actions и отключает остальные;
директива закрыта expert editor. Порт 3131 принадлежит runtime, не публикуется.
Это внутренняя доверенная связь процессов контейнера, не изоляция от Go-кода.

API `/squid/metrics` доступен только admin/viewer, возвращает агрегаты без сырых
reports, URL, usernames и credentials. Каждый report ограничен 1 MiB, headers
16 KiB, весь poll — 2 s; redirects и environment proxy отключены. Cache в памяти
на 2 s ограничивает нагрузку; при отказе сохранён исходный timestamp и stale.
Недоступные значения — null. Перезапуск Go удаляет cache. GET ничего не применяет.

`active_requests` содержит чувствительные URL и usernames. Они остаются только
во временной ограниченной памяти parser, не сохраняются и не логируются. API
содержит количество request URI records без фиксированных manager/probe запросов и
idle socket blocks. Local socket определяет служебный listener: Squid переписывает
authority internal URI на первый http_port (3130), даже при запросе через 3131.
При нераспознанной структуре count отсутствует. Отчёт не предназначен для списка
соединений пользователя. При превышении лимита весь sample недоступен.

Client counters включают polling самого manager. Поэтому опубликованы origin
`server.all` totals/rates: запросы к ресурсам и обмен Squid↔origin, без cache hits.
CPU info — среднее за uptime; 5min — отдельное поле. Maximum Resident Size —
пиковая RSS, Total accounted — отдельный счётчик allocator, не current RSS.
Cache hit ratio — reported info 5min, может включать диагностические запросы.
Cache enabled берётся из подтверждённого LKG snapshot при отсутствии drift.

## Совместимость

Compiler 6 добавляет служебный listener и ACL. Старые active/LKG не переписываются
запросом GET или обычным reload. API возвращает configuration_update_required;
обновление applied snapshot проходит отдельный parse/apply/verify/journal path,
без подстановки пользовательского draft. Исторические ревизии с другим compiler/image не предлагаются для rollback
и отклоняются API; текущий renderer не обещает их совместимость. После каждого apply защита manager вновь входит в полный config.

## Проверки и ограничения

Фактическое исследование: [metrics report](../research/squid-live-metrics.md).
No mTLS, дополнительные сервисы и расшифровка HTTPS сайтов не добавляются.

# Наблюдаемость и диагностика

## Четыре независимых состояния

Панель показывает web health, состояние runtime module, процесс Squid и доступность
проверяемых внешних ресурсов раздельно. «Контейнер запущен» не значит «APT работает».
Каждый статус имеет `observed_at`, источник и stale marker.

## Источники

- Squid access log в явно выбранном format; его доступность и поля проверяются image spike.
- Squid/Pebble service logs — reload, DNS, startup и TLS/helper failures.
- Внутренний operation journal — этап, duration, candidate/LKG hashes, findings.
- Web structured logs — request/operation ID, safe actor ID, status, duration.
- Audit — кто изменил клиента, политику, сертификатную ссылку и кто применил ревизию.

Потоки разные по retention и чувствительности; один бесконечный log stream не нужен.

## Access log pipeline

Внутренний log reader отдаёт bounded tail по opaque cursor, без произвольного path и grep shell.
Parser различает format version, rotation/truncation, partial lines, oversized
records, невалидный UTF-8 и timestamp. В web хранится ограниченный рабочий хвост
и агрегаты; полноценный логовый кластер для MVP не требуется.

Log output экранируется, URL query/userinfo удаляются до хранения/показа. Для
inspection предпочтителен host-only формат без HTTP payload. Parser не считает
внешнюю строку инструкцией и не формирует из неё исполняемую shell-команду.
После rotation нет бесконечного дублирования; при потере данных явный gap marker.
Backpressure или поломка ingestion не должны блокировать трафик Squid.

## Метрики

Предлагаемые counters/gauges: squid_up, control_up, config_drift,
last_verified_timestamp, operations_total по action/outcome, apply_duration,
requests_total по ограниченному outcome, bytes, active_connections если доступны,
log_ingest_lag, dropped_records, disk_free, cert_not_after.

Не label-ить метрики полными URL, query, operation UUID или произвольными hostnames.
Per-client totals допустимы с ограниченным числом зарегистрированных клиентов.
Назначение/запрос искать в bounded log view, а не в бесконечной cardinality метрик.

Bytes CONNECT могут быть только частью измеряемого трафика в выбранном формате;
до проверки не называть их точной экономией bandwidth или полным двунаправленным
учётом. Для кеша показывать только подтверждённые HTTP hit/miss counters.

## Объяснение отказа

UI показывает наблюдаемый факт (например TCP_DENIED/403), policy revision на момент
запроса если известна, VM и requested destination. Если совпавший ACL не был
записан, точное правило не угадывать. Симулятор политики — отдельная оценка,
подписанная «по текущим правилам», а не историческое доказательство.

Для HTTPS tunnel: CONNECT status, host:port, длительность; содержимое сайта скрыто.
Для inspect: больше данных только если pipeline действительно их записал.
TLS bypass/splice отмечается, чтобы оператор не ожидал URL-path диагностику.

## Retention и алерты

Предварительно: raw logs 7 d, агрегаты 30 d, audit 90 d, плюс byte quotas.
Watermarks диска и сроки уточнить до deploy. Alert по runtime down, config drift,
rollback degraded, cert expiry и log gaps. Уведомляющий внешний канал в MVP не
подключён; предупреждения панели не обещают доставку при выключенном браузере.
Audit в локальной SQLite не является неизменяемым forensic-хранилищем.

## Общий контейнер

Pebble следит за процессами отдельно. Ошибка панели не должна приводить watchdog
к пересозданию всего контейнера при работающем Squid. Container-level status
не подменяет состояние каждого процесса. Upgrade общего image прерывает оба.

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

Реализован фоновый reader: каждые 2 секунды читает до 256 KiB из фиксированных
access.log и ротаций .0… .29, в порядке от старых к новым. Checkpoint содержит
inode/device, offset, hash последних 64 bytes и generation. До 64 identities
(включая временно исчезнувшие файлы) сохраняются для продолжения без replay. События и checkpoint
сохраняются одной SQLite-транзакцией, строки получают ID по generation/offset.
После рестарта чтение продолжается без повторной вставки. Rename rotation
сохраняет позицию; уменьшение размера или несовпадение anchor отмечает truncation.
Одинаковое содержимое при подмене inode/mtime не обещает обнаружения всех потерь.

Строка до 16 KiB разбирается до сохранения. Незавершённая строка текущего файла
ждёт окончания; oversized строка пропускается до newline. Некорректные записи,
оборванный конец ротации и исчезновение отслеживаемого файла помечают sticky gap.
Первое чтение начинается с доступных ротаций: до установки сборщика полнота
неизвестна. Наличие следующей страницы обозначает next_cursor отдельно от gap.

Сохраняются source IP, destination host:port, method, Squid result, proxy status,
duration и bytes. URL path/query/userinfo не попадают в нормализованную историю.
Сырой файл Squid может содержать URL и остаётся чувствительным: это отдельный
источник с файловыми правами/rotation. Для HTTPS доступны только CONNECT metadata;
длительные туннели логируются при закрытии. Исторические ACL/revision неизвестны.

Имя/ID клиента — уникальное совпадение source IP/CIDR и, для auth-bound clients,
logged proxy username в активном manifest при сборе (policy_at_ingest), включая подписи источников в expert mode. Неоднозначные
совпадения остаются unknown. Изменение клиента не переписывает сохранённую запись.
Фильтр включает сохранённые ID, даже если клиента уже нет в текущем draft;
список показывает до 200 последних известных клиентов.

GET /events читает историю из БД и не запускает ingestion. Курсор привязан к
фильтрам и идёт по убывающей последовательности, поэтому новые запросы не сдвигают
уже открытые страницы. Доступны точный client_id и failures_only (DENIED/FAIL либо
proxy HTTP status >= 400). Неизвестный matched ACL не угадывается.
Панель показывает последнее успешное чтение, backlog, source availability,
число пропущенных строк и stale после 15 секунд без успешного чтения.
Ошибка API оставляет показанные записи с предупреждением об устаревании.
Все поля выводятся текстом, без HTML и исполнения ссылок/команд.

GET /audit показывает actor/action/entity/time из audit_events с точными
фильтрами actor/action и cursor. Это журнал принятых действий: итог apply смотрят
в operation/history. Пароли, session tokens, ключи и тела конфигураций туда не
добавляются. Доступ к событиям, /events/clients и аудиту имеют admin и viewer;
изменение конфигурации/учётных записей viewer недоступно.
Решение: [ADR 0011](../adr/0011-durable-diagnostics.md).

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

## Retention и алерты

Реализовано: нормализованные события — 7 дней по времени сбора и не более
50 000 строк; audit — 90 дней и не более 5000 строк. Обрезание истории обозначается
у конца доступных страниц; sticky gap также хранит известные потери reader.
Raw logs ограничены rotation count и периодической ротацией по размеру, не
автоматическим TTL 7 дней. Агрегаты, disk watermarks и byte quotas — будущая работа.
Watermarks диска и сроки уточнить до deploy. Alert по runtime down, config drift,
rollback degraded, cert expiry и log gaps. Уведомляющий внешний канал в MVP не
подключён; предупреждения панели не обещают доставку при выключенном браузере.
Audit в локальной SQLite не является неизменяемым forensic-хранилищем.

## Общий контейнер

Pebble следит за процессами отдельно. Ошибка панели не должна приводить watchdog
к пересозданию всего контейнера при работающем Squid. Container-level status
не подменяет состояние каждого процесса. Upgrade общего image прерывает оба.

## Показатели обзора

GET /statistics читает не более 50 000 нормализованных строк SQLite; новых tables,
фонового агрегатора или сервиса нет. Окно [now−24h, now) определяется timestamp
события, в ответе 24 часовых bucket. requests, DENIED, logged bytes и source IP
считаются только по сохранённым завершённым записям. `observed_at` — время расчёта;
свежесть источника берётся из ingestion metadata /events. Нельзя считать
calculation time доказательством текущего чтения Squid. `retention_limited`
обозначает достижение cap; полнота суток тогда не обещается. Ошибка DB/формата
возвращает 503, а не ложные нули. HTTP403 от origin не равен Squid DENIED.

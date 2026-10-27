# Данные и инварианты

SQLite — источник desired состояния и пользовательского аудита.
Filesystem journal внутреннего runtime module фиксирует применение к процессу Squid.
SQLite и journal обслуживает один Go executable, но транзакции между DB, файлами
и Squid нет. Recovery gate умеет восстановить LKG без HTTP readiness панели.

## Сущности

| Сущность | Основные поля | Инвариант |
| --- | --- | --- |
| users | id, username, password_hash, enabled, created_at | Уникальное нормализованное имя; не plaintext password |
| sessions | id, user_id, token_hash, csrf_secret, expires_at, last_seen_at | Отзываются при logout/password reset; raw token не хранится |
| clients | id, name, description, enabled, version | Устойчивый ID, независимый от имени |
| client_networks | client_id, canonical_cidr | Нет пересечений enabled clients; проверка в транзакции |
| policies | id, name, mode, tls_profile_id, version | Только известный enum; пустой allowlist закрывает доступ |
| destinations | policy_id, domain_ascii, include_subdomains | Уникальная нормализованная цель |
| tls_profiles | id, mode(tunnel), transport, cert_ref, version | Ссылки на секреты; ключей в DB нет |
| revisions | id, snapshot_json, policy_hash, compiler_version, image_digest, created_by, created_at | Immutable; содержит snapshot всех clients/policies |
| validations | id, revision_id, capability_hash, status, findings, expires_at | Действительна только для точного набора inputs |
| operations | id, idempotency_key, request_hash, revision_id, expected_revision, action, status, timestamps | Один key — один payload |
| applied_state | singleton_id, revision_id, observed_hash, verified_at, status | Не обновляется при сохранении draft |
| audit_events | id, actor_id, action, entity_id, operation_id, safe_details, created_at | Добавление в транзакции с mutation |
| log_cursors | source, inode_or_file_id, offset, updated_at | Rotation-aware ingestion; не бесконечный replay |
| traffic_buckets | time_bucket, client_id, counters, format_version | Ограниченная cardinality, нет full URL в ключах |
| schema_migrations | version, checksum, applied_at | Применённая миграция не редактируется |

Это логическая схема, не готовые SQL migrations. SQL constraints, индексы и
retention jobs реализуются первым вертикальным срезом.

## Версии и consistency

`version` для mutable draft строк увеличивается при каждом изменении.
Изменения source ranges сериализуются внутри write transaction; проверка пересечений
вне транзакции не обеспечивает инвариант при гонках.
Apply использует snapshot целой конфигурации, не читает live draft по частям.
Удаление клиента из draft не удаляет прошлые ревизии и события.
Персональные display names в snapshots считаются чувствительной конфигурацией.

Время — UTC RFC 3339 в API; UI переводит в timezone оператора и подписывает её.
Идентификаторы — opaque UUID; сортировка по ID не заменяет timestamp.
Байты/длительности передаются без потери точности с явно указанными единицами.

## SQLite

Включить foreign keys, busy timeout, WAL на local filesystem; bounded connection
pool, короткие write transactions, контролируемый checkpoint. Режим synchronous
и конкретный driver выбрать по результатам durability tests; не ослаблять fsync
ради красивого benchmark. Не размещать DB на NFS. На практике `store.Open`
включает `foreign_keys(1)`, `journal_mode=WAL)`, `busy_timeout=5000)`
через DSN-прагмы, а `synchronous=FULL` — через `PRAGMA` после открытия, чтобы
параметр не зависел от формы пула соединений. Connection pool `SetMaxOpenConns(1)`
сериализует writers (см. ADR 0009).

Backup через SQLite backup API или остановленный согласованный snapshot;
копирование только main DB при активном WAL недопустимо как общая стратегия.
[SQLite WAL](https://www.sqlite.org/wal.html),
[Backup API](https://www.sqlite.org/backup.html).

Runtime journal хранит operation ID, request hash, stage, previous/candidate
manifest (compiler_version, revision_id, hash, image_digest, config, snapshot),
secret references и terminal outcome. Он связывается с записью operations по
одному ID: отдельного runner_id/IPC нет. Записи и manifest crash-safe; lock
общий для web worker и startup gate того же бинарника. LKG/recovery metadata
доступны gate при недоступной web DB; новые изменения без DB/audit не
допускаются. Формат проверяется fault injection.

## Фактическая схема MVP (ADR 0008)

Вместо полной реляционной схемы из верхней таблицы реализован kv-blob MVP:

- `draft` и `revision:<id>` хранятся в таблице `kv(key, value)` как opaque JSON.
  Целостность клиентов/политик/TLS-профилей проверяется на уровне приложения
  в `policy.Validate`. Optimistic-lock `expected_version` для draft — в
  `store.Change`. Совместимость ревизии с image/compiler — в `createOperation`.
- `operations` — отдельная таблица `(id, idempotency_key UNIQUE, request_hash,
  body)` с индексом на `json_extract(body,'$.revision_id')`. Idempotency-key
  lookup и CountSucceededByRevision идут через store.Store методы, raw SQL в
  HTTP handlers не используется.
- `audit_events` — отдельная таблица `(id, actor, action, entity_id,
  created_at INTEGER)`. С migration 002 `created_at` хранится как INTEGER unix
  секунд (раньше был TEXT RFC3339); range-queries на Prune используют индекс
  на created_at.
- `sessions` — авторизованные sessions с hash случайного cookie token.
  Новые анонимные CSRF holders подписываются в памяти без DB rows. FK пока не
  добавлен для совместимости с прежними anonymous rows (`username=''`), которые
  истекают и удаляются обычной очисткой; целостность проверяет приложение.

Переход к полной реляционной схеме требует миграции 004+ и пересмотра
`draftMu`/`opsMu` (см. ADR 0009).

## Миграции и архивирование

Миграции вперёд нумеруются и checksum фиксируется. Перед изменением схемы — backup.
Откат приложения возможен только при совместимой схеме, иначе restore согласованной
копии, а не произвольное `down`. Compatibility window документируется в релизе.
Первая миграция (`migrations/001.sql`) регистрируется в `schema_migrations` с
checksum; будущие миграции обязаны следовать тому же шаблону.
Bootstrap, DDL, перенос данных и версия выполняются одной транзакцией SQLite.
Ошибка откатывает весь переход, повторный запуск начинает с прежней схемы.
Это не заменяет backup и не чинит автоматически частичную миграцию, созданную
старой версией приложения. См. [ADR 0010](../adr/0010-session-and-recovery-safety.md).

Предварительный retention: raw access logs 7 дней, агрегаты 30 дней, audit 90 дней,
последние 20 конфигураций плюс закреплённая LKG. Сроки — предложения, не договорённость.
Удаление по сроку дополняется лимитами bytes и disk watermark. Активная ревизия,
LKG и ссылки незавершённых operations не удаляются. Не путать rotation с backup.

`store.Prune` реализует базовый retention: удаляет audit_events старше cutoff (по
умолчанию 90 дней) и оставшиеся сверху 5000 событий по capped-retention, а также
ревизии, которые не ссылаются на succeeded operation и не входят в последние N
(по умолчанию 20). Вызывается из периодического `Manager.Maintain`. Schema-level
FK на сессии пока не добавлен из-за legacy anonymous rows; новые anonymous
holders в таблицу не записываются. Целостность проверяется приложением.

## Реализованное расширение

Client.kind: host/network/group, несколько source networks. settings и expert_config
хранятся в immutable snapshot, исходный admin password туда не включается.
users.password_hash хранит PHC или совместимый legacy verifier. CLI reset меняет
его вместе с удалением sessions/audit в одной транзакции. Логическая схема выше
не объявляет будущие tables/migration retention полностью реализованными.

## Диагностическая история (migration 003)

proxy_events: seq INTEGER PRIMARY KEY AUTOINCREMENT, event_id UNIQUE, client_id,
client_name, failed, ingested_at, body (нормализованный JSON). Индексы client/seq,
failed/seq и ingested_at. kv.event_checkpoint хранит offsets/anchors/generations,
last successful read и известные gap/dropped/backlog/source flags. Вставка событий
и checkpoint — одна транзакция. Сырой текст и URL path/query/userinfo не хранятся.

Retention events: 7 дней по ingested_at, максимум 50 000 строк, применяется при
успешном сборе и периодическом Prune, даже если источник затем недоступен.
Audit использует прежние 90 дней/5000 строк. Cursor — bounded base64 JSON с before
и hash вида журнала/фильтров; это позиция, не credential. Все выборки авторизованы,
SQL параметры связаны; произвольный cursor не расширяет доступ. Новые строки
не сдвигают старые страницы, retention может сократить доступную историю.
Client labels фиксируются при сборе; revision_id null. См. [ADR 0011](../adr/0011-durable-diagnostics.md).

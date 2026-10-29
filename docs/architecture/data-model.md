# Данные и инварианты

SQLite — источник desired состояния и пользовательского аудита.
Filesystem journal внутреннего runtime module фиксирует применение к процессу Squid.
SQLite и journal обслуживает один Go executable, но транзакции между DB, файлами
и Squid нет. Recovery gate умеет восстановить LKG без HTTP readiness панели.

## Фактические данные

| Таблица | Что хранит | Инвариант |
| --- | --- | --- |
| users | username, password_hash, role, enabled | Только verifier; admin/viewer, последний active admin защищён |
| sessions | token_hash, username, csrf, expires_at, last_seen_at | Cookie token хранится только как hash, password/role/disable mutation отзывает sessions |
| proxy_accounts | username, password_hash, enabled, created_at, updated_at | Отдельные credentials proxy, без прав в панели |
| kv | key, value | Draft/revisions/checkpoint как JSON; optimistic version и validation на уровне приложения |
| operations | id, idempotency_key, request_hash, body | Один key — один payload; outcome операции хранится отдельно от draft |
| audit_events | id, actor, action, entity_id, created_at | Без паролей/ключей/config body, mutation и audit атомарны |
| proxy_events | seq, event_id, client_id/name, failed, ingested_at, body | Уникальная нормализованная запись; события и checkpoint атомарны |
| schema_migrations | version, checksum, applied_at | DDL/data/version в одной транзакции; применённая миграция не редактируется |

Клиенты, source networks, policies, destinations, TLS profiles и settings —
типизированные части snapshot в kv, а не отдельные SQL tables. Правила ссылок,
пересечений IP/auth identities и secret refs проверяет policy.Validate.
Validation привязана к immutable revision и operation. Отдельных validations,
traffic_buckets, client_networks и applied_state tables нет; их создание и полная
реляционная переделка не запланированы. Точное применение подтверждает manifest
и fingerprint процесса Squid, не один JSON-флаг в SQLite.

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

Для текущей поставки достаточно остановленной согласованной копии /data и
внешних TLS mounts. Горячий SQLite backup API не требуется;
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
  created_at INTEGER)` — unix секунды; range-queries на Prune используют индекс
  на created_at.
- `sessions` — только авторизованные sessions с hash случайного cookie token,
  FK на `users` с `ON DELETE CASCADE`. Анонимные CSRF holders подписываются в
  памяти без DB rows.

Не запланированный переход к полной реляционной схеме потребовал бы новых миграций и пересмотра
`draftMu`/`opsMu` (см. ADR 0009).

## Миграции и архивирование

Миграции вперёд нумеруются и checksum фиксируется. Перед изменением схемы — backup.
Откат приложения возможен только при совместимой схеме, иначе restore согласованной
копии, а не произвольное `down`. Compatibility window документируется в релизе.
`migrations/001.sql` — базовая схема; следующие миграции добавляются файлами
`002.sql` и далее в `migrations.Ordered`. Runner записывает version и checksum в
`schema_migrations`. DDL, перенос данных и версия выполняются одной транзакцией
SQLite: ошибка откатывает весь переход. Записанный checksum, не совпадающий с
checksum этой сборки, означает базу несовместимой сборки — `store.Open`
отказывается её открывать. См. [ADR 0010](../adr/0010-session-and-recovery-safety.md).

Текущий retention нормализованных событий и audit указан ниже. Отдельные
30-дневные агрегаты и disk watermark automation не реализованы и не входят в
объём. Активная ревизия, LKG и ссылки незавершённых operations сохраняются;
rotation сырого access.log не равна backup.

`store.Prune` реализует базовый retention: удаляет audit_events старше cutoff (по
умолчанию 90 дней) и оставшиеся сверху 5000 событий по capped-retention, а также
ревизии, которые не ссылаются на succeeded operation и не входят в последние N
(по умолчанию 20). Вызывается из периодического `Manager.Maintain`.

## Реализованное расширение

Client.kind: host/network/group, несколько source networks. settings и expert_config
хранятся в immutable snapshot, исходный admin password туда не включается.
users.password_hash хранит только Argon2id PHC verifier. CLI reset меняет
его вместе с удалением sessions/audit в одной транзакции. Логическая схема выше
не объявляет будущие tables/migration retention полностью реализованными.

## Диагностическая история

proxy_events: seq INTEGER PRIMARY KEY AUTOINCREMENT, event_id UNIQUE, client_id,
client_name, failed, ingested_at, body (нормализованный JSON). Индексы client/seq,
failed/seq и ingested_at. kv.event_checkpoint хранит offsets/anchors/generations,
last successful read и известные gap/dropped/backlog/source flags. Вставка событий
и checkpoint — одна транзакция. Сырой текст и URL path/query/userinfo не хранятся.

Retention events: 30 дней по ingested_at, максимум 50 000 строк, применяется при
успешном сборе и периодическом Prune, даже если источник затем недоступен.
Audit использует прежние 90 дней/5000 строк. Cursor — bounded base64 JSON с before
и hash вида журнала/фильтров; это позиция, не credential. Все выборки авторизованы,
SQL параметры связаны; произвольный cursor не расширяет доступ. Новые строки
не сдвигают старые страницы, retention может сократить доступную историю.
Client labels фиксируются при сборе; revision_id null. См. [ADR 0011](../adr/0011-durable-diagnostics.md).

## Учётные записи

users хранит role (admin/viewer) и enabled. Серверный readSession читает актуальное состояние user.
Role/enabled/password mutation и удаление его sessions/audit атомарны. Проверка
последнего active admin входит в ту же transaction. Имена неизменяемы; вместо
удаления используется блокировка с сохранением audit identity.

proxy_accounts — отдельная таблица username/password_hash/enabled/created_at/updated_at.
Пароль не входит в snapshot; Client.proxy_username — только ссылка. Фиксированные
helper режимы читают не БД, а экспорт `/data/runtime/proxy-accounts.json`
(username и verifier включённых аккаунтов, 0640), который Go атомарно перезаписывает
после каждой мутации и при старте; при ошибке записи мутация отвечает 503. Generation
SHA-256 от PHC verifier привязывает Basic auth к текущему состоянию без передачи
verifier на сторону клиента. См. [ADR 0012](../adr/0012-local-users-and-proxy-passwords.md).

Обзор не сохраняет отдельные traffic_buckets: bounded агрегаты `/activity`
вычисляются из proxy_events за 15 минут, час, день, неделю или последние 30 дней.
Суточный `/statistics` сохранён для совместимости, но обзор его не опрашивает.
Отдельная подсистема долгосрочной аналитики и изменение схемы БД не требуются.
Retained события ограничены 30 днями по времени сбора и 50 000 строками; cap
может сократить доступный период. Ранее удалённые события не восстанавливаются,
TTL аудита не изменён.

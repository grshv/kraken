# Данные и инварианты

SQLite — источник desired состояния и пользовательского аудита.
Filesystem journal внутреннего runtime module фиксирует применение к процессу Squid.
SQLite и journal обслуживает один Go executable, но транзакции между DB, файлами
и Squid нет. Recovery gate умеет восстановить LKG без HTTP readiness панели.

## Фактические данные

| Таблица | Что хранит | Инвариант |
| --- | --- | --- |
| users | username, password_hash, role, enabled | Только verifier; admin/manager, последний active admin защищён |
| sessions | token_hash, username, csrf, expires_at, last_seen_at | Cookie token хранится только как hash, password/role/disable mutation отзывает sessions |
| manager_networks | username, cidr | Сети менеджера; FK на users ON DELETE CASCADE, PK по паре |
| manager_clients | username, client_id | Назначенные клиенты менеджера; FK на users ON DELETE CASCADE, PK по паре; висячие удаляются при обслуживании |
| proxy_accounts | username, password_hash, enabled, created_at, updated_at, owner | Отдельные credentials proxy; owner = username панели или NULL (admin). Менеджер видит свои и логины только его клиентов |
| kv | key, value | Draft/revisions как JSON; optimistic version и validation на уровне приложения |
| operations | id, idempotency_key, request_hash, body | Один key — один payload; outcome операции хранится отдельно от draft |
| audit_events | id, actor, action, entity_id, created_at | Без паролей/ключей/config body, mutation и audit атомарны |
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

Foreign keys, busy timeout, WAL на локальной файловой системе, короткие транзакции
записи и контролируемый checkpoint; DB не размещается на NFS, fsync не ослабляется
([ADR 0018](../adr/0018-storage-concurrency.md)). `store.Open` разделён на writer pool (`_txlock=immediate`, `SetMaxOpenConns(1)`,
`synchronous=FULL`, `journal_size_limit=64MiB`) и read-only reader pool
(`query_only(1)`, до 4 соединений) для тяжёлых чтений (аудит, ревизии, проверки
сессий). DSN-прагмы `foreign_keys(1)`, `journal_mode(WAL)`, `busy_timeout(5000)`
применяются к обоим пулам; `synchronous=FULL` и `journal_size_limit` — через
`PRAGMA` после открытия writer. Для in-memory/`file:` DSN в тестах reader равен
writer (один пул). Reader не блокирует writer, writer не ждёт reader'а.
Сессия читается через reader; `last_seen_at` обновляется короткой
write-транзакцией не чаще раза в минуту условным
`UPDATE ... WHERE token_hash=? AND last_seen_at=?` — удалённая параллельным
reset'ом сессия не воскрешается (recheck existence). Периодическое обслуживание:
`PRAGMA wal_checkpoint(TRUNCATE)` раз в 10 мин, `PRAGMA optimize` суточно.

Онлайн-копию делает `kraken backup` (`VACUUM INTO` управляющей БД и `rollups.db`,
runtime и сертификаты); остановленная копия /data тоже согласована. Копирование
только main DB при активном WAL недопустимо.
[SQLite WAL](https://www.sqlite.org/wal.html),
[Backup API](https://www.sqlite.org/backup.html).

Runtime journal хранит operation ID, request hash, stage, previous/candidate
manifest (compiler_version, revision_id, hash, image_digest, config, snapshot),
secret references и terminal outcome. Он связывается с записью operations по
одному ID: отдельного runner_id/IPC нет. Записи и manifest crash-safe; lock
общий для web worker и startup gate того же бинарника. LKG/recovery metadata
доступны gate при недоступной web DB; новые изменения без DB/audit не
допускаются. Формат проверяется fault injection.

## Фактическая схема (ADR 0008)

Вместо полной реляционной схемы из верхней таблицы реализовано kv-хранение:

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
  на created_at. PUT и DELETE draft пишут по одной строке на каждого
  изменённого клиента и политику в той же транзакции: `entity_id` = их id,
  `action` = `update_draft` (PUT) или `discard_draft` (DELETE). `GET /audit`
  принимает фильтр `entity_id` (до 256 символов, иначе 400 `invalid_filter`);
  отпечаток cursor учитывает фильтр, менеджер видит только свои записи.
- `sessions` — только авторизованные sessions с hash случайного cookie token,
  FK на `users` с `ON DELETE CASCADE`. Анонимные CSRF holders подписываются в
  памяти без DB rows.

Переход к полной реляционной схеме потребовал бы новых миграций и пересмотра
`draftMu`/`opsMu` ([ADR 0018](../adr/0018-storage-concurrency.md)).

## Миграции и архивирование

Миграции вперёд нумеруются и checksum фиксируется. Перед изменением схемы — backup.
Откат приложения возможен только при совместимой схеме, иначе restore согласованной
копии, а не произвольное `down`. Compatibility window документируется в релизе.
`migrations/001.sql` — базовая схема; следующие миграции добавляются файлами
`002.sql` и далее в `migrations.Ordered`. `003.sql` добавляет составной индекс
`(client_id, ingested_at)` для rollup-запросов инспектора. `004.sql`
(`telemetry-split-v1`) выносит события прокси из управляющей БД: удаляет
`proxy_events`, его индексы и `event_checkpoint` из `kv`; данных для переноса нет
(тестовые тома), телеметрия начинается с нуля. Runner записывает version и checksum в
`schema_migrations`. DDL, перенос данных и версия выполняются одной транзакцией
SQLite: ошибка откатывает весь переход. Записанный checksum, не совпадающий с
checksum этой сборки, означает базу несовместимой сборки — `store.Open`
отказывается её открывать. См. [ADR 0010](../adr/0010-session-and-recovery-safety.md).

Текущий retention телеметрии и audit указан ниже. Долгоживущие агрегаты
реализованы в телеметрии (см. «Диагностическая история»); пороги диска
(пауза сбора событий, 507 на изменения) описаны в
[observability](../operations/observability.md). Активная ревизия, LKG и ссылки
незавершённых operations сохраняются; rotation сырого access.log не равна backup.

`store.Prune` реализует retention управляющей БД: удаляет audit_events старше
`store.AuditRetention` (3 года) без лимита на количество строк, а также
ревизии, которые не ссылаются на succeeded operation и не входят в последние N
(по умолчанию 20). Retention телеметрии (сырые события и агрегаты) выполняет
`telemetry.Store.Maintain` в том же периодическом тике `Manager.Maintain`.

## Реализованное расширение

Client.kind: host/network/group, несколько source networks. settings и expert_config
хранятся в immutable snapshot, исходный admin password туда не включается.
users.password_hash хранит только Argon2id PHC verifier. CLI reset меняет
его вместе с удалением sessions/audit в одной транзакции. Логическая схема выше
не объявляет будущие tables/migration retention полностью реализованными.

## Диагностическая история

События прокси хранятся вне управляющей БД, в пакете `internal/telemetry`:
месячные файлы SQLite `events-YYYY-MM.db` (сырые нормализованные события и
checkpoint чтения access.log) и `rollups.db` (долгоживущие агрегаты). Управляющая
БД хранит только audit и более не содержит `proxy_events`; миграция 004
(`telemetry-split-v1`) удаляет `proxy_events`, его индексы и `event_checkpoint`
из `kv`. См. [ADR 0017](../adr/0017-telemetry-storage.md).

events-YYYY-MM.db: `host(id, name UNIQUE)` — словарь хостов назначений; `event(id,
ts, client_id, client_name, source_ip, host_id, port, method, result,
http_status, duration_ms, bytes, failed, proxy_username, revision_id,
match_basis, tls_mode, source_id)` — индексы `ts`, `(client_id, ts)`,
`(source_ip, ts) WHERE client_id IS NULL` и уникальный `source_id`; `checkpoint(k,
v)` — позиция чтения access.log, пишется в одной транзакции с событиями.
`source_id` сохраняет исходный id события (хэш строки access.log) для lookup'а
explain по legacy id; API выставляет компактную форму `<YYYYMM>-<rowid>`. Сырой
текст URL и path/query/userinfo не хранятся.

rollups.db (таблицы WITHOUT ROWID, агрегируются UPSERT на месте): `client_15m`,
`client_host_1h`, `client_host_1d` (+ индекс `client_id, day`) — по клиентам;
`unknown_15m`, `unknown_host_1h`, `unknown_host_1d` (с `bytes`) — по неописанным
источникам; `watermark(month, last_event_id)` — до какого события месяц свёрнут. Bucket
хранится как unix-секунды. Rollup идемпотентен: падение между ingest и rollup
даёт повтор с прежнего watermark; потеря rollups.db перестраивается из сырых
месячных файлов при открытии.

Каждый файл: один writer (`_txlock=immediate`, `busy_timeout=5000`, WAL,
`synchronous=NORMAL`) и пул read-only readers (`query_only`); месячные файлы
открываются лениво, кеш открытых — 4. Битый или несовместимый файл телеметрии
переименовывается в `.broken-<unix>`, создаётся заново, warn в лог и
`status.instance.telemetry_broken_files`; панель продолжает работать. Текущий
месяц никогда не удаляется ретеншном.

Retention: сырые события — 90 дней по возрасту и бюджет 5 GiB (oldest-first,
текущий месяц не трогается); часовые агрегаты — 30 дней; 15-минутные и суточные
агрегаты — 3 года. Audit управляющей БД — 90 дней и до 5000 строк. Месячные файлы
удаляются целиком (нет DELETE миллионов строк, VACUUM и фрагментации); агрегаты
чистятся чанками по 5000 строк по составному PK.

Cursor журнала — bounded base64 JSON с before и hash вида фильтров; это позиция,
не credential, привязан к убывающей последовательности `(месяц, id)`. Новые строки
не сдвигают старые страницы. Все выборки авторизованы, SQL параметры связаны;
произвольный cursor не расширяет доступ. Client labels фиксируются при сборе;
revision_id null. Изменение клиента не переписывает сохранённую запись.

## Учётные записи

users хранит role (admin/manager) и enabled. Серверный readSession читает актуальное состояние user.
Role/enabled/password mutation и удаление его sessions/audit атомарны. Смена роли отзывает
сессии; смена области менеджера — нет. Проверка последнего active admin входит в ту же
transaction. Имена неизменяемы; вместо удаления используется блокировка с сохранением audit
identity.

manager_networks и manager_clients — назначения области менеджера, данные панели, а не
конфигурации Squid. Область читается из БД на каждом запросе, как role и enabled.

proxy_accounts — отдельная таблица username/password_hash/enabled/created_at/updated_at/owner.
owner — username менеджера или NULL (admin). Менеджер видит и меняет свои логины (owner = он)
и логины, которыми пользуются только его клиенты; созданный менеджером логин принадлежит ему.
Пароль не входит в snapshot; Client.proxy_username — только ссылка. Фиксированные
helper режимы читают не БД, а экспорт `/data/runtime/proxy-accounts.json`
(username и verifier включённых аккаунтов, 0640), который Go атомарно перезаписывает
после каждой мутации и при старте; при ошибке записи мутация отвечает 503. Generation
SHA-256 от PHC verifier привязывает Basic auth к текущему состоянию без передачи
verifier на сторону клиента. См. [ADR 0012](../adr/0012-local-users-and-proxy-passwords.md).

Обзор не сохраняет отдельные traffic_buckets: bounded агрегаты `/activity`
вычисляются из rollups.db за 15 минут, час, день, неделю или последние 30 дней.
`/statistics` и инспектор читают только агрегаты (кроме explain и draft-impact,
которые читают сырые события за 24 ч); суточный `/statistics` сохранён для
совместимости, но обзор его не опрашивает. Отдельная подсистема долгосрочной
аналитики не нужна: суточные агрегаты хранятся 3 года. Ранее удалённые сырые
события не восстанавливаются, TTL аудита не изменён.

# ADR 0008: kv-blob MVP без реляционных таблиц

Дата: 2026-10-27. Статус: Accepted.

## Контекст

`docs/architecture/data-model.md` описывает реляционную схему SQLite:
`clients`, `client_networks`, `policies`, `destinations`, `tls_profiles` и т.д.
Каждая сущность отдельной таблицей со ссылочной целостностью и индексами.

Первый рабочий срез реализует не эту схему. `src/migrations/001.sql` хранит
через простой `kv(key TEXT PRIMARY KEY, value TEXT NOT NULL)`:
- `draft` — текущий черновик (JSON с `version` + `snapshot`)
- `revision:<id>` — immutable ревизия (полный JSON)
- `operations` и `audit_events` — отдельные таблицы, не kv

`data-model.md` остаётся aspirational; `001.sql` — фактическая реализация.
Расхождение carries risk: schema-level FK и indexed range queries на полях
клиентов/политик недоступны, валидация лежит на приложении.

## Решение

Зафиксировать kv-blob MVP как сознательный компромисс первого среза:

- `draft` и `revision:*` — opaque JSON в `kv`, целостность на уровне приложения.
- `operations` — отдельная таблица с индексом на `json_extract(body,'$.revision_id')`.
- `audit_events` — отдельная таблица (см. ADR 0009).
- `sessions` — без FK на `users`, потому что анонимные CSRF-holder сессии
  используют `username=''`, а FK на `users(username)` нарушил бы
  unauthenticated `GET /session` flow.

Валидация ссылочной целостности клиентов/политик/TLS-профилей выполняется
в `policy.Validate`. Валидация optimistic-lock `expected_version` для draft —
в `store.Change`. Валидация immutable-ревизии — `rev.Compiler == policy.CompilerVersion`
и `rev.Image == s.Image` в `createOperation`.

## Альтернативы

- **Полная реляционная схема сразу**: больше кода, миграций, индексов; не
  нужно для MVP where 100 clients + 100 revisions are well within SQLite range.
- **Промежуточный: таблица revisions + JSON blob внутри**: не даёт выгоды
  реляционной модели; усложняет выборки. Текущий `kv`-with-prefix
  эквивалентен по эффективности через `key LIKE 'revision:%' ORDER BY rowid`.

## Последствия и проверка

- `store.Prune` находит reference-orphaned revisions одним запросом
  `key LIKE 'revision:%' AND key NOT IN (SELECT ... FROM operations WHERE
  json_extract(body,'$.status')='succeeded')`. Работает с 001.sql.
- Schema-migrations framework (см. `migrations/002.sql`) умеет ALTER
  между схемами через rebuild-+-rename; переход на реляционную схему
  потребует миграции 003+.
- `data-model.md` обновляется при переходе к реляционной схеме. До тех
  пор файл помечает kv-blob как фактическую реализацию MVP.

## Замена

Не планируется до второй фазы, где клиентов > 1000 и range-queries по
полям политик станут нужны для аналитики.

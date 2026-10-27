# ADR 0009: single-process concurrency assumption

Дата: 2026-10-27. Статус: Accepted (в сочетании с ADR 0006).

## Контекст

ADR 0006 фиксирует один контейнер с одним Go-бинарником и одним Squid.
Внутри этого бинарника управление параллелизмом построено на трёх механизмах:

- `sync.Mutex` (`store.Store.draftMu`) защищает optimistic-lock update draft.
- `sync.Mutex` (`Server.opsMu`) защищает create-operation intent и live worker.
- `database/sql` `SetMaxOpenConns(1)` превращает pool в single writer, поэтому
  гости из разных goroutines сериализуются на уровне драйвера SQLite.
- `syscall.Flock` в `runtime.Manager.lock` защищает filesystem staging и
  LKG-перезапись от параллельных процессов.

Все три mutex'а безопасны только в одном OS-процессе. Если запустить второй
экземпляр бинарника (даже с тем же `--database`), optimistic-lock станет
гонкой: оба прочтут `version=5`, оба запишут `version=6`, последний выиграет,
первый потеряет изменения. `Flock` ловит файловую часть, но SQLite-транзакции
remain separate.

## Решение

Зафиксировать как сознательное ограничение: **один executable, один процесс
управления**. Это соответствует ADR 0006 (один контейнер, Pebble стартует
ровно один `squid-control serve`). Оператор не должен запускать второй
экземпляр pointing at the same database.

`Server.persistenceFailed` atomic.Bool и recovery gate также assume
single-process: флаг устанавливается в одном процессе и не должен
наблюдаться другими.

## Альтернативы

- **PostgreSQL / MySQL с proper row-level locking**: серьёзная инфраструктурная
  зависимость, не обоснована для single-node MVP.
- **Совместная блокировка через отдельный advisory-lock файл в filesystem**:
  усложняет recovery и не даёт real concurrency benefit, пока всё равно
  single writer.
- **Распределённый lock manager (etcd, consul)**: violates ADR 0006 single-node
  assumption.

## Последствия и проверка

- Multi-replica horizontal scaling панели требует нового ADR, замены mutex'ов
  на SELECT FOR UPDATE (или advisory locks), rethink `persistenceFailed`
  distribution и cross-process idempotency-key lookup.
- Compose-конфигурация `deploy/compose.yaml` намеренно не имеет `replicas: N`
  или multi-host deployment; `restart: unless-stopped` создаёт ровно один
  контейнер.
- Migration 001.sql+ прикладывает `schema_migrations` через INSERT OR IGNORE,
  что безопасно только при single writer.

## Замена

Не планируется. Multi-instance workloads выходят за рамки продукта;
администратору, нуждающемуся в HA, предлагается внешний reverse proxy,
_fronting_ единственной панелью, а не дублирование панели.

## Уточнение приоритета 27.10.2026

[Пересмотренный roadmap](../plans/roadmap.md) не включает HA. Внешний reverse
proxy сам по себе не устраняет отказ единственной панели/Squid/узла и не является
решением HA. Если такая потребность появится, потребуется отдельное решение.

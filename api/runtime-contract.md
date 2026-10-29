# Внутренний контракт runtime module

Модуль находится внутри squid-control. Это Go-интерфейсы, не HTTP/Unix API,
не отдельный сервис и не изоляция от кода панели. Отдельный runner исключён
[ADR 0006](../docs/adr/0006-single-container.md).

## Операции

| Операция | Результат |
| --- | --- |
| Capabilities/Observe | Build/process capabilities, config/LKG hashes и health |
| Validate | Проверка typed snapshot, compiler и локальный squid parse |
| Apply | Stage, promote, reload, verify, LKG или rollback |
| Rollback | Применение выбранной рабочей ревизии с проверкой |
| Recover | Согласование journal/files до mutations и перед стартом Squid |
| ReadEvents | Bounded tail известных logs по opaque cursor |
| CertificateMetadata | Только public metadata установленного inventory |

Вход Apply: operation ID, immutable snapshot, policy hash, compiler/capability
versions, expected applied revision и точная successful validation. Idempotency
публичного API хранится в SQLite; fs journal связывается тем же operation ID.
Request context браузера не владеет lifetime durable operation.

Все данные повторно проверяются на уровне use case/module, даже после HTTP
validation. Cert references выбираются из локально установленного inventory.
Нет arbitrary executable/path/argv/image/URL или private key из запроса.
По ADR 0007 snapshot может содержать ограниченную expert section: compiler
валидирует её, добавляет управляемое окружение и защитные ACL. Это не замена
целого runtime config и не произвольное выполнение команд.

## Локальный Squid adapter

Фиксированные binary/config/PID paths, argv без shell, deadlines и bounded output.
Parse выполняется бинарником Squid из того же итогового image, в том же контейнере,
на candidate bundle с изолированными writable runtime paths. Проверить, что parse
не пишет live PID/cache/log; при невозможности validation отклоняется.
Reload и rotate — manager-запрос с токеном на `127.0.0.1:3131`, не сигнал и не Pebble PID 1. Docker API и запуск соседних контейнеров нет.

## Journal, lock и startup gate

Filesystem journal хранит stage, предыдущий/candidate bundle, hashes, secret refs
и terminal outcome. SQLite хранит пользовательскую операцию и audit. Это один
процесс приложения, но DB/files/Squid не образуют общую транзакцию.

Межпроцессный file lock нужен также короткому recovery подрежиму **того же**
executable, запускаемому перед каждым стартом Squid. Gate не требует доступной
web-сессии или HTTP сервера. При незавершённой операции восстанавливается LKG;
если recovery нельзя доказать, неизвестный candidate не запускается.
После restart web worker согласует DB с journal до новых mutations.

## Ошибки и наблюдаемость

Stable codes: invalid_policy, unsupported_capability, busy, revision_conflict,
missing_secret, parse_failed, reload_failed, verification_failed, rollback_failed,
drift_detected, storage_unavailable. Safe findings не содержат ключи/stack traces.
Отказ `squid -k parse` возвращается как `*runtime.ParseError{Line}` — номер строки
итогового конфига без вывода Squid; HTTP API переводит его в строку экспертной секции,
если ошибка внутри неё.
Status различает доступность runtime module, процесс Squid, проверенную ревизию
и внешний ресурс. Невозможность observe означает unknown, а не доказанный down.
См. [lifecycle](../docs/architecture/configuration-lifecycle.md).

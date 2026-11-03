---
slug: how-draft-apply
title: Черновик, применение и откат
section: Как это работает
order: 30
---

Конфигурация проходит три состояния. Они показываются раздельно: факт
сохранения формы не означает применения, а exit code reload не доказывает
готовность Squid.

## Три состояния

- **Desired** — сохранённый черновик в БД панели.
- **Applied** — ревизия, для которой runtime завершил применение и проверки.
- **Observed** — реально наблюдаемые process health, checksum и probes.

Черновик не влияет на трафик. Preview показывает semantic и config diff
сохранённого черновика против действующей ревизии, без создания новой
ревизии. Нет изменений — применения нет.

## Ревизия

Revision — immutable bundle: canonical policy JSON, версия компилятора,
версия схемы, digest образа, capabilities profile, рендер конфигурации,
content hashes и ссылки на версии секретов без самих ключей. Preview и
apply используют один чистый компилятор внутри Go-приложения. HTTP/TLS
listener-порты принадлежат установке; draft и операции с другими портами
отклоняются `422 installation_setting`.

## Применение

1. Проверка schema, source ranges, доменов, TLS-профиля, expected
   revision, свободного места и наличия секретных ссылок.
2. Компиляция immutable policy, сверка hash с preview.
3. Запись candidate bundle в staging с fsync; журнал — `staged`.
4. Parse всего bundle локальным Squid из того же образа и контейнера,
   фиксированные argv/paths, без shell и Docker API.
5. Semantic validation: неизменность защитных правил, отсутствие пустого
   allow-all, соответствие политики и ACL.
6. Атомарная замена активного конфига через rename в смонтированном
   каталоге, fsync dir; журнал — `promoting`.
7. Reload проверенной командой без shell; журнал — `reloading`.
8. Verify: process health, ошибки reload, fingerprint и поведенческие
   probes; журнал — `verifying`.
9. При успехе атомарное обновление LKG, fsync, durable `succeeded` и
   audit-запись.

При ошибке после замены — `rolling_back`: вернуть прежний bundle,
повторно parse/reload/verify, завершиться `rolled_back` или `degraded`.

## Статусы операции

Публичные: `pending`, `validating`, `validated`, `reloading`,
`succeeded`, `rejected`, `rolled_back`, `degraded`.

- `rejected` — активная конфигурация не менялась;
- `rolled_back` — прежняя конфигурация восстановлена и проверена;
- `degraded` — итоговое состояние не доказано; новые изменения блокируются
  до recovery.

Отдельная операция validate завершается `validated` или `rejected`, без
promotion. Apply требует предварительной проверки этой ревизии.

## Идемпотентность и конкурентность

Apply передаёт immutable snapshot, expected applied revision и
idempotency key. Одинаковый ключ с тем же payload возвращает прежний
operation ID; с другим payload — `409`. Runtime держит один межпроцессный
file lock для validate/promote/reload/rollback; новый apply при занятом
lock — `busy`, скрытой очереди нет.

## Recovery

Recovery gate перед каждым стартом Squid читает durable journal:
первоначальный старт, перезапуск контейнера или хоста, отдельный restart через
Pebble. Незавершённый apply восстанавливает recorded previous manifest.
Нет рабочей ревизии — старт deny-all, не allow-all. Drift от ручного
изменения обнаруживается по hash; модуль не перезаписывает неожиданный
файл молча. Ручной rollback создаёт новую операцию —
см. [Откат](help:scenario-rollback).

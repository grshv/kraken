# План реализации

D0 завершён. D1/D2/D3/D4/D6 выполнены частично первым рабочим срезом;
D5 проверен для TLS proxy; HTTPS сайтов передаётся сквозным CONNECT.
Факты и ограничения: [capability report](../research/image-compatibility.md).
Полный этап не считается завершённым по одному happy-path тесту.

## D0 — основание (подготовлено)

Git main, требования, архитектура, политика клиентов, TLS-профили, интерфейс,
security/operations, контракты, ADR, шаблоны и работающая docs-проверка.
Это не означает готовность proxy manager к запуску.

## D1 — проверка образа и минимальный data plane

Выполнить [image-compatibility](image-compatibility.md), выбрать digest,
подтвердить Pebble/paths/UID и deny-all/allowlist на реальных source identities.
Проверить TLS proxy и HTTPS CONNECT; составить capability matrix.
Результат: воспроизводимый прототип и manifest, а не список предположений.

## D2 — скелет и вертикальный срез

Один Go-бинарник со static UI, typed policy/compiler, SQLite/migrations,
внутренний runtime worker/journal и локальный Squid adapter,
bootstrap CLI, web HTTPS/session/CSRF, первая политика клиента. UI может быть минимален,
но полный путь draft → validate → apply → request должен реально работать.
Результат: наблюдаемый доступ одной тестовой identity и отказ неизвестной.

## D3 — надёжное применение

Сериализация, optimistic concurrency, idempotency, promotion, LKG, rollback,
reconciliation, drift и recovery gate перед каждым стартом Squid. Проверить
crash между DB/journal/Squid, Pebble restart Squid и reboot контейнера/VM.
Restart только Go должен сохранять работающий Squid и открытый CONNECT.
Результат: доказанные AC-04/05/06/07, а не только happy path.

## D4 — полноценная панель и диагностика

UI по interface spec, клиентские режимы, domain lists, events/log cursors,
ошибки/stale/empty, audit, client instructions, certificate metadata.
Результат: администратор объясняет отказ без SSH и ручного чтения squid.conf.
Точные исторические ACL-причины показывать лишь при наличии источника.

## D5 — TLS-подключение к прокси

Проверить client compatibility, доверие к серверному сертификату и ротацию.
HTTPS сайтов остаётся сквозным CONNECT с исходным сертификатом сайта.
Результат: AC-11/12 и TLS matrix для панели, proxy transport и HTTPS origin.

## D6 — поставка и эксплуатация

Multi-stage image, single-service Compose, Pebble plan, firewall matrix,
pinned release, scans, backup/restore drill,
реальные runbook-команды, обновление/откат и acceptance suite.
Результат: установка на чистую VM и восстановление после её потери проверены.

## После первой версии

Рассмотреть общий package cache, internal-resource exceptions, proxy auth/mTLS,
OIDC/read-only roles, push alerts, bandwidth limits, HA. Каждая опция должна
иметь пользовательскую задачу и критерий пользы; не закладывать пустые сервисы.

## Первая задача разработки

D1 можно начинать независимо от выбора UI. Не тратить время на красивую панель,
пока не проверены образ, ACL semantics и TLS capabilities выбранного Squid.
При изменении scope обновить requirements и ADR перед параллельной реализацией.

## Реализованное расширение 27.10.2026

Добавлены общие клиенты (host/network/group), визуальные настройки и защищённая
экспертная секция, история/diff/rollback в UI, отдельный probe listener,
PHC Argon2id и локальный password-reset с отзывом сессий. D3 дополнен ENOSPC
injection, SIGKILL тестового процесса на durable границах и восстановлением
после отмены forward operation. Ошибка сохранения terminal outcome блокирует
следующие изменения. [План с критериями](active/client-settings-and-recovery.md).

Оставшиеся D3/D4/D6: полная crash/reboot/storage matrix, события с retention/cursor,
управление сертификатами, backup/restore drill, security scans и release packaging.

## Повторное ревью 27.10.2026

Исправлены session capacity deadlock, anonymous login pool, first_run после LKG,
неподтверждённый rollback, неатомарная миграция, повторный вход после expiry и
container panel bind. [Проверки](active/review-reliability.md) завершены.
Следующий срез D4: история событий с курсорами и retention, фильтр по клиенту,
аудит в панели и явные stale/unavailable состояния. Затем D6 backup/restore drill
и release gates; эти этапы исправлением регрессий не считаются завершёнными.

## История и аудит 27.10.2026

D4 расширен сохранённой историей запросов, client/failure filters, cursor,
bounded retention, stale/unavailable/gap состояниями и audit API/UI.
[Результаты проверок](active/diagnostics-history.md) подтверждены unit/race,
real-image и browser suite. D4 остаётся частичным: time/destination search,
агрегаты и расширенные client instructions развиваются отдельно.
Следующий срез D6: согласованный backup/restore DB, runtime LKG/journal и
серверных сертификатов; восстановление в чистом fixture с реальным proxy request.
Затем сертификатная ротация, оставшаяся fault matrix и release/security gates.

# План реализации

D0 завершён. D1/D2/D3/D4/D6 выполнены частично первым рабочим срезом;
D5 проверен для TLS proxy, SSL Bump unsupported в текущей базе.
Факты и ограничения: [capability report](../research/image-compatibility.md).
Полный этап не считается завершённым по одному happy-path тесту.

## D0 — основание (подготовлено)

Git main, требования, архитектура, политика по VM, TLS-профили, интерфейс,
security/operations, контракты, ADR, шаблоны и работающая docs-проверка.
Это не означает готовность proxy manager к запуску.

## D1 — проверка образа и минимальный data plane

Выполнить [image-compatibility](image-compatibility.md), выбрать digest,
подтвердить Pebble/paths/UID и deny-all/allowlist на реальных source identities.
Проверить TLS proxy и SSL Bump раздельно и в комбинации; составить capability matrix.
Результат: воспроизводимый прототип и manifest, а не список предположений.

## D2 — скелет и вертикальный срез

Один Go-бинарник со static UI, typed policy/compiler, SQLite/migrations,
внутренний runtime worker/journal и локальный Squid adapter,
bootstrap CLI, web HTTPS/session/CSRF, первая VM policy. UI может быть минимален,
но полный путь draft → validate → apply → request должен реально работать.
Результат: наблюдаемый доступ одной тестовой identity и отказ неизвестной.

## D3 — надёжное применение

Сериализация, optimistic concurrency, idempotency, promotion, LKG, rollback,
reconciliation, drift и recovery gate перед каждым стартом Squid. Проверить
crash между DB/journal/Squid, Pebble restart Squid и reboot контейнера/VM.
Restart только Go должен сохранять работающий Squid и открытый CONNECT.
Результат: доказанные AC-04/05/06/07, а не только happy path.

## D4 — полноценная панель и диагностика

UI по interface spec, per-VM modes, domain lists, events/log cursors,
ошибки/stale/empty, audit, client instructions, certificate metadata.
Результат: администратор объясняет отказ без SSH и ручного чтения squid.conf.
Точные исторические ACL-причины показывать лишь при наличии источника.

## D5 — транспорт TLS и управляемое inspection

TLS к proxy включить после client compatibility. SSL Bump: provisioning issuer,
trust instructions, выбор VM, bypass/splice, cert DB, upstream validation,
CA rotation, privacy limits. Если transport TLS уже реализован в D2/D4,
не дублировать работу — закрыть оставшуюся matrix.
Результат: AC-11/12 и тесты из TLS matrix. Не обещать inspect до capability gate.
Включение SSL Bump в первую поставку зависит от решения владельца о сроке/приоритете.

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

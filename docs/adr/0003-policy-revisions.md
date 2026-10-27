# ADR 0003: typed policy и ревизии

Дата: 2026-10-27. Статус: Proposed.

## Контекст

VM нужны разные режимы доступа. Raw editor squid.conf допускает injection,
обход защитных правил и не даёт предсказуемый preview/rollback.

## Решение

Typed model с per-source client, all_web/allowlist и TLS profile. Canonical snapshot
компилируется чистым модулем внутри Go-приложения. Immutable bundles,
локальный parse Squid из того же image, semantic guards, durable operation journal, LKG и verification.
Draft не применяется автоматически, overlaps source networks запрещены.

## Альтернативы

Прямое редактирование config — гибко, но UI не может гарантировать инварианты.
Только шаблон + restart — просто, но повреждает доступность и плохо переживает crash.
Сохранение generated config как единственного источника лишает semantic diff.

## Последствия

Поддерживается только явно смоделированный subset Squid. Нужна дисциплина compiler
versions/capabilities и recovery. Атомарности между DB/fs/process нет, есть протокол
согласования и проверки. Тесты должны проверять доступ, а не один exit code.

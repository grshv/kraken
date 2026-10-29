# ADR 0001: один узел и модульный control plane

Дата: 2026-10-27. Статус: Accepted. Подтверждено решениями владельца об одном узле
и [ADR 0006](0006-single-container.md).

## Контекст

Прокси размещается на одной VM, основной сценарий — управлять доступом клиентов.
Панель не должна становиться частью пути скачивания пакетов.

## Решение

Go backend с встроенным статическим React/TS UI, SQLite на local disk;
Упаковка уточнена принятым [ADR 0006](0006-single-container.md): один image и
контейнер, Pebble, отдельные Squid и Go со static UI и внутренним runtime module.
Без Redis/PostgreSQL/Kubernetes; host runner и systemd приложения не нужны.

## Альтернативы

Python/FastAPI упрощает CRUD при опыте Python, но добавляет Python runtime в итоговый image.
Go templates сокращают frontend toolchain, но сложный diff и event view потребуют JS.
Полноценный JS framework с SSR не нужен закрытой admin-панели.
Microservices увеличивают deployment/consistency стоимость без подтверждённой нужды.

## Последствия

Простая локальная установка и backup; один узел остаётся SPOF.
SQLite подходит одной инсталляции, а не распределённому multi-writer control plane.
Frontend build dependencies всё равно требуют lock/scan даже без Node в runtime.
Проверка: чистая установка, отказ web не ломает трафик, backup/restore упражнение.

# ADR 0004: TLS transport и SSL Bump отдельно

Дата: 2026-10-27. Статус: Proposed.

## Контекст

Владелец просит предусмотреть оба варианта. Сертификат панели, серверный сертификат
proxy и issuer для inspection решают разные задачи и имеют разные риски.

## Решение

HTTPS панели обязателен. VM → proxy TLS — целевой transport после compatibility.
Для трафика — tunnel по умолчанию, inspect явно для выбранных VM, с bypass/splice.
CA provisioning локальный, web видит metadata/public chain и references, без keys.
Image capability gate проверяет каждую функцию и их комбинацию.

## Альтернативы

Только CONNECT сокращает сложность, но не покрывает будущую inspection потребность.
Bump всех VM автоматически ломает pinning/mTLS и расширяет зону доверия CA.
TLS termination proxy вне Squid возможен как отдельное решение, если образ не умеет
нужный transport; он не создаёт inspection и требует собственного threat review.

## Последствия

Есть trust rollout, ротация issuer, writable cert DB и privacy limits логирования.
Кеш после decrypt всё равно подчиняется HTTP semantics. Не обещать поддержку
текущего Canonical image до тестов; изменение image требует явного решения.

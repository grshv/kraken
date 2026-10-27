# ADR 0002: ограниченный runner

Дата: 2026-10-27. Статус: **Superseded**.

Историческое предложение, отклонено в пользу [ADR 0006](0006-single-container.md).
Не является текущей инструкцией установки или архитектурным ограничением.

## Контекст

Панель должна менять конфиг и просить Squid перечитать его.
Прямой Docker socket в web даёт слишком широкие полномочия при web exploit.

## Решение

Локальный root-owned Go runner под systemd. Web обращается через Unix socket,
с ограниченными правами и типизированным protocol. Runner сам валидирует/render
policy, принимает только известные действия и использует установленный target image
и container identity. Docker socket в web отсутствует.

## Альтернативы

Web с Docker socket — проще первый prototype, но хуже граница доверия.
Docker socket proxy ограничивает endpoints, однако exec/create по-прежнему опасны.
Docker-less sidecar с shared PID/namespace требует отдельного анализа полномочий.
SSH от web добавляет credentials и удалённый shell вместо узкого протокола.

## Последствия

Появляется второй artifact, versioned IPC, systemd и журнал восстановления.
Runner остаётся высокопривилегированным: компрометация даёт риск host, а не
«безопасный sandbox». Нужны audit, строгий input и отсутствие универсального exec.
Проверка: web exploit surface review, Unix permissions, injection и fault tests.

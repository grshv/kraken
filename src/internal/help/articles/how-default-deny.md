---
slug: how-default-deny
title: Закрыто по умолчанию и защитные запреты
section: Как это работает
order: 10
screen: settings
---

Доступ закрыт по умолчанию. Squid применяет защитные запреты до
пользовательских правил, и они не переопределяются клиентом, политикой
`all_web` или экспертной секцией.

## Порядок проверки запроса

1. **Опасные порты.** Запрещены unsafe ports; CONNECT разрешён только на
   утверждённые TLS-порты. На старте это 443, обычный web — 80/443.
2. **Cache manager.** Закрыт от клиентов прокси и публичной сети.
3. **Защищённые назначения.** Запрещены loopback, link-local/cloud
   metadata, private/management destinations, IPv6 ULA и специальные
   диапазоны (6to4, Teredo, NAT64, IPv4-mapped). Эти запреты действуют
   всегда.
4. **Диагностическая identity.** Узкая локальная identity разрешена
   только фиксированным probes; широкая bridge-подсеть «для удобства
   healthcheck» не разрешается.
5. **Клиентская политика.** Применяется Enabled source client и его
   режим/назначения: `all_web` или allowlist.
6. **Явный deny all.** Проверка завершается `http_access deny all`.

## Опасные порты

`safe_ports` проходит через дополнительный denylist, не зависящий от
выбора: ssh (22), telnet (23), smtp (25), pop3 (110), imap (143),
smtps (465), submission (587), imaps (993), pop3s (995), tor (9050,
9051). `all_web` не отменяет этих запретов. `connect_ports` — подмножество
`safe_ports` и наследует запреты.

## Запрещено для всех

`blocked_domains` — список доменов, закрытых для всех клиентов. Эти
`http_access deny` рендерятся сразу после защитных запретов, до
пользовательского блока, поэтому действуют и для `all_web`, и в expert
mode. Это настройка только администратора.

## Private destinations

По умолчанию private destinations закрыты. Включение
`allow_private_destinations` открывает внутренние ресурсы, но loopback,
link-local и metadata остаются защищены. IPv4-mapped аналоги
(`::ffff:10.0.0.5`) остаются заблокированными: это safe-side против
DNS-rebinding через mapped-адреса.

## Expert mode

Expert mode заменяет визуальный ACL-блок пользовательскими директивами.
Защитные `http_access deny` рендерятся до экспертной секции и не
переопределяются. Экспертная секция принимает только директивы из
allowlist; файлы-параметры, helpers, listeners, пути и `sc_` имена
закрыты. См. [Черновик, применение и откат](help:how-draft-apply).

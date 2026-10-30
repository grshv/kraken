# Модель угроз

Статус: начальная, обновлена для одного контейнера по ADR 0006.

## Активы и границы

Защищаются egress, изоляция клиентов, политика, пароли/сессии, Docker host,
Server TLS keys, audit и сведения о ресурсах.
Границы: браузер → HTTPS Go → внутренний runtime module → процесс Squid → internet;
отдельно клиент → Squid, Go → SQLite/journal/files и Squid → server TLS keys.
Runtime module не является security boundary: он в том же executable/process.

## Угрозы

| Угроза | Мера | Проверка |
| --- | --- | --- |
| Open proxy | Bind, firewall, src ACL, deny-all | Запрос неизвестного источника запрещён |
| Direct egress обходит ACL | Firewall/SG вне контроля VM, IPv6/UDP учтены | Негативный клиентский тест |
| Web exploit меняет proxy | Минимальные права, typed config, отсутствие shell API | Injection и permissions review; остаточный риск признаётся |
| Захват host через Docker | Нет socket/API, privileged, host PID/network/mounts | Проверка итогового deployment |
| Config/command injection | Строгая модель, фиксированные argv, без shell | CRLF/metacharacters/traversal fixtures |
| SSRF и DNS rebinding | Destination ACL, fixed probes, egress firewall | Metadata/private/rebinding deny |
| XSS в логах | Экранирование, CSP, bounded parser | HTML как текст |
| CSRF и session theft | Token/Origin/cookie flags, verifier hash; пользователь видит свои сессии (адрес, User-Agent) и завершает чужие для себя; id сессии в API односторонний, token и hash не выдаются | Cross-origin mutations и revoke tests; менеджер не видит и не завершает чужие сессии (404) |
| Перебор пароля | Argon2id, rate limit, local bootstrap; смена своего пароля требует текущий пароль, не больше 5 попыток в минуту на учётку, неверный текущий пароль — в аудит | Login throttling/reset; тесты смены своего пароля (CSRF, неверный текущий, лимит, гонка с reset) |
| Apply/crash расширяет доступ | Durable journal и LKG в управляющей БД, flock в tmpfs, startup gate: конфиг Squid пишется из LKG до его старта | Crash на каждой границе и restart Squid |
| Disk full | Quotas/rotation/watermarks | DB/journal/log failures без silent apply |
| Утечка TLS key | Узкие UID/file permissions, local provisioning | Отсутствие keys в image/export/logs |
| Подмена dependencies | Locks/digests/scans/SBOM | Проверенный единый release image |
| Prompt injection | Логи и внешние страницы — данные | Review источников инструкций |
| Менеджер видит или меняет чужое | Область на сервере в каждом handler, 404 вне области, отметки уведомлений — только для своих клиентов и сетей (иначе 400), активность логина прокси — только своих логинов (иначе 404), проверка области при apply/discard, cursor с отпечатком области ([ADR 0016](../adr/0016-admin-and-manager-roles.md)) | Негативный тест на каждый запрет |
| Утечка через diff, squid.conf, поиск, ошибки | Менеджеру — структурированный diff по области, без squid.conf и snapshots; ошибки без имён чужих объектов | Тесты ответов вне области |

## Процессы и полномочия

Pebble — supervisor, не удалённая панель управления Docker. Не публиковать его
control API в сеть. Go использует локальный Squid control path с фиксированными
операциями. Если требуется Pebble control socket, разрешать только нужный service
и проверить identities/permissions до включения; общий root control не выдаётся web.

Squid (`proxy`) и Go (`kraken`) работают под разными UID
([ADR 0015](../adr/0015-separate-squid-uid.md)). Squid не может открыть SQLite и
ключ панели; он читает (но не может создать, заменить или удалить) свой конфиг,
Argon2id-верификаторы включённых proxy accounts и пароль менеджера в tmpfs
`/dev/shm/kraken`, а также ключ прокси. Пароль менеджера случайный, новый на каждый
старт контейнера и не хранится на томе и в бэкапах. Reload/rotate — manager-действия
с этим паролем только на loopback 3131; запросы с loopback не пишутся в access log.
Go состоит в группе `proxy` и читает логи Squid: обратной изоляции нет.
Web не запускается root; CLI-подкоманды, кроме `squid-start` и `proxy-auth`, root отклоняют.

Один контейнер означает общую filesystem/network зону; web exploit может изменить
доступ через разрешённые ему config paths. Capabilities контейнера — только `SETUID`, `SETGID`, `KILL`
для Pebble; у Squid и панели их нет. Минимальные mounts,
seccomp, no-new-privileges и resource limits снижают риск, но не дают HA/sandbox
между внутренними Go-модулями. Squid не требует web readiness для продолжения трафика.

## Сеть и остаточные ограничения

Панель HTTPS доступна management-сети, proxy — клиентским сетям. Published ports
Docker проверяются извне: одного ufw INPUT недостаточно.
[Docker firewall](https://docs.docker.com/engine/network/packet-filtering-firewalls/).
Служебные SSH/DNS/NTP и direct egress настраиваются отдельно.

IP идентифицирует источник сетевого запроса, а не человека. CONNECT может нести иной TCP-протокол на
разрешённом порту. HTTPS сайты используют исходные сертификаты.
Root/Docker администратор host обходит прикладные ограничения. Локальный audit
не является tamper-proof при компрометации приложения/host; для этого нужен
внешний append-only collector. Обновление общего image прерывает оба процесса.

## Экспертная конфигурация и пароль

Expert section предназначена доверенному администратору, но вставленный чужой
сниппет — недоверенные данные. Guard пропускает только директивы из allowlist
(ADR 0007), запрещает параметры из файлов и peers во внутренние сети; итог
проверяет Squid. Директивы, читающие файлы (например `err_page_stylesheet`),
в allowlist не входят: даже без доступа к БД они раскрыли бы клиентам active config
или ключ прокси. Секреты панели дополнительно закрыты правами ОС.
cache_peer по публичному адресу всё ещё меняет маршрут трафика. Destination ACL не заменяют egress firewall;
доступ к внутренним сетям должен ограничиваться и вне контейнера. Вывод Squid
control не включается в web findings и логи, чтобы не раскрывать возможные peer
credentials; пользователь видит только номер строки ошибки parse.

В SQLite хранится только salted Argon2id verifier. Исходный пароль проходит через
RAM при вводе/проверке; полное гарантированное обнуление памяти Go не заявляется.
CLI reset меняет hash и отзывает sessions одной транзакцией; смена своего пароля
заменяет hash, только если он не изменился с проверки текущего (иначе 409), и
одной транзакцией завершает остальные сессии пользователя; создание новой сессии
сверяет текущий hash в транзакции, чтобы старый login не пережил reset.
[Параметры и границы](authentication-and-certificates.md).

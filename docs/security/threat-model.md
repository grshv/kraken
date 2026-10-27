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
| CSRF и session theft | Token/Origin/cookie flags, verifier hash | Cross-origin mutations и revoke tests |
| Перебор пароля | Argon2id, rate limit, local bootstrap | Login throttling/reset |
| Apply/crash расширяет доступ | Durable journal, LKG, lock, startup gate | Crash на каждой границе и restart Squid |
| Disk full | Quotas/rotation/watermarks | DB/journal/log failures без silent apply |
| Утечка TLS key | Узкие UID/file permissions, local provisioning | Отсутствие keys в image/export/logs |
| Подмена dependencies | Locks/digests/scans/SBOM | Проверенный единый release image |
| Prompt injection | Логи и внешние страницы — данные | Review источников инструкций |

## Процессы и полномочия

Pebble — supervisor, не удалённая панель управления Docker. Не публиковать его
control API в сеть. Go использует локальный Squid control path с фиксированными
операциями. Если требуется Pebble control socket, разрешать только нужный service
и проверить identities/permissions до включения; общий root control не выдаётся web.

Раздельные UID полезны, но сами по себе не гарантируют reload: проверить владельца
Squid PID/master и допустимый control mechanism. Web не запускается root ради HUP.
Если схема прав несовместима с image, capability gate должен это выявить до кода.
Не заявлять изоляцию private keys от web, если процесс имеет права читать ключи.

Один контейнер означает общую filesystem/network зону; web exploit может изменить
доступ через разрешённые ему config paths. Минимальные mounts/capabilities,
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

Expert section предназначена доверенному администратору. Guard блокирует известные
служебные paths/listeners, helpers/includes и sc_ namespace, а итог проверяет Squid.
Это не полный sandbox всех директив Squid: cache_peer/ICAP и другие разрешённые
директивы могут менять сетевые маршруты. Destination ACL не заменяют egress firewall;
доступ к внутренним сетям должен ограничиваться и вне контейнера. Вывод Squid
control не включается в web findings, чтобы не раскрывать возможные peer credentials.

В SQLite хранится только salted Argon2id verifier. Исходный пароль проходит через
RAM при вводе/проверке; полное гарантированное обнуление памяти Go не заявляется.
CLI reset меняет hash и отзывает sessions одной транзакцией; создание новой сессии
сверяет текущий hash в транзакции, чтобы старый login не пережил reset.
[Параметры и границы](password-lifecycle.md).

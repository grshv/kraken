# Политики клиентов и TLS

## Идентичность клиента

Базовая идентичность — source IP, который **видит Squid**. Запись клиента содержит
ID, понятное имя, описание, IP/CIDR, enabled и policy. Обычно используется точный
адрес `/32` или `/128`. Для сетевой группы допустим CIDR, но это уже доверие всем
адресам группы, что UI должен объяснять.

Пересечения source ranges между включёнными клиентами запрещены: правило одного
клиента не должно случайно расширять доступ другого. Валидатор использует parsed IP
networks, а не сравнение строк. `/0` и неопределённые адреса недопустимы.
NAT, скрывающий всех клиентов за одним IP, делает такие политики неразличимыми.
Решение — сохранить исходные адреса либо отдельно проектировать proxy auth.
Общий серверный сертификат HTTPS-прокси не идентифицирует отдельное устройство.

## Модель политики

```text
Client: id, name, kind(host|network|group), source_networks[], enabled, policy_id
Policy: id, name, mode(all_web|allowlist), destinations[], tls_profile_id
Destination: kind(domain), value, include_subdomains
TLSProfile: mode(tunnel), proxy_transport(http|tls), cert_ref
```

- `all_web`: внешние HTTP/HTTPS назначения на разрешённых web-портах.
- `allowlist`: только нормализованные домены и явно выбранные поддомены.
- Общие защитные ограничения применяются к обоим режимам.
- Первая версия не предлагает `*.example.org` как произвольный glob: UI хранит
  `example.org` и отдельный флаг поддоменов, compiler переводит его в ACL.
- Запрещены CR/LF, пробелы/директивы, схемы URL, userinfo и произвольные regex.
  В текущем срезе принимаются canonical ASCII domains; IDN normalization ещё не реализован.
- В allowlist raw IP назначения по умолчанию не разрешается. PTR lookup не должен
  превратить чужой IP в разрешённый домен; точное поведение проверяется fixture.
- Пустой allowlist означает отсутствие разрешённых ресурсов, а не весь интернет.

## Порядок ACL

1. Запретить unsafe ports; CONNECT разрешать только на утверждённые TLS-порты.
   На старте это 443, обычный web — 80/443.
2. Закрыть cache manager от клиентов прокси и публичной сети.
3. Запретить loopback, link-local/cloud metadata, private/management destinations,
   IPv6 ULA и соответствующие специальные диапазоны согласно политике сети.
4. Разрешить узкую локальную diagnostic identity только для фиксированных probes;
   не разрешать широкую bridge subnet «для удобства healthcheck».
5. Применить enabled source client и его режим/назначения.
6. Завершить явным `http_access deny all`.

Список специальных диапазонов, IPv4-mapped IPv6, DNS rebinding и порядок
разрешения адресов проверяются на выбранной версии. Firewall egress на proxy VM
дублирует запрет доступа к metadata/management, с отдельным разрешением нужного DNS.
Нельзя считать текстовый domain allowlist полноценной защитой от DNS rebinding.
Доступ к внутренним репозиториям разрешается явно через настройки и firewall;
loopback/link-local/metadata остаются закрыты.

### Опасные порты в `safe_ports`

`safe_ports` администратора проходит через дополнительный denylist, не зависящий
от выбора: ssh (22), telnet (23), smtp (25), pop3 (110), imap (143), smtps (465),
submission (587), imaps (993), pop3s (995), tor (9050, 9051). `all_web` не
отменяет этих запретов. Связные `connect_ports` являются подмножеством `safe_ports`
и наследуют запреты.

### Служебные и условно-служебные listener-порты

| Порт | Владелец | Поведение |
| --- | --- | --- |
| 3130 | приложение | Loopback-only diagnostic listener, всегда эмитируется `policy.Render` как `http_port 127.0.0.1:3130`. Запрещён в `validateSettings` для HTTPPort/TLSPort. Verify-проба всегда идёт на 3130. |
| 8443 | приложение | Panel listener. Запрещён в `validateSettings` для HTTPPort/TLSPort. |
| 3128, 3129 | установка | Конвенциональные Squid listeners HTTP/TLS. Порты заданы упаковкой; web/API не могут менять их ни через draft, ни через старую revision/rollback. Уже установленные другие значения сохраняются. `DefaultSettings` возвращает 3128/3129, verify использует только независимый 3130. Порты назначения/CONNECT остаются настройками политики. |

### IPv4-mapped IPv6 и приватные destino

`sc_protected_dst` включает `::ffff:0:0/96` (весь IPv4-mapped диапазон),
`sc_private_dst` содержит plain IPv4 приватные сети. При
`AllowPrivateDestinations=true` приватные IPv4 destinations открываются, но их
IPv4-mapped аналоги (`::ffff:10.0.0.5`) остаются заблокированными через
`sc_protected_dst`. Это safe-side: строже, чем требует семантика, исключает
DNS-rebinding через mapped-адреса. Иocumented выбор: если администратору нужны
mapped-private, это требует отдельной директивы, не вводимой в текущем slice.

## Три независимых слоя TLS

| Слой | Что шифруется | Сертификаты и ограничения |
| --- | --- | --- |
| HTTPS панели | Браузер → web-панель | Серверный сертификат с SAN панели |
| HTTPS proxy | Клиент → Squid | Отдельный серверный сертификат proxy; клиент доверяет issuer |
| HTTPS origin через CONNECT | Клиент → внешний сайт внутри туннеля | Обычный сертификат сайта, Squid не читает содержимое |

## Привязка transport к listener

`proxy_transport` означает требуемый входной transport, а не только подсказку
клиентской инструкции. Compiler обязан отклонять запрос клиента на другом listener,
чтобы TLS-профиль нельзя было незаметно обойти через оставленный HTTP-порт.
Способ определения listener (например localport ACL) проверяется на выбранном image.
На одном TLS listener используется один согласованный server cert reference.

## Профиль tunnel

Без расшифровки доступны source IP, CONNECT host:port, результат туннеля,
продолжительность и доступные счётчики байтов. URL-путь, HTTP-статус сайта и
содержимое внутри HTTPS недоступны. HTTP-статус CONNECT не равен статусу сайта.
Не выводить в UI поддельные URL и причины, которых нет в логах.

HTTPS-содержимое не кешируется Squid в этом режиме. Для HTTP кеширование зависит
от cache headers и параметров объекта. Не менять репозитории на HTTP автоматически.

## Прямой egress и обход

Proxy-настройка — добровольный маршрут приложения. Если VM обязана пользоваться
прокси, сетевой firewall/SG разрешает ей соединения только к proxy listener,
служебному DNS и согласованным инфраструктурным сервисам; блокирует прямой
IPv4/IPv6 интернет и UDP/443. Это правило должно стоять на контролируемой границе,
а не зависеть от пользователя VM. Squid не перехватывает весь IP-трафик сам.

Доменный контроль CONNECT ограничен запрошенным host:port; разрешённый CDN или
web-сервис сам может предоставлять tunneling. Для строгой изоляции требуется
отдельная модель ресурсов и угроз. Domain allowlist не является полноценным DLP
или гарантией запрета любого обхода.

## Отключение клиента

Reload меняет правила новых запросов. Открытый CONNECT может продолжить жить.
В MVP UI предупреждает об этом. Немедленное уничтожение туннелей требует отдельно
проверенного механизма; restart всего Squid затронет всех клиентов и не подменяет кнопку
«Отключить доступ» без объяснения последствий.

## Настройки и экспертный режим

Settings управляет listener/safe/CONNECT ports, памятью кеша, DNS/TTL, timeouts,
body limit и rotation. По умолчанию private destinations запрещены; явное
allow_private_destinations открывает их для внутренних ресурсов, но не loopback
или link-local/metadata. Source kind — метаданные, фактическая идентичность IP.

`upstream_proxy` (`address`, `port`, `exclusive`) отправляет трафик через
вышестоящий прокси: `cache_peer <address> parent <port> 0 no-query no-digest default
name=sc_upstream`, при `exclusive` ещё `never_direct allow all`, без него Squid
при недоступном parent идёт напрямую. Адрес — IP-литерал без zone вне
`protectedDestinations`; private-адрес допустим независимо от
`allow_private_destinations`: это выбор администратора, клиентам private-назначения
он не открывает. Защитные запреты проверяются по адресу назначения запроса и
действуют до выбора peer.

Expert mode заменяет визуальный ACL-блок; защитные `http_access deny` рендерятся
до него и не переопределяются. Валидатор (`validateExpert`) принимает только
директивы из `expertDirectives` ([ADR 0007](../adr/0007-general-clients-expert-configuration.md)):

- токен директивы — строго `[a-z0-9_]+`, без кавычек и экранирования;
- параметры из файлов (`"/…"`, `parameters(…)`) запрещены во всех директивах;
- `deny_info` — только `ERR_*`, `http(s)://` URL (опционально с кодом 30x) или `TCP_RESET`;
- `cache_peer` — IP-литерал без zone вне `protectedDestinations`; адрес из
  `privateDestinations` только при `allow_private_destinations`; опции-пути запрещены;
- `sc_` имена допустимы только как `sc_http_listener`/`sc_tls_listener` в `http_access`.

Listeners, пути, helpers, заголовки ответа (canary `X-Squid-Control-Revision`) и
manager в allowlist не входят. Ошибка `squid -k parse` возвращается как номер
строки экспертной секции или итогового конфига, без текста Squid.

Защищённые назначения (всегда): `0.0.0.0/8`, loopback, link-local/metadata,
multicast, reserved, IPv4-mapped `::ffff:0:0/96`, NAT64 `64:ff9b::/96` и
`64:ff9b:1::/48`, discard `100::/64`, Teredo `2001::/32`, 6to4 `2002::/16`.
6to4 и Teredo закрыты целиком: в них кодируются произвольные IPv4, включая внутренние.

## Парольные клиенты proxy

Client.proxy_username связывает source networks с отдельной proxy account. Для
такого клиента обязателен TLS transport и установленный proxy-server cert.
Username case-sensitive и сопоставляется exact regex, включая буквальное REQUIRED
и точки. Разные auth usernames могут иметь пересекающиеся source networks за NAT;
пересечение IP-only клиента с auth-bound или одинаковых auth usernames запрещено.

Managed Basic helper и uncached generation ACL — подрежимы того же executable,
не пользовательские команды. Protected destination/port denies выполняются до
аутентификации. Plaintext для auth sources запрещён, отсутствие/ошибка credentials
не допускают трафик. Generic successful authentication не даёт чужую политику.
Enabled auth clients не допускаются в expert mode, чтобы пользовательский ACL
не обходил binding. IP-only clients сохраняют прежнее поведение.

### Read-only management metrics

Runtime владеет отдельным `http_port 127.0.0.1:3131`; порт защищён от settings
и expert editor. Только loopback GET фиксированных info/counters/5min/active_requests
разрешается на этом listener. Далее listener закрыт целиком, manager запрещён
на пользовательских listeners до любых policy/expert allows. Произвольных manager
actions и проксирования URL через API нет. [ADR 0013](../adr/0013-read-only-squid-metrics.md).

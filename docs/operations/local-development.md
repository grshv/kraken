# Локальный запуск

Команды этого документа реализованы. Остальные runbooks описывают также будущие
операции и не означают, что backup/restore уже автоматизирован.

## Сборка и первый вход

Из корня: `make image`, `make dev-cert`, `make bootstrap`, `make up`.
Compose service/image: `squid-control`, `squid-control:dev`.
Панель публикуется на `127.0.0.1:8443`, HTTP proxy на `127.0.0.1:3128`.
Изначально Squid закрыт для всех неизвестных клиентов; регистрация через браузер отсутствует.
Пользователь/пароль задаются интерактивно bootstrap-командой. Она работает только
при отсутствии admin и запускает бинарник от UID/GID 13:13.

Frontend, backend и Squid в одном контейнере. Go/Python/Node на host не нужны
для runtime; Python нужен host helper для скрытого ввода bootstrap password.
В контейнере оба сервиса работают от proxy (13:13), Pebble — supervisor.
Это практический компромисс первого среза, не изоляция процессов друг от друга.

## Данные

Compose named volume `/data`: `app/` (SQLite), `runtime/` (LKG/journal/active config),
`logs/` и `certs/`. `make down` сохраняет volume. Не используйте `down -v`, если
данные нужны. Cache отключён. Секреты в volume не являются резервной копией.

`dev-cert` не заменяет существующие ключи, создаёт только localhost development
certificate. Public certificate можно получить командой:

```sh
docker compose -f deploy/compose.yaml cp squid-control:/data/certs/panel.crt /tmp/squid-control-panel.crt
```

Для HTTP клиента используйте `--cacert /tmp/squid-control-panel.crt`; не отключайте
проверку TLS. Системный trust store при установке проекта автоматически не меняется.

## Реальные сертификаты

Для своего panel certificate добавьте Compose override с read-only mounts,
`SC_PANEL_CERT`, `SC_PANEL_KEY` и соответствующим `SC_PUBLIC_ORIGIN`.
Имя должно совпадать с SAN. UID 13 должен читать key; не делайте ключ общедоступным.
Для proxy TLS подготовлен [override](../../deploy/compose.tls.yaml): пути к файлам
передаются `SC_PROXY_CERT_FILE`/`SC_PROXY_KEY_FILE`, server ref — `proxy-server`.

```sh
SC_PROXY_CERT_FILE=/absolute/path/proxy.crt SC_PROXY_KEY_FILE=/absolute/path/proxy.key \
  docker compose -f deploy/compose.yaml -f deploy/compose.tls.yaml config --quiet
```

Затем поднимите ту же конфигурацию с `up -d`. TLS listener — 3129. Клиент с TLS-профилем
не получает доступ через HTTP listener. Серверный сертификат защищает
подключение к прокси; HTTPS сайтов остаётся сквозным CONNECT.
Ротация/смена установленных путей требует проверки и пересоздания контейнера;
web provisioning ключей не реализован. Production ключи не помещать в Git/image.

## Проверка поведения

UI: добавить клиента/IP/CIDR → сохранить черновик → посмотреть изменения → проверить →
применить. Разрешённые домены — ASCII DNS names, точка в начале в UI включает
поддомены. IDN/glob/URL paths не поддержаны; пересечения сетей запрещены.
Откат доступен в истории ревизий: сначала сравнение, затем отдельная проверяемая операция.

Для реальных клиентов задайте SC_MANAGEMENT_BIND/SC_PROXY_BIND и firewall до публикации.
Loopback source — внутренняя диагностика, её запросы не попадают в client access log.
Если доступ в панель через Docker NAT, IP браузера не является IP proxy-клиента.
Действующее правило применяется по source IP, который видит именно Squid.

## Диагностика и процессы

```sh
docker compose -f deploy/compose.yaml exec squid-control pebble services
docker compose -f deploy/compose.yaml exec squid-control pebble logs -n 50
docker compose -f deploy/compose.yaml exec --user 13:13 squid-control squid-control check
docker compose -f deploy/compose.yaml exec squid-control pebble restart control
```

Последняя команда перезапускает только Go. Полный container/image upgrade прерывает
и Squid. Любой старт Squid проходит recovery gate. Apply блокируется при unfinished
journal/drift; restart control запускает reconciliation. Не редактировать journal
и active.conf вручную вместо recovery.

## Проверки разработчика

`make check`, `make image`, `make integration` из корня. Smoke создаёт уникальные
containers/network/volume и удаляет их в finally, не трогая пользовательский Compose.
Нужен свободный тестовый subnet 11.250.17.0/24; origins и client identities синтетические.
Доступ к интернету нужен для pull/build зависимостей, не для проверки HTTP origins.
Browser tests сами создают и удаляют отдельный container/volume с account;
случайный password живёт только в памяти и передаётся CLI по stdin, credential
files и password env не используются. Запуск: `npm --prefix src/web run test:e2e`.
Нужен установленный Playwright Chromium либо SC_CHROMIUM_PATH к executable.
Только fixture допускает self-signed development TLS; network smoke отдельно
проверяет chain без отключения верификации.

## Новые настройки и сброс

[Настройки и экспертная граница](../adr/0007-general-clients-expert-configuration.md).
[Пароль и CLI reset](../security/password-lifecycle.md). Внутренние HTTP/TLS ports
редактируются в Settings; публикацию Docker нужно менять отдельно. Диагностический
3130 привязан к loopback и не публикуется; текущий verify использует этот
канонический listener. Совместимость более старого runtime bundle без 3130
нельзя считать подтверждённой; её проверяют перед обновлением конкретной установки.

## Пользователи и логины прокси

После migration 004 прежний администратор остаётся admin с тем же verifier;
пересоздавать bootstrap или сбрасывать пароль для обновления не требуется.
Раздел «Пользователи» доступен admin: две роли, блокировка и смена пароля.
Viewer видит только состояние и журналы. CLI password-reset --username работает
с выбранным пользователем и отзывает его sessions.

Для парольного клиента сначала установите server cert/key для proxy TLS через
имеющийся compose.tls.yaml, затем создайте отдельный proxy login в панели и
назначьте его клиенту. Auth-bound client требует TLS transport, а клиентское
приложение должно проверять server certificate proxy. Пароль не входит в draft,
preview/config или историю; в DB только PHC verifier. Смена/блокировка proxy login
действует для новых запросов, уже открытый CONNECT может продолжаться.

### Порты и интерфейс

Внутренние HTTP/TLS listeners относятся к упаковке/установке. Web/API не меняет их
среди обычных ACL-настроек; destination ports и CONNECT ports по-прежнему задают
доступ клиентов к ресурсам. Внешний порт выбирается mapping Compose; изменение
host mapping не означает редактирование внутреннего squid.conf через UI.

Тема панели по умолчанию светлая; компактная кнопка с солнцем/луной рядом
с выходом сохраняет light/dark preference в браузере. В нём нет session credentials.
Изменения страницы клиента/настроек сохраняют draft; apply выполняется отдельно
в «Изменениях». Создание/блокировка/пароль учётных записей действуют сразу.

### Служебное обновление Squid config

После обновления image старый applied bundle может требовать нового служебного
listener для метрик. В локальном контейнере выполните:

```sh
docker compose -f deploy/compose.yaml exec --user 13:13 squid-control squid-control service-update
```

Команда создаёт ревизию из подтверждённого LKG, журналируемую операцию и audit
`local-cli/service_update`; пользовательский draft не читает и не применяет.
Требуются работающий Squid, доступная SQLite и отсутствие pending recovery/drift.
Parse/reload/verify/rollback те же, что у обычного apply. Уже актуальный bundle
возвращает явную ошибку no-op. При ошибке смотрите сохранённую operation и journal;
не редактируйте LKG вручную. Команда не доступна из публичного HTTP API.

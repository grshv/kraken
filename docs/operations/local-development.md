# Локальный запуск

Команды этого документа реализованы. Остальные runbooks описывают также будущие
операции и не означают, что backup/restore уже автоматизирован.

## Сборка и первый вход

Из корня: `make image`, `make dev-cert`, `make bootstrap`, `make up`.
Compose service/image: `kraken`, `kraken:dev`.
Панель публикуется на `127.0.0.1:8443`, HTTP proxy на `127.0.0.1:3128`.
Изначально Squid закрыт для всех неизвестных клиентов; регистрация через браузер отсутствует.
Пользователь/пароль задаются интерактивно bootstrap-командой. Она работает только
при отсутствии admin и запускает бинарник от UID/GID 10013:10013.

Frontend, backend и Squid в одном контейнере. Go/Python/Node на host не нужны
для runtime; Python нужен host helper для скрытого ввода bootstrap password.
В контейнере Squid работает от `proxy` (13:13), панель — от `kraken` (10013:10013),
Pebble — supervisor ([ADR 0015](../adr/0015-separate-squid-uid.md)).

## Данные

Compose named volume `/data`: `app/` (SQLite), `runtime/` (LKG/journal/active config),
`logs/` и `certs/`. `make down` сохраняет volume. Не используйте `down -v`, если
данные нужны. Cache отключён. Секреты в volume не являются резервной копией.

`dev-cert` не заменяет существующие ключи; создаёт development-сертификаты панели
(`panel.*`, только для 10013) и прокси (`proxy.*`, SAN `proxy`, `localhost`, читает группа `proxy`). Public certificate можно получить командой:

```sh
docker compose -f deploy/compose.yaml cp kraken:/data/certs/panel.crt /tmp/kraken-panel.crt
```

Для HTTP клиента используйте `--cacert /tmp/kraken-panel.crt`; не отключайте
проверку TLS. Системный trust store при установке проекта автоматически не меняется.

## Реальные сертификаты

Для своего panel certificate добавьте Compose override с read-only mounts,
`KRAKEN_PANEL_CERT`, `KRAKEN_PANEL_KEY` и соответствующим `KRAKEN_PUBLIC_ORIGIN`.
Имя должно совпадать с SAN. UID 10013 должен читать key; не делайте ключ общедоступным.
Для proxy TLS подготовлен [override](../../deploy/compose.tls.yaml): пути к файлам
передаются `KRAKEN_PROXY_CERT_FILE`/`KRAKEN_PROXY_KEY_FILE`, server ref — `proxy-server`.

```sh
KRAKEN_PROXY_CERT_FILE=/absolute/path/proxy.crt KRAKEN_PROXY_KEY_FILE=/absolute/path/proxy.key \
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

Для реальных клиентов задайте KRAKEN_MANAGEMENT_BIND/KRAKEN_PROXY_BIND и firewall до публикации.
Loopback source — внутренняя диагностика, её запросы не попадают в client access log.
Если доступ в панель через Docker NAT, IP браузера не является IP proxy-клиента.
Действующее правило применяется по source IP, который видит именно Squid.

## Диагностика и процессы

```sh
docker compose -f deploy/compose.yaml exec kraken pebble services
docker compose -f deploy/compose.yaml exec kraken pebble logs -n 50
docker compose -f deploy/compose.yaml exec --user 10013:10013 kraken kraken check
docker compose -f deploy/compose.yaml exec kraken pebble restart control
```

Последняя команда перезапускает только Go. Полный container/image upgrade прерывает
и Squid. Любой старт Squid проходит recovery gate. Apply блокируется при unfinished
journal/drift; restart control запускает reconciliation. Не редактировать journal
и active.conf вручную вместо recovery.

## Проверки разработчика

`make check`, `make image`, `make integration` из корня. `make integration` —
`go test -tags integration` (пакет `src/tests/integration`), фильтр через `RUN=`
(например `make integration RUN=TestSmoke`). Тесты создают уникальные
containers/network/volume и удаляют их в `t.Cleanup`, не трогая пользовательский
Compose. Нужен свободный тестовый subnet 11.250.17.0/24; origins и client
identities синтетические. Доступ к интернету нужен для pull/build зависимостей,
не для проверки HTTP origins. Web unit-тесты (`npm --prefix src/web run test`)
гоняют vitest без браузера и образа; e2e на ключевые потоки — отдельный срез.

`make integration RUN=TestOps` (`ops_test.go`) поднимает отдельные
disposable контейнеры/сети/volumes с уникальным префиксом и проверяет
процедуры runbook на образе `kraken:ops` (`docker build -f deploy/Dockerfile
-t kraken:ops .`, тег параметризуется через `KRAKEN_TEST_IMAGE`): копия и
восстановление `/data`, замена серверного сертификата панели и прокси, обновление
образа на том же volume, поведение при заполненном диске (tmpfs-backed `/data`).
Сертификаты и ключи генерируются в `t.TempDir()` через `crypto/x509` и не коммитятся.

## Новые настройки и сброс

[Настройки и экспертная граница](../adr/0007-general-clients-expert-configuration.md).
[Пароль и CLI reset](../security/password-lifecycle.md). Внутренние HTTP/TLS ports
редактируются в Settings; публикацию Docker нужно менять отдельно. Диагностический
3130 привязан к loopback и не публикуется; verify использует этот listener.

## Пользователи и логины прокси

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
docker compose -f deploy/compose.yaml exec --user 10013:10013 kraken kraken service-update
```

Команда создаёт ревизию из подтверждённого LKG, журналируемую операцию и audit
`local-cli/service_update`; пользовательский draft не читает и не применяет.
Требуются работающий Squid, доступная SQLite и отсутствие pending recovery/drift.
Parse/reload/verify/rollback те же, что у обычного apply. Уже актуальный bundle
возвращает явную ошибку no-op. При ошибке смотрите сохранённую operation и journal;
не редактируйте LKG вручную. Команда не доступна из публичного HTTP API.

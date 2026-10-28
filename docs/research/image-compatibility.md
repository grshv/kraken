# Проверка реального image, 27–28.10.2026

Проверено локально через Docker Engine на Linux/arm64. Это измерения текущего
рабочего среза; amd64 и production сеть ещё не проверены.

## Зафиксированная база

`ubuntu/squid:6.6-24.04_beta@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029`

arm64 manifest: `sha256:eecf35336788c1ab143566bca99ac58f213e9f17906a9a684346d7b323b6696c`.
Фактический `/usr/sbin/squid -v`: **6.13**, `--with-gnutls`, без OpenSSL.
Image entrypoint — `/usr/local/bin/entrypoint.sh`, shell, **Pebble отсутствует**.
Это расходится с overview Docker Hub; `7.2-26.04_beta` registry не находит.
Tag name не является надёжным источником фактической Squid version.

В производный image добавляется Pebble **v1.33.2** из pinned Go build.
Go builder **1.27.3-bookworm**, Node builder **24-alpine** закреплены digest в
[Dockerfile](../../deploy/Dockerfile). Версии Go dependencies и npm пакетов/шрифтов
зафиксированы в `src/go.mod`, go.sum и web/package-lock.json.

## UID, управление и процессы

Squid и squid-control запускаются от `proxy:13:13`, без root web и Docker socket.
Общий UID позволяет локальный parse/reconfigure и чтение persistent LKG/config.
Он не защищает конфиг и server TLS keys от скомпрометированного Go-процесса;
изоляция секретов внутри общего UID не заявляется.
Pebble PID 1 управляет сервисами отдельно; `run --hold` не запускает сервисы,
поэтому production command нашего image — `run --verbose`.

Перед запуском Squid подрежим того же executable восстанавливает journal/LKG,
затем exec `/usr/sbin/squid -N -f /data/runtime/active.conf`.
Apply перед изменением проверяет old runtime canary; в startup gap запрос не
может примениться к ещё не запущенному Squid. Общий filesystem lock защищает gate.

## Проверенная матрица

| Возможность | Результат | Проверка |
| --- | --- | --- |
| HTTP и deny unknown source | Supported | Реальный client fixture |
| Per-source all_web/allowlist | Supported | Разные src, allowed/denied fake domains |
| Loopback/metadata защита | Supported для проверенных cases | Отказы 127.0.0.1 и 169.254.169.254 |
| HTTPS CONNECT | Supported | TLS origin с проверкой сертификата |
| TLS до proxy | Supported | GnuTLS listener, trusted server cert |
| Защита transport от downgrade | Supported | TLS-профиль на HTTP listener получает 403 |
| TLS до proxy + TLS CONNECT в комбинации | Supported в проверенном сценарии | Verified TLS к proxy и отдельная проверка сертификата origin внутри CONNECT, включая proxy auth |
| Parse / reload / canary | Supported | Локальный бинарник и revision response header |
| Independent Go restart | Supported | PID Squid сохраняется, открытый CONNECT проверяется smoke |
| Squid/container restart | Supported для штатных cases | Gate и persistent applied policy |
| Fault/crash boundaries | Частично | ENOSPC injection на записи; SIGKILL отдельного Go test process на durable границах; parse/reload/rollback/drift/lock |
| Визуальные настройки и expert section | Supported в проверенном объёме | Реальный parse/apply и трафик; неизвестная директива не меняет active |
| Listener ports контейнера | Installation setting | Draft и старые revisions не могут менять listeners; реальный 3128 и canary3130 сохраняются после отказа API |
| CLI password-reset | Supported | Старые сессия/пароль отклоняются, новый вход успешен |

Runtime canary — HTTP 403 обязательного loopback deny с fingerprint compiler,
snapshot и installed options. Это проверяет перечитывание конфигурации и защитный
запрет, не доказывает каждый ACL; поведение клиентских политик проверяется отдельно.
Начиная с compiler v3 используется отдельный listener 127.0.0.1:3130, который не публикуется
Docker. Для сохранённых конфигураций прежнего compiler probe совместим с 3128.

## Исполняемые доказательства

- `make check`: Go race tests (auth/CSRF/revoke, stale draft, policy guards,
  parse/reload/rollback/crash/drift, PHC/reset, expert guards), frontend typecheck/build и docs.
- `make image`: multi-stage image из [deploy](../../deploy/Dockerfile).
- `make integration`: [реальные сетевые fixtures](../../src/tests/integration/smoke.py),
  без default password, с проверкой цепочки сертификатов.
- [Browser fixture](../../src/web/tests/panel.spec.ts): вход, создание клиента,
  visual settings/expert template, validation/apply, история/rollback, desktop/mobile
  overflow и logout. Каждый запуск создаёт и удаляет отдельный контейнер/volume;
  случайный пароль существует только в памяти и передаётся CLI по stdin.
  Только браузерный fixture принимает свой self-signed сертификат без проверки
  доверия; trust/chain проверяется отдельным сетевым smoke.

## Текущий остаток

[Roadmap](../plans/roadmap.md) задаёт объём: реальный APT, выбранная целевая
архитектура/сеть, адресные IPv6/rebinding/private deny tests, один реальный
storage failure и простые процедуры эксплуатации. Остановленная копия volume,
внешних TLS mounts и параметров с одним restore exercise достаточна; backup
подсистема не требуется. Перед распространяемым релизом — scans и короткие notes.

Rotation/truncation, migrations, история/cursor/retention уже реализованы и
проверены в срезах ниже. Дополнительные users/roles и proxy auth реализованы и
проверены на указанной сборке, подробности — в последнем срезе ниже.
Certificate UI, агрегаты/расширенный поиск, произвольные helpers, SSO и HA исключены. Отсутствие проверки не превращается
в Supported: конкретные гарантии доступа по-прежнему требуют своих tests.
Один контейнер — единая точка обновления/отказа, это известное ограничение.

ENOSPC здесь инъецируется в слой атомарной записи, а SIGKILL выполняется над
отдельным тестовым процессом. Это не испытание переполненного реального диска,
отключения питания или reboot всей VM. Expert guard не является полным sandbox
Squid; helper/storage provisioning добавляется только по отдельной потребности. HTTPS сайтов сквозной.

Финальная проверка расширения: `make check`, `make image`, Docker build target
`verify` (Go race/vet на Linux/arm64), real-image smoke и Playwright пройдены.
Сохранённый локальный Compose volume поднят новой версией: `/healthz` и `/`
возвращают 200 при проверке доверия к panel certificate, оба Pebble services active,
`check` успешен. Bootstrap/reset существующего администратора не выполнялись.

## Сквозной HTTPS, уточнение 27.10.2026

Единственный TLS mode для сайтов — `tunnel`. API 0.3.0 исключает прежние
неиспользуемые поля TLS profile. Compiler v4 учитывает новую сериализацию;
сохранённый runtime bundle продолжает работать до нового apply. Проверены
`make check`, image build, реальные HTTP/CONNECT/TLS proxy и browser suite.
Существующий локальный volume успешно поднят без bootstrap/reset.
[Критерии и результат](../plans/active/tunnel-only.md).

## Исправления повторного ревью, 27.10.2026

Проверены anonymous CSRF без DB pool, атомарная миграция, first_run recovery и
подтверждённый online rollback. Устранён регресс адреса панели: :8443 внутри
контейнера, ограничение host exposure задаёт Compose. Real-image smoke прошёл,
включая pending-journal restart с возвратом прежних ACL и реальными запросами;
Playwright — 4 сценария, включая повторную авторизацию после GET/mutation expiry.
[Полные критерии, результаты и ограничения](../plans/active/review-reliability.md).
Тестовые контейнеры/volumes отдельные; существующая установка не обновлялась.

## История и аудит, 27.10.2026

API 0.4.0, migration 003: фоновый bounded reader, нормализованная SQLite-история,
checkpoint, client/failure filters и cursor; отдельный audit API/UI. Retention
событий 7 дней по сбору/50 000 строк, audit 90 дней/5000 строк. Проверены atomic
retry, source stale/unavailable, rotation/truncation, реальные Squid запросы,
отдельный restart control с сохранением истории, audit CLI reset и 5 browser
сценариев. make check и image build прошли; screenshots desktop/mobile проверены.
Compiler v4 и правила трафика не изменены; существующая установка не обновлялась.
[Критерии и оставшиеся ограничения](../plans/active/diagnostics-history.md).

## Локальные пользователи, proxy passwords и shadcn/ui, 27.10.2026

API 0.5.0, migrations 004/005, compiler v5. Две роли admin/viewer, серверные
permissions, создание/блокировка/смена пароля, session revoke и last-admin guard.
Миграционный тест сохраняет прежние admin/hash/session. Отдельные proxy accounts
используют Argon2id и управляемые Basic/state helpers того же executable.

На pinned Squid 6.13/GnuTLS Linux/arm64 проверены verified TLS proxy auth,
HTTP 407/403, разные identities за NAT с разными destinations, literal REQUIRED,
case-sensitive username, пароль с пробелом/+/% и reset/disable на живом повторно
используемом TLS-соединении. Проверен CONNECT с независимой проверкой сертификата
сайта внутри TLS-подключения к прокси. Тестовый volume просканирован на исходные
пароли и Basic values; совпадений нет. Начатый до reset запрос и открытый CONNECT
могут завершиться; принудительное закрытие существующих туннелей не реализовано.

React UI переведён на shadcn/ui (New York/Radix), Tailwind 4, Inter и Lucide.
Sidebar/Sheet, Button, Input/NativeSelect/Textarea, Table, Badge, Card, Dialog и
Tabs установлены из официального registry; общие states принадлежат компонентам.
Проверены реальные desktop/mobile screenshots обзора, событий, аудита и accounts.
Все 6 Playwright сценариев прошли, включая permissions/403 и отзыв viewer session.

`make check` (docs, type/build, Go race tests, vet, diff check), `make image`
(включая Linux/arm64 verify stage), полный real-image smoke и браузерные сценарии
пройдены. Тесты используют отдельные контейнеры/volumes и случайные credentials
через stdin; существующий пароль для проверки не сбрасывался. Это подтверждение
указанной сборки и сценариев, не новой целевой архитектуры или реального APT.

Локальная сохранённая Compose-установка обновлена тем же образом, volume и учётные
записи сохранены. В её начальном LKG без compiler_version отсутствовал порт 3130:
панель запускалась, но текущий fingerprint probe не мог подтвердить Squid.
Для этой установки при остановленном контейнере добавлен только loopback listener
127.0.0.1:3130 с первым deny ACL. Существующие клиентские правила, snapshot,
revision и hash сохранены; candidate проверен настоящим Squid parse до записи,
исходный manifest оставлен в volume как `.legacy-probe-previous.json`.
После запуска `check` успешен, оба Pebble services active, `/healthz` и `/` дают
200 с проверкой доверия к panel certificate, новый embedded bundle подтверждён.
Это выполненная локальная совместимость начального конфига, не автоматическая
миграция произвольных старых runtime bundles и не общее обещание обновления без
перерыва. Bootstrap/password-reset на сохранённой установке не выполнялись.

## Потоки панели и retained статистика, 28.10.2026

API 0.6.0, compiler5 и migrations1–5 сохранены. Read-only draft preview отделён от
создания revision; defaults и эквивалентные CIDR не дают пользовательский delta.
Managed config update виден отдельно, полный технический diff раскрывается по
запросу. Listener ports защищены в draft/validate/apply/rollback; уже установленное
значение сохраняется. Empty expert template fail-closed, итоговый конфиг доступен
без создания revision. /statistics читает bounded retained историю за сутки,
ошибки/лимит/источник не скрываются под фиктивными показателями.

Оболочка без branding/avatar/лишнего toolbar; компактная тема, два semantic schemes,
общий EntityDialog, separate transport state, dirty navigation guard и operation
GET recovery. Обзор — состояние/статистика, accounts не смешивают список с inline
editor. Старые «О журнале»/вводные пояснения убраны из рабочих экранов.

`make check`, `make image`, полный native smoke и 15 browser cases прошли.
Проверены обе темы и desktop/mobile screenshots. Точная матрица и пределы:
[UI workflows](../plans/active/ui-workflows.md). Это не проверка нового APT клиента
или новой платформы. После проверок локальная Compose-установка обновлена:
оба Pebble services active, `check` успешен, /healthz и новый embedded bundle
возвращают 200 с проверкой доверия к panel certificate. Сохранён прежний named
volume; password-reset/bootstrap и изменение политики доступа не выполнялись.

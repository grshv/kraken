# Исправление пользовательских потоков панели

Дата: 2026-10-27. Статус: Completed 28.10.2026, включая повторный разбор выравнивания и живого обзора. Основание — разбор владельцем реальных экранов.

## Обнаружено

| Область | Расхождение | Решение и проверяемый результат |
| --- | --- | --- |
| Каркас | Управление sidebar вынесено в лишний toolbar с повторной маркой | Desktop trigger внутри sidebar; свёрнутое меню сохраняет доступ к trigger, mobile имеет только кнопку открытия |
| Темы | shadcn подключён без полноценной тёмной схемы | Единые light/dark semantic tokens; Следование ОС до ручного выбора, компактная кнопка light/dark, сохранение предпочтения, проверка обеих тем |
| Ошибки | Fetch exception смешан с отказом Squid и не очищается при успешном опросе | Состояние связи с панелью отдельно от Squid; повторный успешный запрос снимает network banner; mutation error не стирается случайным poll |
| Обзор | CRUD-кнопка вместо показателей | Только наблюдаемое состояние и статистика SQLite за 24 часа; нет добавления клиента и вымышленных счётчиков |
| Изменения | Просмотр создаёт revision; defaults и compiler delta выглядят пользовательской правкой | Read-only preview черновика; user/runtime flags; no-op не требует применения, служебный diff раскрывается отдельно |
| Установка | Listener ports редактируются вместе с ACL, Docker mapping остаётся прежним | Порты — параметры установки; убраны из UI, draft API отвергает их случайную смену |
| Expert | Пустой template при пустых клиентах; полный конфиг спрятан в другой экран | Непустой fail-closed template, явный переход в expert, доступный итоговый read-only конфиг |
| Редакторы | Клиент — Dialog, accounts — inline form | Один EntityDialog для создания/редактирования всех сущностей, одинаковые footer и pending/dismiss rules |
| Журналы | Постоянная техническая справка замусоривает рабочий экран | Удалить общий блок справки; сохранить только реально действующие error/stale/gap сообщения |

Это исходные найденные дефекты; подтверждённые результаты приведены в конце плана.

## Архитектура интерфейса

- AppFrame отвечает за навигацию/sidebar без логотипа и аватара, ThemeProvider — за тему; не запрашивает конфигурацию.
- App координирует сессию, permissions, черновик и операции; транспорт отличает
  network failure, HTTP error и session expiry. Успех транспорта не доказывает
  здоровье Squid: оно приходит только из status API.
- OverviewPanel получает status/statistics/history как данные, не изменяет draft.
- SettingsPanel редактирует локальную копию и сохраняет её в draft; source ports
  и полный служебный конфиг не являются произвольными редактируемыми полями.
- ChangesPanel показывает read-only preview. Immutable revision создаётся для
  проверки/применения, не при каждом открытии страницы; validate привязан к revision.
- EntityDialog — общий редактор клиента/users/proxy accounts; справочники не
  смешивают таблицу с открытой inline формой. Account mutation действует сразу,
  client/settings mutation меняет только draft — эта разница обозначена в действиях.
- DiagnosticsPanel владеет фильтрами, страницей и freshness своего журнала.
- Backend остаётся источником прав, валидации и ACL; UI не обходит guards.

## Проверка остальных сценариев

Проверить вход/выход/expiry и viewer permissions; initial/loading/empty/API-error
состояния каждого экрана; повторную отправку и busy guards; скрытые ошибки после
успеха; локальные несохранённые настройки при навигации; сохранение/preview/
validate/apply/rollback с конфликтом версии; источник времени и единицы показателей;
поведение после reload и на mobile; keyboard focus/dialog dismissal/contrast.
Найденное вне текущего исправления фиксировать адресно с причиной, не объявлять
весь продукт исправным на основании скриншотов или одного happy path.

## Критерии завершения

Все девять жалоб воспроизведены или объяснены по коду/контракту и закрыты
наблюдаемым поведением. Backend tests проверяют no-op/default normalization,
installation port guard, empty expert template и статистику, включая временные
границы. Browser tests проверяют сеть/восстановление, темы, sidebar, единые
редакторы, экспертный конфиг и отсутствие CRUD в обзоре. Проходят make check,
image, real-Squid smoke, browser suite; screenshots обеих тем desktop/mobile
просмотрены. Обновлены requirements/interface/lifecycle/OpenAPI/capability report.
Рабочая панель обновляется после проверок с сохранением volume и пароля.

## Дополнительные результаты аудита

Помимо девяти экранов проверены права, состояние формы, ошибки источника и
жизненный цикл операций. Исправлены потеря opener focus после Dialog, no-op
account submit с лишним session revoke, уход из dirty settings без подтверждения,
потеря опроса известной операции после сетевого отказа, отсутствие retry списка
accounts и ложное пустое состояние при ошибке чтения. Возврат поля настроек к
исходному значению теперь снимает dirty, а не требует пустого сохранения.

В документах найдены и исправлены: VM как обязательная сущность; роли как якобы
ещё не реализованная функция; admin-only журналы после введения viewer;
таблица будущих реляционных сущностей при фактических JSON snapshots; старые
утверждения про compiler и редактирование listeners. Основные решения/контракты
обновлены в interface, requirements, lifecycle, data-model, observability и OpenAPI.

## Подтверждено 28.10.2026

- `make check`: docs, frontend type/build, все Go race tests, vet, diff check.
- `make image`: pinned single-container build, Linux/arm64 verify stage.
- Полный real-Squid smoke: HTTP/TLS/CONNECT/auth, actual settings/expert traffic,
  parse/reload/rollback/recovery, CLI reset, история и права. Попытка сменить оба
  installation listeners получает 422; действующий порт и canary работают.
- 15 Playwright сценариев на настоящих изолированных fixtures, все прошли.
  Включены original apply/rollback/expiry/audit/roles и новые theme/sidebar/
  statistics/network/dialog/expert/no-op/dirty-navigation/operation-recovery cases.
- При обрыве GET operation и восстановлении связи проверка завершается, POST
  operation остаётся ровно один. Это не обещание автоматически повторять mutation,
  если её исходный ответ потерян до получения operation ID.
- Снимки light/dark desktop, dark mobile, mobile menu и expert editor просмотрены.
  Подписи графика вынесены из масштабируемого SVG, остаются читаемыми на 390px.
  Branding и avatar удалены, тема — одна icon button.

Проверки не означают полный аудит всего Squid, всех браузеров или production
сети. Целевой Linux/архитектура и реальный APT остаются отдельными проверками
roadmap. Новые сервисы/миграции/долгосрочная аналитика не добавлялись.

Локальная панель обновлена после проверок, оба сервиса active, live fingerprint
подтверждён CLI check, /healthz и актуальный UI bundle — HTTP200 с доверенным cert.
Volume, пароли и действующая клиентская политика сохранены. Отдельное служебное
обновление конфига остаётся явным действием, не выполняется молча при просмотре.

## Повторный разбор: живое состояние Squid и выравнивание

Текущий этап открыт после новых фактических скриншотов владельца. Ранее пройденные
15 сценариев не проверяли геометрию controls при разной высоте подсказок и не
подтверждали полезность обзора для текущей работы Squid.

- Причина смещений — растянутые grid labels с неявными auto rows; description
  участвовал в распределении свободной высоты. Общие labels и grids получают
  start alignment; checkbox фильтра выравнивается по control, не по label+control.
  Header/footer sidebar используют те же horizontal gutters, что group labels.
- Новая установка начинает со светлой темы независимо от ОС; только ручной выбор
  включает тёмную и сохраняет его. Автоматического следования ОС нет.
- Первичный источник обзора — fixed read-only Squid reports. Проверенные live
  показатели: версия/uptime, активные запросы без probes, CPU, peak RSS, origin
  requests/errors и bytes/rates. Не подменять peak RSS текущей RAM; не считать
  manager polling пользовательским трафиком. Недоступные значения — null.
- Пустой часовой график удалён. Retained access log остаётся компактной сводкой
  суток и последними ошибками с переходом в уже отфильтрованный журнал.
- Новый manager listener только loopback, exact reports/method/source/localport;
  public proxy по-прежнему закрывает manager. GET не меняет конфигурацию.
  Служебный upgrade применяет только confirmed snapshot, не пользовательский draft.

Критерии: сравнение координат полей settings/client/filter на реальной странице;
light default при dark OS + explicit dark persistence; native readings при HTTP,
idle manager polling и открытом CONNECT; наружный manager403; no fake data при
отказе/старом bundle; service-update сохраняет dirty draft и действующую политику.
Подробная граница: [ADR 0013](../../adr/0013-read-only-squid-metrics.md).
Подтверждено на финальной сборке:

- `make check`, image build и полный real-image smoke прошли; 17 browser cases
  прошли вместе, включая сравнение координат paired settings/client controls,
  checkbox/select filter baseline и sidebar text gutters.
- При dark OS без сохранённого выбора UI светлый, ручная тёмная сохраняется.
- Live endpoint возвращает реальные version/uptime/resources; простой =0 active,
  held CONNECT=1; polling manager не меняет origin request counters.
- Native test обнаружил переписывание authority manager URI на первый listener
  3130. Parser исправлен по local socket + exact report path, idle sockets не
  считаются активными; тест с пустой нагрузкой не ослаблен.
- Public manager read/write reports получают403. CLI service-update переводит
  старый bundle на новый compiler и сохраняет dirty draft неизменным; отказ parse
  сохраняет прежний runtime. Старые несовместимые revisions не предлагаются для rollback.
- Screenshots новых settings/client/filter baseline, светлого live overview и
  dark/mobile просмотрены. Пустого графика и прежних четырёх больших zero cards нет.

Ограничения: rates отражают интервал Squid до5мин и могут отсутствовать сразу
после запуска; peak RSS не current RSS; telemetry содержит origin traffic, а не
скрытые HTTP-запросы внутри HTTPS. Это не обещание видеть расшифрованный трафик.

Локальное обновление этого этапа также завершено: service-update succeeded,
confirmed policy до/после совпадает в journal, live canary подтверждён. Панель и
актуальный bundle отвечают200, public manager403; volume/пароль сохранены.

## Повторный разбор после наполнения журнала

28.10.2026 выявлена ошибка прежнего diff: общий prefix/suffix не выравнивал
неизменённую середину. Изменение fingerprint в начале поэтому красило почти
весь конфиг как заменённый. Реализованы bounded line LCS, unified hunks,
нормализация fingerprint, отдельный полный конфиг. В сценарии добавления клиента
теперь ровно три added и ноль removed; неизменённые служебные directives не дублируются.

Поток проверки/применения показывает источник сравнения; после проверки конкретной
ревизии применение и rollback требуют явного подтверждения в общем Dialog.
Исторический read-only просмотр не выдаётся за текущий черновик.
В таблице клиентов видны домены/поддомены и раскрытие длинного списка.
Журнал получил server-side IP/destination/method/result/time filters, reset,
размер страницы, движение в обе стороны и pause. Обзор показывает объём по журналу,
долю отказов и компактную почасовую динамику только при наличии данных.

Существующий browser test нашёл регрессию очищения старых строк при refresh error:
исправлено, предыдущие записи сохраняются с предупреждением. Browser tests
проверяют реальный apply/rollback с подтверждением, отмену без POST apply,
три добавленных строки, раскрытие доменов, серверный поиск за пределами первой
страницы, сброс cursor и оформление обзора при наличии записей.

Подтверждено: `make check`, image build, полный real-image smoke и все19 browser
cases прошли. После финальных правок подписей/размера page select повторены два
затронутых browser сценария на финальном image. Просмотрены реальные screenshots
светлой/тёмной темы, доменов, diff, подтверждения и mobile overview/filters.
Проверка не расширяет видимость HTTPS за пределы CONNECT metadata и не заменяет
оставшуюся проверку реального APT на целевом Linux.

# Исправление пользовательских потоков панели

Дата: 2026-10-27. Статус: Completed 28.10.2026. Основание — разбор владельцем реальных экранов.

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

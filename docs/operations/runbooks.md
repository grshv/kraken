# Runbooks

Ниже рабочие действия для текущего Compose и целевые процедуры backup/restore.
Backup/restore ещё не автоматизированы и не подтверждены упражнением восстановления.
Проверка процессов:

```sh
docker compose -f deploy/compose.yaml ps
docker compose -f deploy/compose.yaml exec squid-control pebble services
docker compose -f deploy/compose.yaml exec --user 13:13 squid-control squid-control check
```

## Прокси недоступен

1. Отделить отказ proxy VM/сети от отказа панели. Проверить маршрут к listener
   с клиентского устройства и health на host.
2. Проверить Squid process/Pebble health, disk space, DNS, срок сертификата,
   firewall и последние runtime errors.
3. Сравнить последнюю applied revision, runtime LKG и наблюдаемый config hash.
4. Если поломка после apply — rollback через внутренний runtime module, проверить реальный запрос.
5. Если поломка от upstream/DNS — не менять ACL случайными allow-all.
6. Зафиксировать время, operation ID, image digest и очищенные findings.

## Панель недоступна, трафик работает

Проверить HTTPS Go → SQLite → runtime module и Pebble service status отдельно.
Squid не перезапускать для восстановления UI. Если web упал в apply, сначала прочитать runtime journal;
повторить запрос с прежним idempotency key или выполнить reconciliation.
Задать новый пароль admin: `make password-reset` из корня репозитория.
Команда работает и при остановленной панели, прежние сессии отзываются.
Старый пароль не восстанавливается. [Прямой CLI](../security/password-lifecycle.md).

## APT/curl не скачивают

Сверить effective proxy settings приложения: sudo, systemd, контейнеры и APT
могут не наследовать shell env. Проверить source IP, enabled client, allowlist,
redirect/CDN домены, TLS profile, proxy DNS и время на VM.
Различать proxy denied (обычно 403), proxy auth (407),
TLS handshake failure, upstream HTTP errors и APT signature/hash errors.
В tunnel режиме upstream HTTP status не виден в Squid log.
Не отключать apt-secure и TLS verification ради устранения симптома.

## Apply завершился ошибкой

- `rejected`: активная версия не менялась, исправить findings и создать новую validation.
- `rolled_back`: проверить LKG и причину; повторный apply создаёт новую operation.
- `degraded`: запретить новые apply, проверить journal/hash/secret references,
  локально восстановить согласованный LKG и выполнить runtime probes.
- Reload timeout не равен «ничего не применилось»; повтор без key запрещён протоколом.

## Drift

Сохранить очищенные metadata и hash изменения, выяснить источник ручной правки.
Не импортировать неизвестный squid.conf как доверенную политику. Выбрать восстановление
LKG либо отдельное согласованное изменение модели, затем validate/apply.

## Disk full

Остановить новые apply, проверить log/cache budgets. Удалять только
проверенно disposable/retention-expired данные, не текущий journal/LKG/DB/WAL.
Ошибка записи audit не должна сопровождаться незаметным изменением прав.
Уменьшить нагрузку логов и расширить диск; проверить сохранность DB и runtime.

## Копирование данных

Для текущего одного контейнера достаточно остановленной копии состояния.
Горячий backup API, maintenance mode, поколения архивов и manifest-сервис не нужны.
Процедура ниже — согласованный минимальный план; исполненное упражнение с
копией пользовательской установки не заявляется.

1. Дождаться завершения apply/rollback и остановить единственный контейнер.
   Убедиться, что он действительно остановлен; клиенты на время потеряют proxy.
2. Скопировать named volume /data с сохранением прав/UID. Не выбирать только
   control.sqlite3: сохранить весь app/ вместе с возможными WAL/SHM и runtime/
   с LKG/journal/config. logs/ можно включить в простую полную копию.
3. Сохранить Compose overrides, несекретные параметры запуска и точный image
   ID/digest. Если TLS cert/key подключены из внешних путей (compose.tls.yaml или
   свой override), /data их не содержит: сохранить эти файлы/ссылки отдельно в
   защищённом месте. Архив с ключами защищается принятыми средствами оператора.
4. Запустить тот же контейнер, проверить panel health и proxy request.

Копирование одного SQLite-файла при работающем приложении не является этой
процедурой. Приложение использует WAL, а runtime-файлы изменяются отдельно от DB.
Периодичность и место внешней копии выбирает оператор, панель их не планирует.

## Восстановление копии

1. В отдельном тестовом volume/изолированной сети вернуть /data с прежними правами,
   подключить сохранённые внешние TLS files и параметры. Исходный volume не заменять
   при проверке. Не публиковать восстановленный proxy в пользовательские сети.
2. Запустить совместимый сохранённый image, проверить recovery, вход и реальный
   разрешённый/запрещённый запрос. Откат image не выполняет down migration SQLite.
3. Копия БД содержит прежние session verifiers. Для реального возврата старой копии
   явно отозвать доступ через существующий CLI password-reset; он задаёт новый hash
   и удаляет sessions, не восстанавливает исходный пароль. Отдельная revoke-only
   команда пока не реализована и для первой процедуры не обязательна.

Одного такого упражнения достаточно для первоначальной инструкции. Оно ещё не
проведено; готовые backup/restore команды и заявленное время восстановления не
добавлять до проверки. Автоматизация без остановки потребуется только при отдельной
задаче непрерывной доступности. Текущий приоритет: [roadmap](../plans/roadmap.md).

## Обновление образа/приложения

Новый общий image (Go/UI + Squid + Pebble plan) проходит capability suite,
security scan, migration compatibility и
staging traffic test. Сделать backup; заранее сохранить старый digest и secret refs.
Обновление даже только панели пересоздаёт общий контейнер и может прервать
CONNECT — запланировать окно. Для обычного сбоя Go перезапускать только его
Pebble service, без restart всего контейнера.
Откат image не откатывает SQLite schema автоматически: использовать совместимую
версию либо restore.

## Истекающий/скомпрометированный сертификат

Минимальная замена server cert: проверить key/chain/SAN/срок и права UID 13,
остановить контейнер, заменить файлы/исправить mounts, запустить и проверить
TLS настоящим клиентом. Заранее оставить возможность вернуть прежние файлы.
Горячий reload и автоматический audit файловой замены не реализованы; API metadata
не доказывают, какой сертификат сейчас выдаёт HTTPS listener.
При компрометации server TLS key заменить ключ и сертификат, отозвать старый
сертификат у issuer. См. [lifecycle ключей](../security/authentication-and-certificates.md).

## Визуальные настройки и expert squid.conf

Сохранить форму → просмотреть diff → проверить настоящим `squid -k parse` →
применить. История позволяет выбрать ранее применённую ревизию, посмотреть diff
и отдельно подтвердить откат. Откат не переписывает черновик; неприменённые
изменения остаются видны. Внутренние listeners не редактируются через web/API.
Host publish настраивается при установке через Compose. Служебные loopback-порты
3130/3131 не публиковать.

В expert mode визуальные клиентские политики не действуют; общие настройки
остаются исходными значениями. Helpers/includes/cache_dir и служебные paths
запрещены. При parse failure исправить expert section, создать новую ревизию;
ручная правка active.conf создаёт drift. При degraded после ошибки persistence
сначала устранить проблему хранилища, затем перезапустить control для reconciliation
и проверить runtime status до следующего apply.

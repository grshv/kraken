# Runbooks

Рабочие действия оператора выполняются командой `kraken` внутри контейнера
(`docker exec -it <контейнер> kraken …`). Проверка процессов:

```sh
docker compose -f deploy/compose.yaml ps
docker compose -f deploy/compose.yaml exec kraken pebble services
docker compose -f deploy/compose.yaml exec kraken kraken check
docker compose -f deploy/compose.yaml exec kraken kraken status
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
Задать новый пароль admin командой внутри контейнера:

```sh
docker exec -it <контейнер> kraken password-reset --username admin
```

Команда работает и при остановленной панели, прежние сессии отзываются.
Старый пароль не восстанавливается. См. [пароли и сброс](../security/authentication-and-certificates.md#пароли-и-сброс).

## Пользователи и сессии

Список, блокировка, смена роли и отзыв сессий — через `kraken`:

```sh
docker exec <контейнер> kraken user list
docker exec <контейнер> kraken user disable <имя>
docker exec <контейнер> kraken user role <имя> manager
docker exec <контейнер> kraken sessions revoke --user <имя>
docker exec <контейнер> kraken sessions revoke --yes        # все сессии
```

Блокировка и смена роли отзывают сессии пользователя; последний включённый
administrator защищён от блокировки и понижения. Каждое изменение пишет audit
(actor `cli`).

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

Онлайн-копия без остановки панели выполняется CLI внутри контейнера:

```sh
docker exec <контейнер> kraken backup                      # /data/backups/kraken-<UTC>.tar
docker exec <контейнер> kraken backup --out /data/backups/manual.tar
```

Архив содержит консистентную онлайн-копию `app/control.sqlite3` (через
`VACUUM INTO`, без живого файла и `-wal`/`-shm`), `runtime/**`, `certs/**` и
при наличии `app/telemetry/rollups.db` (также через `VACUUM INTO`). Логи,
месячные файлы событий и WAL sidecar не включаются. Файл принадлежит UID 10013,
права 0600, не перезаписывается. Состав архива и консистентность копии при
параллельной записи проверяются unit-тестами пакета `admin`.

Готовый архив вынести наружу:

```sh
docker cp <контейнер>:/data/backups/kraken-<UTC>.tar ./kraken-backup.tar
```

Для полной копии состояния с остановкой (включая WAL checkpoint и промежуточные
файлы) остался прежний вариант: остановить контейнер (`docker stop` с grace для
штатного shutdown Squid и панели), скопировать весь named volume `/data` с
сохранением прав/UID, запустить тот же контейнер. Это не требует работающей панели
и захватывает `logs/`. Периодичность и место внешней копии выбирает оператор.

## Восстановление копии

1. Создать новый volume и распаковать архив `kraken backup` в `/data` с прежними
   правами/UID (kraken 10013:10013, proxy 13:13). Не публиковать восстановленный
   proxy в пользовательские сети при проверке. Подключить сохранённые внешние
   TLS-файлы и параметры, если они хранились вне `/data`.
2. Запустить совместимый сохранённый image: `docker run -d --name <новый> -v
   <новый-volume>:/data <image>`. Проверить recovery, вход и реальный
   разрешённый/запрещённый запрос. Откат image не выполняет down migration SQLite.
3. Копия БД содержит прежние session verifiers. Для реального возврата старой копии
   явно отозвать доступ: `docker exec -it <новый> kraken sessions revoke --yes`
   или `docker exec -it <новый> kraken password-reset --username <имя>` — последний
   задаёт новый hash и удаляет sessions, не восстанавливает исходный пароль.

Для полной остановленной копии процедура та же: вернуть `/data` целиком с прежними
правами и запустить контейнер. При остановленной копии WAL уже чекпоинчен, Squid
`stale PID` удаляется squid-start перед запуском.

Готовые backup/restore команды заявлены и проверяются unit-тестами; упражнение
восстановления на пользовательской установке отмечается в [status](../status.md)
после проведения.

## Обновление образа/приложения

Новый общий image (Go/UI + Squid + Pebble plan) проходит capability suite,
security scan, migration compatibility и
staging traffic test. Сделать backup (`kraken backup`); заранее сохранить старый digest и secret refs.
Обновление даже только панели пересоздаёт общий контейнер и может прервать
CONNECT — запланировать окно. Для обычного сбоя Go перезапускать только его
Pebble service, без restart всего контейнера.
Останавливать контейнер через `docker stop` (или `docker compose stop`) с grace,
достаточным для штатного shutdown Squid и панели: корректный выход Squid и
чекпоинт WAL панели. Принудительный `docker rm -f`/`docker kill` оставляет
stale PID-файл, но squid-start удаляет его перед запуском, поэтому следующий
старт Squid не падает с `FATAL: Squid is already running`.
Откат image не откатывает SQLite schema автоматически: использовать совместимую
версию либо restore.

## Истекающий/скомпрометированный сертификат

Минимальная замена server cert: проверить key/chain/SAN/срок и права (панель — UID 10013, прокси — группа 13),
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

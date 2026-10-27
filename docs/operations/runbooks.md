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
Различать proxy denied (обычно 403), proxy auth (407, если auth появится),
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

Остановить новые apply, проверить log/cache/cert DB budgets. Удалять только
проверенно disposable/retention-expired данные, не текущий journal/LKG/DB/WAL.
Ошибка записи audit не должна сопровождаться незаметным изменением прав.
Уменьшить нагрузку логов и расширить диск; проверить сохранность DB и runtime.

## Backup

Предлагается ежедневная зашифрованная копия вне proxy VM; периодичность согласуется
с владельцем. Сначала запретить новые apply и дождаться terminal operation либо
войти в maintenance, чтобы DB и runtime bundle относились к одному состоянию.

Копировать: SQLite согласованным backup API, versioned policy/config bundles,
runtime journal/LKG manifest, deployment pins/UID mapping, TLS/CA secrets отдельным
защищённым архивом. Secret export не доступен из браузера. Cache не резервируется.
Sessions в восстановленной DB инвалидировать; сохранённый пароль — только hash.

Backup manifest включает generation ID, hashes, revision ID, schema/image versions
и secret refs. Затем снять maintenance. Проверить decrypt/read/hash и тестовый
restore на изолированной VM; существование архива не является проверкой восстановления.

## Restore

1. Поднять изолированную VM с deny-all/network isolation и нужным image digest.
2. Проверить backup manifest, расшифровать secrets локально, установить права.
3. Восстановить DB, bundles и runtime journal из одной согласованной generation.
   Запустить единый image: recovery gate должен предшествовать старту Squid.
4. Отозвать старые sessions; выполнить миграции только совместимой версией.
5. Сверить applied/LKG/hash/secret refs, parse, start и probes.
6. Проверить разрешённые и запрещённые назначения с клиентской identity.
7. Переключить DNS/маршрут по принятой процедуре; измерить фактическое время.

RPO/RTO не заявляются до упражнения восстановления.

## Обновление образа/приложения

Новый общий image (Go/UI + Squid + Pebble plan) проходит capability suite,
security scan, migration compatibility и
staging traffic test. Сделать backup; заранее сохранить старый digest и secret refs.
Обновление даже только панели пересоздаёт общий контейнер и может прервать
CONNECT — запланировать окно. Для обычного сбоя Go перезапускать только его
Pebble service, без restart всего контейнера.
Откат image не откатывает SQLite schema автоматически: использовать совместимую
версию либо restore. Не удалять старый signer до окончания CA rotation overlap.

## Истекающий/скомпрометированный сертификат

Серверный cert: stage новый key+chain, name/expiry check, reload, TLS probe, audit.
Inspection CA: сначала trust rollout, потом signer switch; при компрометации
удаление доверия у клиентов обязательно. См. [lifecycle ключей](../security/authentication-and-certificates.md).

## Визуальные настройки и expert squid.conf

Сохранить форму → просмотреть diff → проверить настоящим `squid -k parse` →
применить. История позволяет выбрать ранее применённую ревизию, посмотреть diff
и отдельно подтвердить откат. Откат не переписывает черновик; неприменённые
изменения остаются видны. Смена внутреннего порта не меняет Docker publish:
публикацию на host нужно согласовать отдельно. Служебный 127.0.0.1:3130 не публиковать.

В expert mode визуальные клиентские политики не действуют; общие настройки
остаются исходными значениями. Helpers/includes/cache_dir и служебные paths
запрещены. При parse failure исправить expert section, создать новую ревизию;
ручная правка active.conf создаёт drift. При degraded после ошибки persistence
сначала устранить проблему хранилища, затем перезапустить control для reconciliation
и проверить runtime status до следующего apply.

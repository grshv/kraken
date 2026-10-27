# Spike: совместимость ubuntu/squid

Статус: **не выполнен**, контейнеры в ходе проектирования не запускались.
Это первый implementation blocker: выбранный продуктовый образ требует измерений.

## Записать в manifest

Tag, immutable digest, CPU architecture, дата проверки, Squid version/build flags,
base image, Pebble version/plan, service name/command, binary/config/PID paths,
effective UID/GID, writable paths, log format и required capabilities.
Не брать EOL tag из примера Docker Hub README без проверки support table.

## План

1. Получить digest кандидата и проверить доступность/происхождение.
   Официальная страница на дату исследования показывает в том числе beta/edge
   каналы; это не является доказательством production maturity.
2. Прочитать OCI entrypoint и Pebble `plan`, собрать вывод `squid -v`/help
   через проверенный binary path, не предполагая `squid` в PATH.
3. Проверить directory config mount, include/`-f`, atomic rename виден контейнеру,
   требуемые default files image не скрыты.
4. Проверить локальный parse из Go в том же итоговом контейнере: fixed argv,
   candidate paths и отсутствие записи в live PID/cache/log/cert DB.
5. Проверить control command reload и способ доказать новую активную ревизию.
   PID 1 может быть Pebble — сигнал контейнеру не считать Squid reload.
6. Протестировать source IP после Docker networking и все ACL fixtures.
7. TLS proxy: compile TLS library, listener, cert/key permissions, client curl/APT.
8. SSL Bump: OpenSSL support, cert helper, cert DB path/init, bump/splice steps,
   selected VM profile, upstream trust и обходы. Отдельно nested TLS proxy+inspection.
9. Проверить permissions/non-root/read-only rootfs/capabilities без поломки image.
10. Проверить собственный image со встроенным Go/UI: отдельные Pebble services,
    UID/GID, reload без root web, logs/rotation, cache init если нужен.
11. Recovery gate перед каждым стартом Squid: отдельный service restart, restart
    контейнера и reboot VM во время apply. Общий lock не допускает гонки с worker.
12. Падение/перезапуск Go не останавливает Squid и открытый CONNECT. Обновление
    image документированно пересоздаёт оба процесса, без обещания zero downtime.

## Матрица результата

| Возможность | Статус до spike | Доказательство для Supported |
| --- | --- | --- |
| HTTP forward proxy | Unknown | Разрешённая VM получает fake origin |
| HTTPS CONNECT | Unknown | Клиент видит origin cert, deny cases работают |
| Per-source all_web/allowlist | Unknown | Разные identities, domain/subdomain/raw IP fixtures |
| TLS к proxy | Unknown | Валидный TLS succeeds, wrong SAN/CA fails |
| SSL Bump | Unknown | Свой issuer виден клиенту, upstream validation работает |
| TLS proxy + SSL Bump | Unknown | Комбинация проверена настоящим клиентом |
| Safe reload + verification | Unknown | Old/new canary и config fingerprint подтверждены |
| Crash recovery | Unknown | Любой restart Squid/контейнера не запускает pending candidate |

Допустимые значения после проверки: Supported, Unsupported, Needs configuration.
Каждый статус содержит команду/тест, очищенный output и image digest.

## Если возможности нет

Описать, чего не хватает: TLS library, helper, directive/version, client support,
path/permission. Рассмотреть другой поддерживаемый tag Canonical; если нужен
производный image, явно зафиксировать изменение исходного требования в ADR.
Нельзя приписать существующему image поддержку функции по одной Squid документации.

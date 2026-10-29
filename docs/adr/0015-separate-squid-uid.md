# ADR 0015: отдельный UID для Squid

Дата: 2026-10-29. Статус: Accepted по поручению владельца (план работ, срез 2).

## Контекст

Squid и Go работали от `proxy:13:13`. Любая директива Squid, читающая файл, раскрывала
SQLite панели, ключ панели и TLS-ключ прокси (подтверждено на `err_page_stylesheet`).
Allowlist expert-секции закрывает известные пути, но граница держалась на полноте
фильтра. Общий UID нужен был из-за `squid -k reconfigure/rotate` (сигнал по PID) и
proxy-auth helper, который читал SQLite.

Spike 29.10.2026 на Squid 6.13: `kill -HUP` из другого UID даёт EPERM, а запрос
`cache_object://127.0.0.1/reconfigure@<token>` на loopback-listener 3131 перечитывает
конфигурацию из процесса с любым UID.

## Решение

- Go работает как `sccontrol` (`10013:10013`, дополнительная группа `proxy`).
  Squid и его helpers — как `proxy:13:13`.
- Права в volume:

  | Путь | Владелец | Режим | Кто читает |
  | --- | --- | --- | --- |
  | `/data/app` (SQLite, токен manager) | sccontrol:sccontrol | 0700 | только Go |
  | `/data/certs/panel.*` | sccontrol:sccontrol | 0600 | только Go |
  | `/data/certs/proxy.*` | sccontrol:proxy | 0640 | Go, Squid |
  | `/data/runtime` (active.conf, LKG, journal, include, экспорт) | sccontrol:proxy | 0750, файлы для Squid 0640 | Go пишет, Squid читает |
  | `/data/squid` (PID, cache.log) | proxy:proxy | 0750 | Squid пишет |
  | `/data/logs` (access.log) | proxy:proxy | 0750, файлы 0640 | Squid пишет, Go читает через группу |

- Reload и rotate выполняет Go запросом к manager на `127.0.0.1:3131`:
  `cachemgr_passwd <token> reconfigure rotate` из файла
  `/data/runtime/manager-auth.conf`, который подключается фиксированной строкой
  `include`. Токен случайный, хранится в `/data/app`, не попадает в рендер, diff,
  ревизии и API. Доступ к этим действиям — только с loopback на 3131, как у
  read-only отчётов [ADR 0013](0013-read-only-squid-metrics.md); публичные listeners
  manager по-прежнему закрывают.
- Proxy-auth helper ([ADR 0012](0012-local-users-and-proxy-passwords.md)) читает не
  SQLite, а экспорт `/data/runtime/proxy-accounts.json` (0640 sccontrol:proxy) с
  верификаторами включённых proxy accounts. Go перезаписывает его атомарно после
  каждого изменения proxy accounts и при старте.
- Запуск Squid (`squid-start`, UID proxy) больше не пишет состояние: он ждёт, пока
  `active.conf` совпадёт с LKG, а журнал будет завершён или его `previous` совпадёт с
  LKG. Восстановление на диске выполняет `serve` (UID sccontrol) до онлайн-проверки.

## Последствия

- Компрометация Squid или ошибка в expert-секции не раскрывает БД, ключ панели и
  токены сессий. Squid по-прежнему видит Argon2id-верификаторы proxy accounts и
  TLS-ключ прокси — это необходимый минимум.
- Go остаётся в группе proxy и читает логи; изоляции Go от Squid в обратную сторону нет.
- Если Go не запущен и журнал незавершён, Squid не стартует до восстановления.
  Работающий Squid и Squid с завершённым журналом от Go не зависят.
- Существующий volume перед первым запуском нового образа нужно перевести на новых
  владельцев (процедура в runbooks).

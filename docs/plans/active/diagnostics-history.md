# История запросов и аудит

Дата: 2026-10-27. Статус: Completed.

Критерии приёмки:

- Нормализованные события сохраняются в SQLite вместе с checkpoint reader в одной
  транзакции; повтор после рестарта не дублирует записи.
- Reader ограничен по bytes/record, переживает rename rotation, truncation и
  частичные строки; неизвестная потеря источника помечается gap, не замалчивается.
- URL path/query/userinfo не сохраняются; CONNECT остаётся метаданными туннеля.
- История имеет стабильные страницы cursor, фильтр клиента и отказов. Сопоставление
  клиента — уникальный IP/CIDR в политике при сборе, не доказанная историческая ACL.
- Сбор идёт отдельно от запросов API, ошибки не блокируют proxy data plane.
  UI показывает недоступность/устаревание, последнее успешное чтение и пропуски.
- Retention: события 30 дней по времени сбора и максимум 50 000 строк; аудит
  существующие 90 дней/5000 строк. Это лимиты реализации, не обещание полноты.
- Audit API/панель показывают сохранённые действия с actor/action/entity/time,
  постранично и с точными фильтрами, без паролей и session tokens.
- Проверены auth, cursor/filters, ingestion/restart/rotation/partial/oversized,
  retention, escaping/privacy, реальный Squid и браузер desktop/mobile.
- Обновлены API, архитектура, ADR и capability report. Рабочая установка не меняется.

Дизайн: существующие Canvas #F2F6FA, Surface #FFFFFF, Ink #172D45, Action #185FA5,
Danger #B42336; IBM Plex Sans для управления, Mono для адресов. Левая привязка,
фильтры над журналом, свежесть рядом с заголовком, pagination под таблицей.
Акцент — хронология реальных запросов; декоративные метрики не добавляются.

## Проверено

- make check: docs/JSON/refs, web typecheck/build, Go race tests, vet, patch check.
- Reader: restart/resume, retry IDs, partial/oversized/malformed/invalid UTF-8,
  byte budget, rename rotation, truncation/anchor rewrite, временно исчезнувший
  файл без replay. Checkpoint identities ограничены 64, включая отсутствующие.
- Store: rollback общей транзакции при injected checkpoint failure, reopen,
  dedup, client/failure/audit filters, cursor при новых событиях и неверных
  фильтрах/формате, SQL parameter binding, TTL и реальный cap 50 000 строк.
- HTTP: admin-only endpoints, безопасные поля, сохранённые client labels,
  неизвестная revision, source failure/stale и source missing без потери истории.
- make image, real-image smoke на Linux/arm64: настоящие Squid события,
  client/cursor, rotation, restart control, сохранённый audit password-reset.
- Playwright: 5 сценариев прошли. Новая проверка проходит страницы истории,
  фильтры клиента/отказов, audit, literal HTML как текст, сохранение старых строк
  при API 503, mobile overflow. Desktop/mobile screenshots просмотрены.

API 0.4.0, additive migration 003; compiler остаётся v4, live policy не меняется.
Рабочая панель и её volume не обновлялись, проверки используют отдельные fixtures.
Это ограниченная локальная история, не forensic archive. Не реализованы поиск по
времени/назначению, агрегаты, экспорт/alerts, byte quotas/disk watermarks, backup/
restore drill и полная fault/IPv6/DNS matrix. Следующий обязательный этап — backup/
restore согласованного состояния и проверка восстановления с реальным запросом.

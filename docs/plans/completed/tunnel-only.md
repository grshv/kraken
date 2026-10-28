# Сквозной HTTPS

Дата: 2026-10-27. Статус: Completed.

Прямое решение владельца: HTTPS сайтов передаётся сквозным CONNECT.
TLS-подключение к прокси и HTTPS панели сохраняются.

Критерии приёмки:

- Удалены неиспользуемые варианты TLS profiles, поля и планы из UI/API/docs/examples.
- Expert editor не допускает директив ssl_ и security_ для изменения TLS сайтов;
  listeners остаются под управлением compiler.
- Существующие tunnel snapshots читаются; сохранённые runtime bundles продолжают
  работать до следующего apply, пароли и сертификаты существующей установки не меняются.
- Go race/vet, web build, docs check, реальные CONNECT/TLS proxy и browser suite проходят.

Изменение сериализации snapshot учитывает compiler v4. Для прямого apply старой
ревизии нужна новая проверка; rollback компилирует snapshot текущей версией.

Подтверждено: `make check`, `make image`, real-image smoke и Playwright.
Обновлён существующий Compose deployment с сохранённым volume: доверенный HTTPS
для `/healthz` и `/` возвращает 200, `check` успешен, оба Pebble services active.
Учётная запись, пароль и сертификаты не переинициализировались.

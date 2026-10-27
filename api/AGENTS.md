# Область api/

OpenAPI — начальный контракт критического пути, не полная спецификация всех CRUD.
Сохраняйте unique operationId, JSON `$ref`, явную auth и CSRF для mutations.
Внутренний runtime contract не допускает arbitrary path/command/image/URL или keys.
expert_config — явно разрешённая пользовательская секция; guard проверяет её
до parse, она не получает контроль служебных paths и команд.
Изменения API обновляют примеры и соответствующий architectural lifecycle.
Текущая проверка `make docs-check` структурная; полноценный OpenAPI validator
будет добавлен при реализации сервера/генерации клиента.

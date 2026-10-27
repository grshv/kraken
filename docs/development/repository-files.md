# Какие файлы нужны агентской разработке

## Необходимый минимум, уже создан

| Файл/каталог | Зачем |
| --- | --- |
| README.md | Назначение, статус, вход в документацию, реальные команды |
| AGENTS.md | Короткие правила и маршрут к знаниям |
| docs/product/ | Проверяемые требования и продуктовые решения |
| docs/architecture/ | Границы компонентов, политики, lifecycle, модель данных |
| docs/adr/ | Мотивация существенных решений и альтернативы |
| api/openapi.json | Начальный машиночитаемый контракт критического пути |
| api/runtime-contract.md | Внутренние операции применения, без IPC |
| docs/security/ | Угрозы, вход, сессии, сертификаты и CA |
| docs/operations/ | Установка, наблюдаемость, аварии, backup/restore |
| docs/plans/ | Этапы, prerequisites, compatibility spike |
| docs/templates/ | Задача, execution plan, ADR, handoff |
| CONTRIBUTING.md | Понятный путь изменения проекта |
| SECURITY.md | Граница версии и обращение с security reports |
| .gitignore, .editorconfig, .gitattributes | Гигиена, единое форматирование, окончания строк |
| .env.example | Документированные placeholder-переменные, без секретов |
| Makefile, scripts/check_docs.py | Работающая локальная проверка |
| .github/ | PR/issue templates, docs CI без деплоя |
| examples/ | Только синтетические политики и клиентские примеры |

## Уже добавлено первым срезом

`src/go.mod`, `src/go.sum`, `src/internal/`, `src/cmd/`, `src/migrations/`,
`src/web/` с npm lock, `src/tests/integration/`, `src/tools/`, multi-stage
`deploy/Dockerfile`, single-service Compose и `deploy/pebble/`.
Весь исходный код приложения и тестов находится под src/.

## Развивать вместе с кодом

- `go.mod`, `go.sum`: после выбора module path/toolchain/dependencies.
- `web/package.json`, один package-manager lock, TS/Vite config: в UI срезе.
- `migrations/*.sql`: при storage реализации, с checksums и upgrade tests.
- `deploy/compose.yaml`, multi-stage Dockerfile и `deploy/pebble/`:
  после image spike, с реальными командами, правами и boot-recovery проверкой.
- Capability/release manifest: точные digest, architectures, TLS compile flags,
  process paths, helper paths и подтверждённые операции.
- Versioned fixtures и tests: рядом с инвариантом, который они проверяют.
- Scoped `AGENTS.md` в `web/`, `internal/runtime/`, `deploy/`, если правила реально
  отличаются. Не создавать пустую папку только ради ещё одной инструкции.
- Runtime CI: build/lint/unit/integration/e2e/scan по мере появления команд.
- Release notes и changelog: при первом пользовательском релизе.

## Только если есть потребность

`CODEOWNERS` — когда определены реальные владельцы и платформа репозитория.
`LICENSE` — когда владелец выберет условия распространения; автоматически MIT не ставим.
`CLAUDE.md`/Copilot/Cursor adapters — для выбранных инструментов.
`.agents/skills/` — для повторяемой специфичной процедуры, а не копии AGENTS.
MCP config — без credentials, только при реальной интеграции.
Огромный PRD, RACI, отдельный файл на каждый endpoint и генерация всех мыслимых
инструкций не нужны для качественной первой версии.

## Правило одного источника

API shape — OpenAPI; network semantics — policies-and-tls; apply state machine —
configuration-lifecycle; актуальные команды — README/Makefile; важные trade-offs —
ADR; текущий прогресс — roadmap/активный task plan. Другие документы ссылаются
на источник, а не переписывают его целиком. Согласованность проверяется review.

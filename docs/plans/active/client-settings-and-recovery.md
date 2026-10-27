# Клиенты, настройки и надёжное применение

Статус: Completed (в указанном объёме). Дата: 2026-10-27.

Прямые уточнения владельца: VM — только пример; продукт управляет произвольными
клиентами прокси. Исходный пароль никогда не сохраняется в БД, файлах, argv,
env или логах; хранится только salted Argon2id verifier. Нужен локальный CLI reset.
Текущий узкий интерфейс Squid необходимо расширить.

- [x] PHC hash, совместимость прежних hashes, CLI reset и атомарный отзыв сессий.
- [x] Клиенты/адреса/сети/группы без привязки к VM.
- [x] Визуальные настройки Squid, единый revision/parse/apply/rollback.
- [x] История, diff, rollback UI и явные unapplied/degraded состояния.
- [x] Fault tests: реальный SIGKILL тестового процесса и инъекция ENOSPC на записи.
- [x] Документация, API, image/browser/integration verification.

Владелец выбрал визуальные настройки + экспертный squid.conf. ADR 0007 описывает
принятую модель и защитную границу, universal exec не вводится.

Подтверждено 27.10.2026: `make check`, сборка image, real-image smoke и браузерный
сценарий (клиенты/settings/expert/apply/rollback/mobile), Docker target `verify`
с Go race/vet на Linux/arm64. Существующий volume
пользователя и пароль не менялись. Полный диск, reboot VM, backup/restore,
DNS rebinding/IPv6 matrix, UI certificates и scans остаются в общем roadmap.

# Первый рабочий срез

Статус: Implemented, verification documented. Дата: 2026-10-27.

Цель: один собираемый контейнер с Pebble, Squid и Go HTTPS-панелью со static UI.
Критерии: bootstrap без default password; login/session/CSRF; desired snapshot;
preview/parse/apply; неизвестный src deny; all_web/allowlist; независимый restart
Go; persistent config/journal; воспроизводимые проверки и точный capability report.
TLS proxy/inspection включаются только после подтверждения сборки и тестов.

- [x] Измерить реальный ubuntu/squid, pin digest, UID/control и TLS capabilities.
- [x] Go domain/compiler, storage/auth, локальный runtime adapter и recovery gate.
- [x] React/TS интерфейс, встроенный в binary, multi-stage image и Compose.
- [x] Unit/integration/browser проверки, обновление реального статуса документации.

Наблюдение: tag 7.2-26.04_beta из Docker Hub overview не найден registry;
проверяется доступный 6.6-24.04_beta. Это причина не выбирать digest по README.

Результаты и незавершённые возможности: [capability report](../../research/image-compatibility.md).
Исходники перенесены в src/ по прямому указанию владельца.

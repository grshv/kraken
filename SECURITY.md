# Безопасность

Kraken — личный проект без поддерживаемых релизов и отдельного канала сообщений
об уязвимостях. Модель угроз — [docs/security/threat-model.md](docs/security/threat-model.md),
инварианты — в [AGENTS.md](AGENTS.md#инварианты).

Не публикуйте пароли, private keys, cookies, конфигурацию реальной сети и access
logs в issues. Исправление уязвимости описывает затронутую границу, устраняемый
сценарий и regression test без реальных секретов.

`make scan` проверяет зависимости и секреты: govulncheck по Go-коду, SBOM образа,
web-зависимостей и Squid под grype, gitleaks по истории. Принятые находки с причиной —
[deploy/grype.yaml](deploy/grype.yaml) и [.gitleaksignore](.gitleaksignore).
HTTPS сайтов остаётся сквозным: клиент проверяет исходный сертификат сайта.

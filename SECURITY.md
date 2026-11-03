# Безопасность

Kraken — личный проект без отдельного канала сообщений об уязвимостях. Модель угроз —
[docs/security/threat-model.md](docs/security/threat-model.md), инварианты — в
[AGENTS.md](AGENTS.md#инварианты).

Не публикуйте в issues пароли, ключи, cookies, конфигурацию своей сети и access
logs. Исправление уязвимости описывает затронутую границу, устраняемый сценарий и
regression-тест без настоящих секретов.

`make scan` проверяет зависимости и секреты: govulncheck по Go-коду, SBOM образа,
web-зависимостей и Squid под grype, gitleaks по истории. Принятые находки с причиной —
[deploy/grype.yaml](deploy/grype.yaml) и [.gitleaksignore](.gitleaksignore). Образы
публикуются с подписанными provenance и SBOM attestations GitHub.

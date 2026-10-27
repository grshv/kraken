# Требования

Дата: 2026-10-27. Это требования и критерии; фактический статус отдельно в
[capability report](../research/image-compatibility.md). Все примеры синтетические.

## Продукт

Управление Squid через защищённую web-панель. Один собственный контейнер на базе
ubuntu/squid, Pebble и отдельные Squid/Go процессы; UI встроен в Go executable.
Размещение — Linux-узел с Docker, например выделенная VM. Клиентом proxy может
быть устройство, сервер, контейнер, адрес, сеть или именованная группа. VM не
является сущностью продукта и не ограничивает интерфейс.

Клиенты используют явный HTTP/HTTPS forward proxy для доступа к ресурсам,
включая APT/deb. Главная ценность — управление доступом и диагностика.
Часть клиентов имеет весь разрешённый web-доступ, часть — domain allowlist.
Визуальные настройки дополняются экспертной секцией squid.conf.

## Безопасность пароля

Пароль администратора никогда не сохраняется как исходное значение. В DB — только
salted Argon2id PHC verifier, без ключа для обратной расшифровки. Ввод ограниченно
обрабатывается в памяти; пароль не попадает в argv, env, файлы или логи.
Локальный CLI reset задаёт новый пароль, атомарно меняет verifier, отзывает все
sessions пользователя и пишет audit без password/hash. Старый пароль не требуется
и не восстанавливается. Уже начавшийся login не обходит reset.

## Основные сценарии

- Локальный оператор создаёт первого admin без default password и публичной регистрации.
- Администратор добавляет клиента, IP/CIDR или группу, выбирает режим и TLS transport.
- Администратор редактирует порты, DNS/TTL, timeouts, кеш памяти, body limit и log rotation.
- Эксперт управляет пользовательскими ACL/http_access/refresh_pattern/cache_peer
  и другими доступными директивами в защищённом блоке squid.conf.
- Черновик не влияет на трафик. Preview показывает semantic и config diff; validate
  запускает реальный Squid; apply проверяет наблюдаемый fingerprint, при сбое откатывает.
- История показывает применённые ревизии; rollback сначала показывает сравнение,
  затем повторно проверяет конфигурацию и применяет её отдельной операцией.
- Оператор видит source IP, destination, observed proxy result и ограничения данных.
- Reset пароля выполняется из CLI, включая остановленную панель, без раскрытия старого.

## Режимы конфигурации

Structured: source identities и их all_web/allowlist компилируются из форм.
Expert: пользовательская squid.conf-секция заменяет визуальные правила доступа;
они сохраняются для возврата, но не действуют. Это явно показано в UI.
Settings общие как defaults; экспертные обычные директивы могут их переопределять.
Финальный полный конфиг доступен для просмотра. Runtime paths, managed listeners,
sc_ namespace, includes и executables не передаются текстовому редактору.
Граница и остаточные риски описаны в [ADR 0007](../adr/0007-general-clients-expert-configuration.md).

## TLS и ограничения

HTTPS панели обязателен. TLS к proxy и обычный HTTPS CONNECT — отдельные слои.
HTTPS сайтов передаётся только через сквозной CONNECT. Клиент проверяет исходный
сертификат сайта; прокси не получает URL-путь, заголовки и содержимое HTTPS.
Кеширование применяется только к обычному HTTP с учётом HTTP semantics.

Forward proxy не заменяет VPN, IP-маршрутизатор, DLP и запрет direct egress.
Source IP не идентифицирует человека; NAT может скрывать разные устройства.
Для обязательного proxy нужен firewall/SG, включая IPv6/UDP обходы.
По умолчанию private destinations закрыты. Включение внутренних сервисов явно
разрешается настройкой, loopback/link-local/metadata остаются защищены.
Экспертные peers/ICAP требуют дополнительного сетевого контроля.

## Критерии приёмки

| ID | Сценарий | Результат |
| --- | --- | --- |
| AC-01 | Неизвестный source в structured mode | Отказ, без обращения к цели |
| AC-02 | all_web клиент | Работает доступ на разрешённые ports с защитными запретами |
| AC-03 | allowlist клиент | Разрешённый domain доступен, чужой и raw IP запрещены |
| AC-04 | Invalid policy/config | Активная ревизия не меняется |
| AC-05 | Reload/verify failure | Verified rollback либо явное degraded |
| AC-06 | Concurrent draft writes | Stale version получает conflict |
| AC-07 | Падение только Go | Squid и открытый CONNECT продолжают работу |
| AC-08 | Loopback/link-local/metadata | Отказ во всех режимах |
| AC-09 | Нет session/CSRF/Origin | Сервер отклоняет mutation |
| AC-10 | HTTPS tunnel diagnostics | Нет выдуманных URL-path/origin status |
| AC-11 | TLS proxy | Сертификат проверяется, plaintext downgrade закрыт |
| AC-12 | Сквозной HTTPS | Исходный сертификат сайта сохраняется, содержимое туннеля недоступно прокси |
| AC-13 | Backup restore | Согласованы DB/LKG/secret refs и реальный запрос |
| AC-14 | Отключение клиента | Новые запросы закрыты, открытые tunnels объяснены |
| AC-15 | CLI password reset | Только новый hash, sessions revoked, old login не выпускает session |
| AC-16 | Settings/expert mode | Изменяется фактическая конфигурация, а не только UI |
| AC-17 | History/rollback | Сравнение, повторный parse/verify и отдельный audit/operation |
| AC-18 | Crash/write failure | Candidate не становится рабочим без подтверждения |

## Пределы первой поставки

Один узел/контейнер не даёт HA; image upgrade прерывает оба процесса.
Backup/restore, полная IPv6/rebinding/TLS matrix, helpers/auth provisioning,
дисковый cache, роли/OIDC и внешние alerts развиваются отдельно.
Обещание «весь Squid поддержан» недопустимо: возможности зависят от версии,
сборки, provisioning и реальных проверок.

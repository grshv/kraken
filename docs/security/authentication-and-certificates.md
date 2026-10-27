# Аутентификация и сертификаты

## Вход в панель

Первая версия — локальные персональные admin-учётные записи. Первая создаётся
локальной CLI-подрежимом будущего Go-бинарника внутри контейнера, через stdin/TTY, без пароля в argv,
истории shell и `.env`. Пока пользователя нет, панель не открывает публичную
регистрацию. Команда bootstrap будет документирована после реализации.

Пароль хранится Argon2id с salt из CSPRNG. Стартовый минимум OWASP — 19 MiB,
2 iterations, parallelism 1; фактический профиль калибруется на VM и ограничивается
budget/rate limit. Пароли не обрезать молча; ограничить максимальную длину запроса.
Проверенный пакет hashing выбирается отдельно; собственную криптографию не писать.
[OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

Сессия — случайный opaque token, сервер хранит verifier hash; отдельный CSRF token.
Cookie `__Host-session; Secure; HttpOnly; SameSite=Strict; Path=/`, без Domain.
Rotation после login и повышения прав; logout, disable user и reset password
отзывают сессии серверно. Предварительные сроки: idle 30 min, absolute 12 h.
Не хранить bearer token в localStorage. Credentials не попадают в access log.
[OWASP Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html).

Все mutations, включая logout, защищены CSRF token и проверкой Origin.
Login также защищён от login CSRF; разрешённый Origin фиксируется deployment.
SameSite — дополнительная защита. Серверная авторизация обязательна для каждого
endpoint; скрытая кнопка в UI не является контролем доступа.
[OWASP CSRF](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).

Login ограничивает частоту по account и источнику, с bounded backoff и общим
сообщением об ошибке. Не вводить бессрочный lockout, позволяющий DoS admin account.
Пароль не восстанавливается из хэша. Локальный оператор задаёт новый через
CLI password-reset, с audit и атомарным отзывом сессий.
Подробности: [password lifecycle](password-lifecycle.md).

## Серверные сертификаты

Панель и HTTPS proxy могут использовать инфраструктурную PKI, отдельные leaf
сертификаты и SAN соответствующих DNS-имён. Это не signing CA для SSL Bump.
Private keys поставляются вне Git, файлы с узкими правами; backup зашифрован.
UI показывает subject, SAN, issuer, fingerprint, notAfter и статус доверия,
но не отдаёт private key. До истечения предупреждение, например за 30/14/7 дней.

При ротации: проверить key/cert match, chain, SAN, срок; stage; parse; reload;
клиентский TLS probe с проверкой имени; сохранить возможность отката.
Не добавлять `curl -k`, `Verify-Peer false` и другие insecure defaults в инструкции.
Раздавать публичный CA можно, private key — никогда.

## Signing CA для SSL Bump

Доверие к inspection CA позволяет выпускать сертификаты сайтов, которым будут
доверять выбранные VM. Это отдельный высокочувствительный актив.
Рекомендуется выделенный intermediate issuer, root offline и ограниченная зона
применения. Ограничения intermediate должны быть совместимы с ожидаемым bump;
нельзя обещать, что произвольный name constraints подходит для всего интернета.

Provisioning выполняется локально, вне web API первой версии. Панель назначает
проверенный profile и показывает metadata/cert export. CA signing key доступен
только Squid/helper и локальному provisioning, насколько позволяет проверенная
схема UID/GID. Для локального parse проверить необходимость доступа к ключу.
Runtime module и HTTP handlers — один Go-процесс: если validation требует чтения
signing key, скрыть его от web-кода внутри этого процесса невозможно. Такое
расширение прав явно фиксируется compatibility/security review, не маскируется
внутренним интерфейсом. Ключ панели, напротив, нужен Go для собственного HTTPS.
Не передавать CA key в универсальный endpoint upload/config.

План включения:

1. Проверить capabilities образа, helper и безопасную cert DB.
2. Создать issuer и записать public fingerprint в deployment manifest.
3. Установить публичную цепочку доверия на выбранные VM и application stores.
4. Проверить origin validation, APT, curl, pinned applications, mTLS и bypass.
5. Назначить inspect-профиль явно и применить с возможностью rollback.
6. Следить за сроком, disk use cert DB и отказами handshake.

Ротация CA двухфазная: сначала новый public CA в trust stores рядом со старым,
затем смена signer на proxy, проверка, завершение старых TLS sessions, и только
после этого удаление старого CA у клиентов. Откат остаётся возможным в overlap.
При компрометации — остановить inspection, удалить доверие, заменить issuer,
проанализировать доступ и уведомить владельца инфраструктуры по принятому каналу.

## Конфиденциальность логов

В tunnel режиме не придумывать содержимое HTTPS. В inspect режиме URL, заголовки
и тела могут содержать credentials и персональные данные; по умолчанию не писать
query strings, Authorization, cookies и payload. Даже путь может содержать секрет:
для инспектируемого трафика предпочтительно хранить host и безопасную категорию,
расширенные детали — только с явным диагностическим режимом и коротким retention.

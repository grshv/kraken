# Аутентификация и сертификаты

## Вход в панель

Первая версия — локальные персональные admin-учётные записи. Первая создаётся
локальным CLI-подрежимом Go-бинарника внутри контейнера, через stdin/TTY, без пароля в argv,
истории shell и `.env`. Пока пользователя нет, панель не открывает публичную
регистрацию. Команда: `make bootstrap` из корня репозитория.

Пароль хранится Argon2id с salt из CSPRNG. Стартовый минимум OWASP — 19 MiB,
2 iterations, parallelism 1; фактический профиль калибруется на VM и ограничивается
budget/rate limit. Пароли не обрезать молча; ограничить максимальную длину запроса.
Проверенный пакет hashing выбирается отдельно; собственную криптографию не писать.
[OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

Авторизованная сессия — случайный opaque token, сервер хранит verifier hash;
отдельный CSRF token.
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
На практике `httpapi` держит две независимые карты throttle: per-(host, username)
с порогом 5 попыток/мин (защита от перебора с одного IP) и per-account с порогом
20 попыток/мин (защита от распределённого перебора, когда атакующий вращает source
IP). Cross-account перебор с одного IP ловится первым счётчиком. Параллелинг
Argon2id ограничен семафором из 2 слотов. Каждая карта использует sliding window
из отдельных timestamps, а не fixed 1-minute window — это закрывает границу
окна, где атакующий мог успеть сделать 5 запросов в 0:59 + 5 в 1:01 = 10 за
2 секунды.

Анонимная форма входа получает подписанный CSRF holder на 10 минут без записи
в БД. HMAC-SHA256 привязывает cookie к nonce, expiry и ключу текущего процесса;
CSRF token выводится отдельно с другим назначением. Рестарт требует обновить
holder. Анонимный поток не занимает пул авторизованных сессий (потолок 256).
Capacity audit выполняется в той же транзакции, что и проверка пула.
При 401 защищённого GET или mutation UI получает новый holder через GET /session
и возвращает форму входа без перезагрузки. Неверный пароль сохраняет holder для
повторной попытки. Подробности: [ADR 0010](../adr/0010-session-and-recovery-safety.md).

Пароль не восстанавливается из хэша. Локальный оператор задаёт новый через
CLI password-reset, с audit и атомарным отзывом сессий.
Подробности: [password lifecycle](password-lifecycle.md).

### Параметры Argon2id

`PasswordHash` использует Argon2id `m=19456 KiB, t=2, p=1`, 16-байтный salt из
`crypto/rand`, 32-байтный output; PHC-формат хранится в `users.password_hash`.
Verifier в `passwordMatches` сужает допустимые параметры PHC: `memory` до 38912
KiB (2× номинал), `iterations` ≤ 4, `parallel` ≤ 2. Это закрывает OOM-вектор,
когда злоумышленник с доступом к БД подсовывает легаси verifier с аномальным
`memory=262144` и заставляет панель выделять 256 MiB на одну попытку входа.

## Серверные сертификаты

Панель и HTTPS proxy могут использовать инфраструктурную PKI, отдельные leaf
сертификаты и SAN соответствующих DNS-имён.
Private keys поставляются вне Git, файлы с узкими правами; backup зашифрован.
UI показывает subject, SAN, issuer, fingerprint, notAfter и статус доверия,
но не отдаёт private key. До истечения предупреждение, например за 30/14/7 дней.

При ротации: проверить key/cert match, chain, SAN, срок; stage; parse; reload;
клиентский TLS probe с проверкой имени; сохранить возможность отката.
Не добавлять `curl -k`, `Verify-Peer false` и другие insecure defaults в инструкции.
Раздавать публичный CA можно, private key — никогда.

## Конфиденциальность логов

В HTTPS CONNECT доступны домен и порт, результат туннеля, длительность и счётчики
байтов. URL-путь, заголовки и тела HTTPS не доступны. Для обычного HTTP query
strings, Authorization, cookies и payload не сохранять; даже путь может содержать
секрет. Расширенные диагностические данные требуют короткого retention.

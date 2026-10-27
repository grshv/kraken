# ADR 0012: локальные роли и парольные клиенты proxy

Дата: 2026-10-27. Статус: Accepted по прямым уточнениям владельца.

## Решение

Панель имеет две роли: admin и viewer. Admin управляет конфигурацией, применением,
учётными записями панели и proxy. Viewer читает состояние, события и аудит;
черновики, экспертный текст, полные ревизии и административные endpoints закрыты
серверными permissions. Скрытая кнопка не является проверкой доступа.
Изменение role/enabled/password атомарно отзывает sessions. Последнего активного
admin нельзя отключить или понизить. Существующие users при migration 004 становятся
включёнными admin без замены password hash или исходного пароля.

Proxy accounts — отдельные username/hash/enabled, не доступ в панель. Migration
005 добавляет только нужную таблицу. Пароли используют существующий Argon2id PHC;
plaintext не сохраняется в snapshots/config/argv/env/logs и не возвращается API.
Basic credentials передаются только через TLS listener Squid. Client может
связать username с IP/CIDR и своей политикой, успешный вход не отменяет ACL.
Существующие IP-only клиенты сохраняют поведение. Для общих source ranges за NAT
auth identities должны однозначно различаться.

Squid получает только управляемую фиксированную команду helper-подрежима того же
Go executable и установленный путь DB. Это ограниченное исключение для auth,
не arbitrary helper/exec из expert editor. Helper читает отдельные verifier hashes
из локальной DB, не выполняет migrations и не является новым сервисом/контейнером.
Basic TTL установлен в 1 секунду: TTL=0 в проверенном Squid 6.13 не позволяет
положительной проверке оставаться действительной и зацикливает аутентификацию.
После Basic helper возвращает opaque generation SHA-256 от verifier, без самого
hash или пароля. Отдельный фиксированный --state режим того же executable
проверяет username/generation/enabled из DB на каждом разрешаемом запросе через
managed external ACL без кеша. ACL положительный в allow rule: ошибка/overload
не открывает доступ. Клиент не задаёт generation annotation HTTP-заголовком.
Смена пароля/блокировка меняет допустимость generation сразу, даже когда Squid
использует закешированные Basic credentials. Начатый до изменения запрос может
завершиться; уже открытый CONNECT не разрывается принудительно.
Expert mode с auth-bound clients отклоняется, пока не определена безопасная модель
совместного действия, чтобы expert ACL не обходил identity restrictions.

## Проверка и ограничения

Проверить миграцию, авторизацию каждого API, last-admin guard, session revoke и
legacy/PHC login. На настоящем Squid: корректный TLS login, неверный/отсутствующий
пароль, запрет plaintext, чужая identity и destination, reset/disable без reload.
Уже открытый CONNECT не обещает немедленного закрытия при блокировке учётной записи.
Role matrix подтверждена владельцем, mTLS и дополнительные роли не входят в объём.

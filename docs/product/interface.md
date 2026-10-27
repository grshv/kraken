# Web-интерфейс

Закрытая русскоязычная панель Squid Control. Аудитория — администратор proxy.
VM — пример клиента; интерфейс не управляет гипервизором и не требует виртуализации.

## Визуальное направление

Диспетчерская сети: состояние пути клиент → Squid → ресурс, правила, отклонения
и действия администратора. Главный экран показывает таблицу клиентов и реальные
события; статистические графики без источника данных не рисуются.

| Роль | Цвет |
| --- | --- |
| Canvas | #F2F6FA |
| Surface | #FFFFFF |
| Ink | #172D45 |
| Action | #185FA5 |
| Healthy | #16664C |
| Warning | #89520B |
| Danger | #B42336 |

IBM Plex Sans/Mono поставляются локально с лицензиями; внешний font CDN не нужен.
Типографика 15–16 px основной текст, 22/30 px headings, 13–14 px code.
Цвет дублируется текстом/значком; focus виден с клавиатуры. На узком экране
навигация переносится, таблицы прокручиваются внутри своего контейнера.

## Обзор

Состояния Squid и внутреннего модуля управления различаются. Время наблюдения
подписано, unknown не показывается зелёным. Внешняя доступность не объявляется
проверенной только по running container. Баннер «Есть неприменённые изменения»
ссылается на diff; degraded запрещает новые runtime mutations.
Путь соединения, таблица клиентов, затем ограниченный хвост событий.

## Клиенты

Запись имеет имя, type host/network/group, один или несколько IP/CIDR, enabled,
политику и TLS-profile. Это источник трафика или именованная группа источников,
а не обязательно отдельная машина. Bare IP сервер нормализует в /32 или /128.
Пересекающиеся enabled source ranges запрещены, чтобы порядок записей не расширял
доступ неожиданно. Иерархические/пересекающиеся группы пока не моделируются.

Режимы all_web и allowlist, domain/subdomain entry, текущий transport и enabled.
Сохранение формы меняет draft, а не трафик. При expert mode виден явный баннер:
визуальные client rules сохранены, но не участвуют в генерации.

## Настройки Squid

Вкладки:

- Сеть и ACL: внутренние HTTP/TLS ports, Safe_ports, CONNECT ports, private
  destinations, body limit и количество архивов логов.
- Кеш: включение HTTP memory cache, cache_mem, max object in memory.
  HTTPS CONNECT не кешируется; cache_mem не равен лимиту всего процесса.
- DNS/timeouts: nameservers, positive/negative TTL, connect/read/request timeouts.
- Экспертный режим: переключатель structured/expert, редактор пользовательской
  секции squid.conf, загрузка исходных visual rules без потери прежнего draft.
- Возможности: реализованное, требующее provisioning и недоступное данной сборке.

Изменение портов внутри контейнера не меняет Docker port publication автоматически.
3130 — loopback-only diagnostic listener, 8443 — panel; они зарезервированы.
Обычные экспертные directives могут переопределять defaults. Правила cache из
редактора предшествуют завершающему rule глобального cache-переключателя.

PID/log paths, includes, executables/helpers, sc_ namespace, managed listeners
и disk cache paths без подготовки не открываются редактору. UI объясняет это
вместо обещания полного покрытия всех Squid directives. Полный результирующий
файл показывается read-only в preview, не подменяется скрытым raw exec.

## Изменения и история

Поток: сохранить draft → подготовить revision → semantic/config diff → validate
реальным Squid → apply → verify. Новый draft инвалидирует предыдущий preview.
Config diff bounded и может показывать крупные changed blocks; это не минимальный
Myers diff. При усечении есть явная строка. Credentials внешних peers, вписанные
экспертом, делают preview чувствительным: он доступен только авторизованному admin.

History содержит последние 100 revision summaries, время, status/current и доступность
отката. «Сравнить и откатить» сначала открывает diff, затем отдельная кнопка
запускает rollback с повторным parse/reload/verify. Оригинальная revision immutable;
применение старого snapshot может быть скомпилировано текущим compiler.

Статусы accepted/validating/reloading/validated/succeeded/rejected/rolled_back/degraded
различаются. Успешный reload exit code не считается доказанным результатом.
Потеря DB outcome блокирует новые operations до reconciliation.

## События

Время с timezone, source IP, destination, method, Squid result и доступные счётчики.
Текст экранируется; URL не исполняется как HTML, query/userinfo отбрасываются.
Для CONNECT origin path/status неизвестны. Точное совпавшее правило не угадывается.
Раздел «События» показывает сохранённую историю, фильтр клиента/отказов и кнопку
«Раньше». «Последние записи» возвращает live page с обновлением каждые 10 секунд;
при просмотре старых страниц автообновление приостановлено. Время включает timezone.
Клиент подписан по IP при сборе, не как доказанная историческая ACL/revision.
Source unavailable, stale reader, backlog, dropped records и gap видны отдельно.
Ошибка обновления сохраняет прежние строки с предупреждением об устаревании.
События ограничены 7 днями/50 000 строк; поиск по time/destination добавляется
при реальной потребности поверх существующей БД.

Раздел «Аудит»: участник, действие, объект и время; точные фильтры actor/action,
постраничная история с тем же управлением. Запись apply означает принятие действия,
успешность подтверждается operation/history. Лимиты: 90 дней/5000 строк.

## Пароль и сертификаты

Login без публичной регистрации, default password и localStorage token.
Пароль не показывается и не выдаётся из DB. Сброс — local CLI password-reset,
со скрытым вводом и отзывом sessions, а не web-ссылка на восстановление старого.
Сертификаты панели и TLS listener прокси — отдельные сущности; private keys не
экспортируются через API. Certificate metadata endpoint есть; отдельная полная
страница metadata/expiry может быть добавлена при необходимости. Файловая замена
сертификата с остановкой контейнера достаточна; web provisioning и hot rotation
не входят в обязательный план.

## Проверки интерфейса

Hermetic browser fixture создаёт отдельный container/volume и случайный password
только в памяти. Проверяются login, клиент, настройки, expert template, apply,
history/rollback, desktop/mobile overflow и logout; runtime data не имитируются.

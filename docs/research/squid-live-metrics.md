# Live metrics: Squid 6.13/GnuTLS

Дата: 2026-10-28. Исследован отдельный disposable контейнер pinned
`ubuntu/squid:6.6-24.04_beta@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029`,
Linux/arm64, Squid 6.13 с GnuTLS. Рабочий volume приложения не подключался.

GET `/squid-internal-mgr/info`, `counters`, `5min`, `active_requests` действительно
работают без squidclient. Read-only path/method/source/localport ACL и
`cachemgr_passwd none <reports>` + `disable all` проходят parse/reconfigure.

На первом измерении uptime был 4.136 s, CPU time 0.047 s, CPU usage 1.14%,
Maximum Resident Size 110512 KB, Total accounted 0 KB. Это синтетическая проверка
формата, не baseline потребления ресурсов приложения. Ноль accounted не означает
нулевое использование RAM. Info не содержит текущую RSS.

`info` вернул HTTP requests 0, следующий `counters` — client_http.requests=1:
manager polling попадает в client statistics. Поэтому API не публикует client
counters/rates как трафик пользователей: используются `server.all`. Byte counters
в reports в KiB, API переводит в bytes умножением на 1024. Это округлённые
счётчики, не точность до отдельного байта.

В первом `5min` sample_start_time совпал с sample_end_time и rates были 0;
API возвращает rates null до непустого интервала. Это скользящее окно Squid,
а не мгновенная скорость. Cache ratio из info относится к запросам Squid,
включая диагностические; HTTPS внутри CONNECT не кешируется.

`active_requests` включил свой manager запрос как Connection block, URI и username.
Parser оставляет только количество, исключая служебные запросы; raw данные
не попадают в API/SQLite/log. Number of clients accessing cache не используется:
это другой показатель, а не число активных соединений.

Источники: [Squid cachemgr_passwd](https://www.squid-cache.org/Doc/config/cachemgr_passwd/),
[официальный HTTP manager access](https://wiki.squid-cache.org/Features/CacheManager/SquidClientTool),
[reports menu](https://wiki.squid-cache.org/Features/CacheManager/Menu).

Дополнительный native smoke на том же изолированном Squid: синтетический HTTP
origin дал +1 server.all.requests и +28 KiB server.all.kbytes_in. Открытый CONNECT
показал два Connection blocks: туннель и сам active_requests report. Повторные
info/5min/counters без трафика дали +0 origin requests. Все шесть проверенных
manager reports (`info`, `counters`, `5min`, `active_requests`, `shutdown`, `config`)
на public proxy listener вернули 403 даже при разрешённом обычном HTTP трафике.
Это исследование reports/ACL; полный application integration дополнительно
проверяет renderer и API (см. `src/tests/integration/smoke.py`).

В полном конфиге с первым canary listener 3130 Squid переписывает authority
URI active_requests report на `kraken:3130`, хотя local socket остаётся
`127.0.0.1:3131`. Отдельный native повтор подтвердил это для восьми последовательных
Go polls. Классификация диагностики использует local socket + fixed report path,
а не port из URI. Idle Connection blocks без request URI не считаются активными
запросами. Список соединений и число запросов — разные показатели.

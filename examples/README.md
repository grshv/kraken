# Примеры

Все адреса и имена синтетические; `.test` не является рабочим зеркалом.
[policy.json](policy.json) — пример снимка конфигурации (схема 2): клиенты с режимом
доступа, политика — набор доменов, TLS-профили. Его формат проверяет `make docs-check`.
Инструкции подключения для разных систем — в справке панели.

## APT через HTTP-прокси

`/etc/apt/apt.conf.d/90-kraken`:

```text
Acquire::http::Proxy "http://proxy.example.test:3128/";
Acquire::https::Proxy "http://proxy.example.test:3128/";
```

`https::Proxy` относится к HTTPS-**репозиторию**: APT открывает `CONNECT` через
незашифрованное подключение к прокси, затем TLS к репозиторию. Для TLS **к прокси**
нужен `https://proxy.example.test:3129/` и доверенный CA установки; проверьте это на
своей версии APT. Сценарий проверяет `TestApt` (`make integration`).

Документация: [APT HTTP](https://manpages.debian.org/bookworm/apt/apt-transport-http.1.en.html),
[APT HTTPS](https://manpages.debian.org/trixie/apt/apt-transport-https.1.en.html).

## curl через TLS-прокси

```sh
curl --proxy https://proxy.example.test:3129 \
  --proxy-cacert /path/to/kraken-ca.crt \
  https://packages.example.test/
```

`--proxy-cacert` проверяет сертификат прокси; сертификат сайта curl проверяет по
системному хранилищу. Ни одна проверка не отключается для удобства
([curl manual](https://curl.se/docs/manpage.html)).

Не переводите источники APT на HTTP и не отключайте проверку подписей пакетов.
`HTTP_PROXY`, `HTTPS_PROXY` и `NO_PROXY` приложения понимают по-разному; APT,
`sudo`, systemd и контейнеры проверяйте отдельно.

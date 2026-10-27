# Примеры

Все адреса и имена синтетические, `.test` не является рабочим mirror.
[policy.json](policy.json) иллюстрирует разные политики VM без отражения реального
числа клиентов. Это snapshot будущего API, не готовый input существующей CLI.
Профиль TLS не утверждает, что выбранный image уже поддерживает его.

## APT через обычный HTTP proxy с HTTPS origin

Будущий UI выдаёт содержимое файла `/etc/apt/apt.conf.d/90-squid-control`:

```text
Acquire::http::Proxy "http://proxy.example.test:3128/";
Acquire::https::Proxy "http://proxy.example.test:3128/";
```

`https::Proxy` здесь относится к HTTPS **ресурсу**. HTTP URI proxy означает
CONNECT через незашифрованное до proxy подключение, затем TLS к origin.
Для TLS **к proxy** нужен `https://proxy.example.test:3129/`, доверенный серверный
CA и проверка именно установленной версии APT; это не автоматически совместимо
со всеми приложениями. Для inspect дополнительно доверяется signing CA сайтов.

[APT HTTP](https://manpages.debian.org/bookworm/apt/apt-transport-http.1.en.html),
[APT HTTPS](https://manpages.debian.org/trixie/apt/apt-transport-https.1.en.html).

## curl и разные CA

Пример после настройки настоящих DNS, listener и CA (пути необходимо заменить):

```sh
curl --proxy https://proxy.example.test:3129 \
  --proxy-cacert /path/to/proxy-server-ca.pem \
  https://packages.example.test/
```

`--proxy-cacert` проверяет proxy server. В inspect-профиле доверие к подменённому
сертификату origin задаётся системным trust store либо отдельно `--cacert` с
inspection CA. Это разные проверки; ни одна не отключается для удобства.
См. [официальный curl manual](https://curl.se/docs/manpage.html).

Не менять источники APT на HTTP и не отключать подписи пакетов ради кеша.
Переменные `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` поддерживаются приложениями
по-разному; APT, sudo, systemd и контейнеры проверяются отдельно.

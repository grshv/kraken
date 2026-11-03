---
slug: connect-lang
title: pip, npm, Go, Maven
section: Подключение машин
order: 40
screen: clients
---

Некоторые менеджеры пакетов читают переменные окружения по-своему или
игнорируют их. Адрес и порт прокси показаны в шапке панели состояния.

## pip и uv

pip и uv читают `http_proxy`/`https_proxy`. Доверие HTTPS-репозиториям
проверяет клиент; прокси не расшифровывает трафик.

```
export http_proxy="http://<kraken-host>:<port>"
export https_proxy="http://<kraken-host>:<port>"
pip install -r requirements.txt
```

Политика клиента должна разрешать `pypi.org` и `files.pythonhosted.org`.

## npm, yarn, pnpm

npm читает `npm_config_proxy` и `npm_config_https_proxy`, либо
`http_proxy`/`https_proxy`. Удобно задать в проекте:

```
npm config set proxy http://<kraken-host>:<port>
npm config set https-proxy http://<kraken-host>:<port>
```

Политика должна разрешать `registry.npmjs.org` и домены используемых
реестров.

## Go modules

`go mod` использует `GOPROXY` и пропускает трафик через `http_proxy`/
`https_proxy`. Для публичных модулей:

```
export http_proxy="http://<kraken-host>:<port>"
export https_proxy="http://<kraken-host>:<port>"
export GOPROXY=https://proxy.golang.org,direct
go mod download
```

`direct` в `GOPROXY` означает, что при недоступности proxy Go идёт к
источнику напрямую; не задавайте `direct` первым, если политикой прямой
egress запрещён. Домены: `proxy.golang.org`, `storage.googleapis.com`.

## Maven и Gradle

Maven читает `settings.xml`. В `~/.m2/settings.xml`:

```xml
<settings>
  <proxies>
    <proxy>
      <id>kraken</id>
      <active>true</active>
      <protocol>http</protocol>
      <host><kraken-host></host>
      <port><port></port>
      <nonProxyHosts>localhost|127.*</nonProxyHosts>
    </proxy>
  </proxies>
</settings>
```

Для `https` повторите блок с `<protocol>https</protocol>` и тем же
HTTP-адресом прокси. Gradle читает `gradle.properties`:
`systemProp.http.proxyHost=<kraken-host>`, `systemProp.http.proxyPort=<port>`.

## Типичные домены для политики

Сводку доменов для типичных репозиториев смотрите в
[Разрешить репозиторий пакетов](help:scenario-allow-repo).

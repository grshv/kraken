---
slug: connect-macos
title: macOS
section: Подключение машин
order: 60
screen: clients
---

Системные настройки сети и терминал настраиваются отдельно. Адрес и порт
прокси показаны в шапке панели состояния.

## Настройки сети

«Системные настройки» → «Сеть» → выбрать интерфейс → «Подробнее» →
«Прокси». Включите «Веб-прокси (HTTP)» и «Защищённый веб-прокси (HTTPS)»,
укажите `<kraken-host>` и `<port>`. Эти настройки используют браузер и
приложения, читающие системную конфигурацию.

То же через терминал:

```
sudo networksetup -setwebproxy "Wi-Fi" <kraken-host> <port>
sudo networksetup -setsecurewebproxy "Wi-Fi" <kraken-host> <port>
sudo networksetup -setproxybypassdomains "Wi-Fi" "localhost,127.0.0.1,::1"
```

Имя интерфейса — `networksetup -listallnetworkservices`. Проверьте:

```
networksetup -getwebproxy "Wi-Fi"
```

## Терминал и Homebrew

Системные настройки не наследуются оболочкой. В сессии:

```
export http_proxy="http://<kraken-host>:<port>"
export https_proxy="http://<kraken-host>:<port>"
export no_proxy="localhost,127.0.0.1,::1"
```

Добавьте строки в `~/.zshrc` для постоянства. Homebrew `brew` читает эти
переменные; `brew update` обращается к `github.com` и
`*.githubusercontent.com` — разрешите их в политике клиента.

## Доверие сертификату

Для подключения по TLS корневой CA прокси добавьте в «Связку ключей»
(Keychain Access) и отметьте как доверенный для систем. Раздавать
приватный ключ нельзя. Подробнее — в
[TLS-прокси с логином](help:connect-tls).

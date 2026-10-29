---
slug: connect-env
title: Переменные окружения
section: Подключение машин
order: 20
---

Большинство утилит Linux (curl, wget, git, apt через конфиг) читают
переменные `http_proxy`, `https_proxy` и `no_proxy`. Адрес и порт прокси
показаны в шапке панели состояния.

## Для текущей сессии

```
export http_proxy="http://<kraken-host>:<port>"
export https_proxy="http://<kraken-host>:<port>"
export no_proxy="localhost,127.0.0.1,::1"
```

`https_proxy` указывает на HTTP-адрес прокси: HTTPS-запрос пойдёт через
CONNECT, прокси не расшифровывает трафик.

## Для всех пользователей: /etc/environment

```
http_proxy="http://<kraken-host>:<port>"
https_proxy="http://<kraken-host>:<port>"
no_proxy="localhost,127.0.0.1,::1"
```

PAM подставит переменные в сессии при следующем входе. Перечитайте профиль
или перевойдите в систему.

## Исключения no_proxy

`no_proxy` — список адресов, которые идут напрямую, минуя прокси. Вносите
туда loopback и локальные сервисы; не вносите внешние домены, иначе клиент
обойдёт политику Kraken. Прокси закроет прямые обращения к private и
loopback-адресам независимо от клиента, но обход внешнего egress должен
контролироваться сетевым firewall.

## Systemd-сервисы

Systemd не читает `/etc/environment`. Для сервиса создайте drop-in:

```
sudo systemctl edit UNIT
```

```
[Service]
Environment="http_proxy=http://<kraken-host>:<port>"
Environment="https_proxy=http://<kraken-host>:<port>"
Environment="no_proxy=localhost,127.0.0.1,::1"
```

Затем `sudo systemctl daemon-reload` и перезапустите сервис. Если сервис
запускается под отдельным пользователем, убедитесь, что сеть до прокси
доступна из его окружения.

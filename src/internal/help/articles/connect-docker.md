---
slug: connect-docker
title: Docker
section: Подключение машин
order: 30
screen: clients
---

Скачивание образов (`docker pull`) и прокси внутри контейнеров
настраиваются отдельно. Адрес и порт прокси показаны в шапке панели
состояния.

## Прокси для docker pull (systemd drop-in)

Docker daemon читает прокси из своего окружения. Если демон запущен через
systemd, создайте drop-in:

```
sudo systemctl edit docker
```

```
[Service]
Environment="HTTP_PROXY=http://<kraken-host>:<port>"
Environment="HTTPS_PROXY=http://<kraken-host>:<port>"
Environment="NO_PROXY=localhost,127.0.0.1,::1"
```

Затем `sudo systemctl daemon-reload` и `sudo systemctl restart docker`.
Проверьте: `docker info` покажет настроенные прокси. Хост демона должен
быть описан как клиент в Kraken.

## Прокси внутри контейнеров

Контейнерное приложение читает `~/.docker/config.json` и подставляет прокси
в переменные окружения при `docker run`:

```json
{
  "proxies": {
    "default": {
      "httpProxy": "http://<kraken-host>:<port>",
      "httpsProxy": "http://<kraken-host>:<port>",
      "noProxy": "localhost,127.0.0.1,::1"
    }
  }
}
```

Source-адрес контейнера должен попадать в клиент Kraken; NAT, скрывающий
все контейнеры за одним IP, делает клиентов неразличимыми.

## Сборка образов

Передайте прокси как build args, чтобы шаги `RUN apt-get` и `RUN pip` в
Dockerfile шли через прокси:

```
docker build \
  --build-arg HTTP_PROXY=http://<kraken-host>:<port> \
  --build-arg HTTPS_PROXY=http://<kraken-host>:<port> \
  --build-arg NO_PROXY=localhost,127.0.0.1,::1 \
  -t myimage .
```

Не задавайте прокси паролем в build args: образ и история слоёв
сохраняют аргументы. Для логина используйте TLS-прокси по доверению
сертификату, см. [TLS-прокси с логином](help:connect-tls).

## Какие домены разрешить

В политике клиента хоста-сборщика добавьте реестры образов:
`registry-1.docker.io`, `auth.docker.io`, `*.docker.io` для Docker Hub,
и адреса приватных реестров. См. [Разрешить репозиторий пакетов](help:scenario-allow-repo).

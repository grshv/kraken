---
slug: connect-k8s
title: Kubernetes
section: Подключение машин
order: 70
---

Узлы Kubernetes и поды настраиваются отдельно: containerd и kubelet на
узле читают своё окружение, а поды получают прокси через переменные.
Адрес и порт прокси показаны в шапке панели состояния.

## Переменные в pod

Прокси для приложения в поде задаётся через `env` в манифесте или через
Downward API из ConfigMap:

```yaml
env:
  - name: HTTP_PROXY
    value: "http://<kraken-host>:<port>"
  - name: HTTPS_PROXY
    value: "http://<kraken-host>:<port>"
  - name: NO_PROXY
    value: "localhost,127.0.0.1,10.0.0.0/8"
```

`NO_PROXY` должен включать диапазоны кластера (Service CIDR, Pod CIDR) и
`kubernetes.default.svc`, иначе внутренний API-трафик пойдёт через прокси.
Source-адрес пода должен попадать в клиент Kraken.

## containerd

`containerd` тянет образы на узле и читает прокси из своего окружения.
При запуске через systemd создайте drop-in:

```
sudo systemctl edit containerd
```

```
[Service]
Environment="HTTP_PROXY=http://<kraken-host>:<port>"
Environment="HTTPS_PROXY=http://<kraken-host>:<port>"
Environment="NO_PROXY=localhost,127.0.0.1,10.0.0.0/8"
```

Затем `sudo systemctl daemon-reload` и `sudo systemctl restart containerd`.
Узел должен быть описан как клиент в Kraken.

## Исключения для сети кластера

В политику исключений и `NO_PROXY` вносите:

- `kubernetes.default.svc`, `<service-domain>` кластера;
- Service CIDR и Pod CIDR;
- DNS-имя реестра кластера и его IP, если они локальные.

Не вносите внешние домены: обход внешнего egress должен контролироваться
сетевым firewall, а не настройкой прокси.

## Какие реестры разрешить

В политике клиента узла добавьте реестры образов: `registry.k8s.io`,
`*.docker.io`, `ghcr.io` и адреса приватных реестров. См.
[Разрешить репозиторий пакетов](help:scenario-allow-repo).

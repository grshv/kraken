---
slug: connect-windows
title: Windows
section: Подключение машин
order: 50
screen: clients
---

На Windows системный прокси, WinHTTP и PowerShell настраиваются отдельно.
Адрес и порт прокси показаны в шапке панели состояния.

## Системный прокси

«Параметры» → «Сеть и интернет» → «Прокси-сервер». Включите ручную настройку
и укажите адрес `http://<kraken-host>:<port>`. Этот прокси используют
браузер и приложения, читающие системные настройки. Исключения задаются в
том же окне; не вносите туда внешние домены.

## netsh winhttp

Команды `curl`, `winget` и некоторые системные компоненты читают WinHTTP,
а не системный прокси. В elevated PowerShell:

```
netsh winhttp set proxy proxy-server="<kraken-host>:<port>" bypass-list="localhost;127.0.0.1"
```

Проверьте:

```
netsh winhttp show proxy
```

WinHTTP хранит настройку вне контекста пользователя; перезагрузка не
требуется.

## PowerShell и winget

PowerShell `Invoke-WebRequest` по умолчанию следует системному прокси
из `netsh winhttp` и переменным окружения сессии. Для явного задания в
сессии:

```powershell
$env:http_proxy = "http://<kraken-host>:<port>"
$env:https_proxy = "http://<kraken-host>:<port>"
```

`winget` использует WinHTTP. Хост Windows должен быть описан как клиент в
Kraken; NAT, скрывающий несколько машин за одним IP, делает их
неразличимыми. Для входа по логину используйте TLS-прокси, см.
[TLS-прокси с логином](help:connect-tls).

## Доверие сертификату

Если клиент подключается к прокси по TLS, корневой CA, выпустивший
сертификат прокси, должен быть доверенным на машине Windows — в
«certmgr» → «Trusted Root Certification Authorities». Раздавать
приватный ключ нельзя.

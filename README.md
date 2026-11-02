<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/images/kraken-mark-dark.svg">
    <img src="docs/images/kraken-mark-light.svg" alt="" width="88" height="88">
  </picture>
</p>

<h1 align="center">Kraken</h1>

<p align="center">
  Веб-панель для прокси Squid: кому какие сайты открыты<br>
  и почему запрос заблокирован.
</p>

<p align="center">
  <a href="https://github.com/grshv/kraken/actions/workflows/image.yml"><img src="https://github.com/grshv/kraken/actions/workflows/image.yml/badge.svg" alt="Image"></a>
  <a href="https://github.com/grshv/kraken/actions/workflows/security.yml"><img src="https://github.com/grshv/kraken/actions/workflows/security.yml/badge.svg" alt="Security"></a>
  <a href="https://github.com/grshv/kraken/pkgs/container/kraken"><img src="https://img.shields.io/badge/ghcr.io-grshv%2Fkraken-2b3137?logo=docker&logoColor=white" alt="ghcr.io/grshv/kraken"></a>
</p>

<p align="center">
  <a href="#быстрый-старт">Быстрый старт</a> ·
  <a href="#настройка">Настройка</a> ·
  <a href="#обслуживание">Обслуживание</a> ·
  <a href="docs/README.md">Документация</a>
</p>

<p align="center">
  <img src="docs/images/clients.png" alt="Обзор клиента: доступ, политики и журнал запросов" width="900">
</p>

Клиент — устройство, адрес, сеть или группа. Каждому открыт весь веб, только
разрешённые домены или ничего, а журнал показывает, кто куда ходил и почему получил
отказ.

## Возможности

- 🔒 **Закрыто по умолчанию.** Внутренние адреса и опасные порты запрещены всегда,
  даже в режиме «весь веб».
- 📝 **Черновик отдельно от трафика.** Правки копятся и применяются одной операцией
  с проверкой и автоматическим откатом.
- 🔎 **Понятные отказы.** Журнал за 30 дней, причина каждого отказа и кнопка
  «разрешить» прямо из записи.
- 👥 **Роли.** Администратор и менеджер, которому видны только его клиенты.
- 🔑 **Логины прокси.** Пароль передаётся только по TLS.
- 🌐 **HTTPS без расшифровки.** Сайты открываются со своими сертификатами.

## Быстрый старт

Нужен Docker с Compose.

```sh
curl -fsSLO https://raw.githubusercontent.com/grshv/kraken/main/deploy/compose.yaml
docker compose up -d
docker exec -it kraken kraken bootstrap --username admin
```

Последняя команда создаёт администратора и спрашивает пароль. Готово:

| | Адрес |
| --- | --- |
| Панель | <https://localhost:8443> |
| Прокси | `localhost:3128`, с логином — `localhost:3129` (TLS) |

Kraken сам выпускает сертификат для панели и прокси. Чтобы браузер ему доверял,
импортируйте сертификат центра (CA) установки:

```sh
docker exec kraken kraken cert ca > kraken-ca.crt
```

<details>
<summary>Как импортировать на macOS, Linux и Windows</summary>

- **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain kraken-ca.crt`
- **Debian/Ubuntu:** `sudo cp kraken-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates`
- **Windows:** `certutil -addstore -f Root kraken-ca.crt` (от администратора)

Firefox использует своё хранилище: «Настройки → Приватность → Сертификаты → Импорт».
CA подписывает только имена этой установки и не может выдать сертификат чужому сайту.

</details>

## Настройка

По умолчанию всё доступно только с этой машины. Чтобы открыть панель и прокси для
сети, положите рядом с `compose.yaml` файл `.env` ([пример](.env.example)):

```ini
KRAKEN_PUBLIC_ORIGIN=https://10.0.0.5:8443
KRAKEN_MANAGEMENT_BIND=10.0.0.5
KRAKEN_PROXY_BIND=10.0.0.5
```

и выполните `docker compose up -d`. Лишние порты закройте firewall.

| Переменная | Что задаёт |
| --- | --- |
| `KRAKEN_PUBLIC_ORIGIN` | Адрес, по которому открывают панель |
| `KRAKEN_MANAGEMENT_BIND` | IP хоста для панели |
| `KRAKEN_PROXY_BIND` | IP хоста для прокси |
| `KRAKEN_CERT_NAMES` | Дополнительные имена в сертификате, например DNS-имя прокси |
| `KRAKEN_DATA` | Каталог для данных вместо тома Docker, например `./data` |

<details>
<summary>Несколько адресов в разных сетях</summary>

Добавьте `compose.override.yaml` рядом с `compose.yaml`. Compose подхватит его сам:

```yaml
services:
  kraken:
    ports:
      - "192.168.10.5:8443:8443"   # панель во второй сети
      - "10.30.0.5:3128:3128"      # прокси во второй сети
      - "10.30.0.5:3129:3129"
    environment:
      KRAKEN_CERT_NAMES: 192.168.10.5,10.30.0.5
```

Затем перевыпустите сертификат под новые адреса:

```sh
docker exec kraken kraken cert renew && docker compose restart kraken
```

</details>

<details>
<summary>Свой сертификат вместо выпущенного</summary>

```sh
cat panel.crt panel.key | docker exec -i kraken kraken cert install panel
cat proxy.crt proxy.key | docker exec -i kraken kraken cert install proxy
docker compose restart kraken
```

Команда проверяет, что ключ подходит к сертификату и срок действия не истёк.

</details>

## Обслуживание

```sh
docker compose pull && docker compose up -d   # обновление
docker compose logs -f                        # логи
```

Остальное — командой `docker exec -it kraken kraken <команда>`:

| Команда | Что делает |
| --- | --- |
| `bootstrap`, `password-reset` | Первый администратор, смена пароля |
| `user`, `sessions` | Пользователи панели и их сессии |
| `status`, `check` | Состояние и проверка Squid |
| `backup` | Копия данных и сертификатов |
| `cert ca`, `cert renew`, `cert install` | Сертификаты |

Обновление на время разрывает открытые соединения прокси. Восстановление из копии и
аварии описаны в [runbooks](docs/operations/runbooks.md).

## Документация

- [Установка и сеть](docs/operations/deployment.md)
- [Сертификаты и пароли](docs/security/authentication-and-certificates.md)
- [Модель угроз](docs/security/threat-model.md)
- [Что сделано и что осталось](docs/status.md)
- [Разработка](CONTRIBUTING.md)

Личный проект, лицензия не выбрана.

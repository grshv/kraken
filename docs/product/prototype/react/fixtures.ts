// Synthetic fixtures; no API, credentials or real telemetry.
export const fixtures = {
  "data": {
    "policies": [
      {
        "id": "build",
        "name": "Сборка и CI",
        "mode": "list",
        "dom": [
          {
            "d": "archive.ubuntu.com",
            "s": 1,
            "h": 9120
          },
          {
            "d": "ports.ubuntu.com",
            "s": 1,
            "h": 2310
          },
          {
            "d": "docker.io",
            "s": 1,
            "h": 14210
          },
          {
            "d": "ghcr.io",
            "s": 0,
            "h": 3120
          },
          {
            "d": "github.com",
            "s": 1,
            "h": 8800
          },
          {
            "d": "pypi.org",
            "s": 0,
            "h": 6200
          },
          {
            "d": "files.pythonhosted.org",
            "s": 0,
            "h": 3920
          },
          {
            "d": "bitbucket.org",
            "s": 0,
            "h": 0
          }
        ]
      },
      {
        "id": "ci01",
        "name": "ci-01",
        "own": true,
        "mode": "list",
        "dom": [
          {
            "d": "pypi.org",
            "s": 0,
            "h": 3920
          },
          {
            "d": "files.pythonhosted.org",
            "s": 0,
            "h": 2104
          }
        ]
      },
      {
        "id": "k8s",
        "name": "Кластер prod",
        "mode": "list",
        "dom": [
          {
            "d": "registry.k8s.io",
            "s": 0,
            "h": 7810
          },
          {
            "d": "quay.io",
            "s": 0,
            "h": 4410
          },
          {
            "d": "docker.io",
            "s": 1,
            "h": 6022
          },
          {
            "d": "gcr.io",
            "s": 1,
            "h": 2130
          },
          {
            "d": "github.com",
            "s": 1,
            "h": 1201
          }
        ]
      },
      {
        "id": "web",
        "name": "Весь веб",
        "mode": "all",
        "dom": []
      },
      {
        "id": "s3",
        "name": "Бэкапы S3",
        "mode": "list",
        "dom": [
          {
            "d": "s3.eu-central-1.amazonaws.com",
            "s": 0,
            "h": 412
          }
        ]
      },
      {
        "id": "mon",
        "name": "Мониторинг",
        "mode": "list",
        "dom": [
          {
            "d": "api.telegram.org",
            "s": 0,
            "h": 288
          }
        ]
      }
    ],
    "clients": [
      {
        "id": "build",
        "name": "build-runners",
        "desc": "CI-раннеры GitLab",
        "kind": "Сеть",
        "nets": [
          "10.20.0.0/24"
        ],
        "pol": "build",
        "tr": "HTTP",
        "login": "",
        "req": 48210,
        "den": 212,
        "bytes": "38,4 ГБ",
        "last": "сейчас",
        "sp": [
          42,
          76,
          78,
          95,
          122,
          133,
          86,
          75,
          72,
          133,
          70,
          89,
          71,
          39,
          35,
          51,
          29,
          30,
          35,
          37,
          38,
          31,
          58,
          59
        ],
        "on": true,
        "tun": 5
      },
      {
        "id": "ci01",
        "name": "ci-01",
        "desc": "Сборочный сервер фронтенда",
        "kind": "Устройство",
        "nets": [
          "10.20.1.15"
        ],
        "pol": "ci01",
        "tr": "HTTP",
        "login": "",
        "req": 6120,
        "den": 37,
        "bytes": "2,3 ГБ",
        "last": "2 мин",
        "sp": [
          14,
          27,
          27,
          22,
          49,
          37,
          41,
          39,
          37,
          29,
          25,
          33,
          17,
          17,
          17,
          20,
          14,
          11,
          12,
          19,
          17,
          17,
          16,
          13
        ],
        "on": true,
        "tun": 1,
        "note": "Отдельный адрес: не пересекается с сетью build-runners"
      },
      {
        "id": "k8s",
        "name": "k8s-nodes",
        "desc": "Узлы кластера prod",
        "kind": "Группа",
        "nets": [
          "10.50.1.0/24",
          "10.50.2.0/24",
          "10.50.3.0/24"
        ],
        "pol": "k8s",
        "tr": "HTTP",
        "login": "",
        "req": 22904,
        "den": 0,
        "bytes": "21,7 ГБ",
        "last": "1 мин",
        "sp": [
          31,
          47,
          89,
          56,
          105,
          122,
          84,
          61,
          59,
          75,
          70,
          73,
          53,
          36,
          35,
          44,
          23,
          24,
          35,
          39,
          43,
          27,
          30,
          32
        ],
        "on": true,
        "tun": 3
      },
      {
        "id": "vpn",
        "name": "vpn-office",
        "desc": "Офис через VPN",
        "kind": "Сеть",
        "nets": [
          "10.8.0.0/16"
        ],
        "pol": "web",
        "tr": "HTTP",
        "login": "",
        "req": 31877,
        "den": 9,
        "bytes": "12,1 ГБ",
        "last": "сейчас",
        "sp": [
          35,
          78,
          97,
          73,
          107,
          67,
          79,
          91,
          87,
          101,
          108,
          76,
          43,
          35,
          33,
          45,
          35,
          25,
          53,
          35,
          27,
          38,
          54,
          45
        ],
        "on": true,
        "tun": 2
      },
      {
        "id": "dev",
        "name": "dev-laptops",
        "desc": "Ноутбуки разработчиков",
        "kind": "Сеть",
        "nets": [
          "10.40.0.0/22"
        ],
        "pol": "web",
        "tr": "TLS",
        "login": "dev-*",
        "req": 9310,
        "den": 4,
        "bytes": "6,0 ГБ",
        "last": "сейчас",
        "sp": [
          21,
          49,
          60,
          72,
          76,
          75,
          65,
          75,
          72,
          39,
          67,
          51,
          44,
          27,
          28,
          31,
          28,
          16,
          33,
          31,
          23,
          32,
          27,
          16
        ],
        "on": true,
        "tun": 1,
        "changed": "Было: HTTP без логина"
      },
      {
        "id": "bkp",
        "name": "backup-01",
        "desc": "Выгрузка бэкапов в S3",
        "kind": "Устройство",
        "nets": [
          "10.30.1.7"
        ],
        "pol": "s3",
        "tr": "TLS",
        "login": "backup",
        "req": 412,
        "den": 0,
        "bytes": "94,2 ГБ",
        "last": "38 мин",
        "sp": [
          5,
          8,
          15,
          16,
          9,
          17,
          19,
          15,
          15,
          18,
          12,
          13,
          6,
          8,
          8,
          4,
          6,
          4,
          6,
          4,
          4,
          7,
          4,
          8
        ],
        "on": true,
        "tun": 0
      },
      {
        "id": "mon",
        "name": "monitoring",
        "desc": "Алерты в мессенджер",
        "kind": "Устройство",
        "nets": [
          "10.10.0.5"
        ],
        "pol": "mon",
        "tr": "HTTP",
        "login": "",
        "req": 288,
        "den": 0,
        "bytes": "3 МБ",
        "last": "4 мин",
        "sp": [
          3,
          7,
          7,
          10,
          7,
          7,
          13,
          13,
          13,
          11,
          7,
          9,
          7,
          5,
          2,
          3,
          5,
          3,
          3,
          3,
          5,
          3,
          3,
          5
        ],
        "on": true,
        "tun": 0
      },
      {
        "id": "prn",
        "name": "lab-printers",
        "desc": "Принтеры лаборатории",
        "kind": "Группа",
        "nets": [
          "10.60.0.0/24",
          "10.61.0.0/24"
        ],
        "pol": "",
        "tr": "HTTP",
        "login": "",
        "req": 0,
        "den": 0,
        "bytes": "—",
        "last": "—",
        "sp": [
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        "on": false,
        "tun": 0
      },
      {
        "id": "arm",
        "name": "build-arm",
        "desc": "ARM-раннеры, команда платформы",
        "kind": "Сеть",
        "nets": [
          "10.21.0.0/24"
        ],
        "pol": "build",
        "tr": "HTTP",
        "login": "",
        "req": 7310,
        "den": 0,
        "bytes": "5,2 ГБ",
        "last": "сейчас",
        "sp": [
          17,
          21,
          36,
          46,
          38,
          31,
          33,
          54,
          52,
          44,
          51,
          48,
          20,
          29,
          16,
          15,
          19,
          15,
          25,
          21,
          22,
          25,
          21,
          18
        ],
        "on": true,
        "tun": 1
      }
    ],
    "unknown": [
      {
        "ip": "10.99.1.4",
        "den": 5,
        "dst": [
          "deb.debian.org",
          "security.debian.org"
        ],
        "last": "21 мин"
      }
    ],
    "denied": {
      "ci01": [
        {
          "d": "registry.npmjs.org",
          "n": 37,
          "why": "list"
        }
      ],
      "build": [
        {
          "d": "objects.githubusercontent.com",
          "n": 164,
          "why": "list"
        },
        {
          "d": "smtp.gmail.com:587",
          "n": 48,
          "why": "port"
        }
      ],
      "vpn": [
        {
          "d": "169.254.169.254",
          "n": 9,
          "why": "internal"
        }
      ]
    },
    "blocked": [
      {
        "d": "doubleclick.net",
        "s": 1,
        "h": 212
      },
      {
        "d": "ngrok.io",
        "s": 1,
        "h": 3
      },
      {
        "d": "pastebin.com",
        "s": 0,
        "h": 0
      },
      {
        "d": "tracker.example",
        "s": 1,
        "h": 41,
        "isnew": 1
      }
    ],
    "accounts": {
      "panel": [
        {
          "n": "admin",
          "role": "Администратор",
          "on": true,
          "last": "сейчас"
        },
        {
          "n": "oncall",
          "role": "Менеджер",
          "on": true,
          "last": "вчера"
        },
        {
          "n": "contractor",
          "role": "Менеджер",
          "on": false,
          "last": "12 д"
        }
      ],
      "proxy": [
        {
          "n": "backup",
          "on": true,
          "used": "backup-01"
        },
        {
          "n": "dev-anna",
          "on": true,
          "used": "dev-laptops"
        },
        {
          "n": "dev-oleg",
          "on": true,
          "used": "dev-laptops"
        }
      ]
    }
  },
  "events": [
    [
      "14:02:11",
      "ci01",
      "10.20.1.15",
      "CONNECT",
      "registry.npmjs.org:443",
      "403",
      "d"
    ],
    [
      "14:02:09",
      "build",
      "10.20.0.31",
      "CONNECT",
      "github.com:443",
      "200",
      "a"
    ],
    [
      "14:02:08",
      "build",
      "10.20.0.31",
      "CONNECT",
      "objects.githubusercontent.com:443",
      "403",
      "d"
    ],
    [
      "14:01:58",
      "vpn",
      "10.8.3.17",
      "GET",
      "archive.ubuntu.com:80",
      "200",
      "a"
    ],
    [
      "14:01:51",
      "k8s",
      "10.50.2.11",
      "CONNECT",
      "registry.k8s.io:443",
      "200",
      "a"
    ],
    [
      "14:01:40",
      "ci01",
      "10.20.1.15",
      "CONNECT",
      "pypi.org:443",
      "200",
      "a"
    ],
    [
      "14:01:12",
      "?",
      "10.99.1.4",
      "GET",
      "deb.debian.org:80",
      "403",
      "d"
    ],
    [
      "14:00:58",
      "mon",
      "10.10.0.5",
      "CONNECT",
      "api.telegram.org:443",
      "200",
      "a"
    ],
    [
      "14:00:44",
      "build",
      "10.20.0.44",
      "CONNECT",
      "smtp.gmail.com:587",
      "403",
      "d"
    ],
    [
      "14:00:31",
      "bkp",
      "10.30.1.7",
      "CONNECT",
      "s3.eu-central-1.amazonaws.com:443",
      "200",
      "a"
    ],
    [
      "13:59:41",
      "?",
      "10.20.1.9",
      "CONNECT",
      "registry.npmjs.org:443",
      "403",
      "d"
    ],
    [
      "14:00:20",
      "ci01",
      "10.20.1.15",
      "GET",
      "registry.npmjs.org:80",
      "403",
      "d"
    ],
    [
      "13:59:50",
      "ci01",
      "10.20.1.15",
      "CONNECT",
      "pypi.org:443",
      "200",
      "a"
    ]
  ],
  "audit": [
    {
      "day": "Сегодня",
      "t": "13:58",
      "who": "admin",
      "type": "draft",
      "ic": "diff",
      "title": "dev-laptops: HTTP → TLS с логином",
      "sum": "в черновике",
      "ch": [
        [
          "m",
          "dev-laptops: HTTP → TLS с логином"
        ],
        [
          "a",
          "Запрещено для всех: *.tracker.example"
        ]
      ]
    },
    {
      "day": "Сегодня",
      "t": "13:40",
      "who": "admin",
      "type": "apply",
      "ic": "play",
      "title": "Применена ревизия r-41",
      "sum": "ok",
      "rev": "r-41",
      "ch": [
        [
          "a",
          "Запрещено для всех: *.doubleclick.net"
        ],
        [
          "m",
          "DNS: кеш ответов 6 ч → 1 ч"
        ]
      ],
      "steps": [
        [
          "Проверка правил",
          "0,1 с",
          1
        ],
        [
          "Проверка конфигурации в Squid",
          "0,4 с",
          1
        ],
        [
          "Запись журнала применения",
          "0,0 с",
          1
        ],
        [
          "Squid перечитывает конфигурацию",
          "1,2 с",
          1
        ],
        [
          "Подтверждение r-41",
          "0,3 с",
          1
        ]
      ]
    },
    {
      "day": "Сегодня",
      "t": "12:02",
      "who": "admin",
      "type": "apply",
      "ic": "play",
      "title": "Применение r-40 откатилось",
      "sum": "bad",
      "rev": "r-40",
      "ch": [
        [
          "m",
          "squid.conf вручную: 3 строки"
        ]
      ],
      "steps": [
        [
          "Проверка правил",
          "0,1 с",
          1
        ],
        [
          "Проверка конфигурации в Squid",
          "0,3 с",
          0,
          "Squid отклонил строку 74 squid.conf вручную; текст ошибки не показывается"
        ]
      ],
      "note": "Трафик не прерывался: работала r-39"
    },
    {
      "day": "Сегодня",
      "t": "09:12",
      "who": "admin",
      "type": "login",
      "ic": "user",
      "title": "Вход в панель",
      "sum": "",
      "kv": [
        [
          "IP",
          "10.8.3.17"
        ],
        [
          "Браузер",
          "Firefox 131, macOS"
        ]
      ]
    },
    {
      "day": "Вчера",
      "t": "18:15",
      "who": "oncall",
      "type": "login",
      "ic": "user",
      "title": "Вход в панель",
      "sum": "",
      "kv": [
        [
          "IP",
          "10.8.9.40"
        ],
        [
          "Браузер",
          "Chrome 130, Windows"
        ]
      ]
    },
    {
      "day": "Вчера",
      "t": "17:50",
      "who": "oncall",
      "type": "login",
      "ic": "warn",
      "title": "Неудачный вход: неверный пароль",
      "sum": "warn",
      "kv": [
        [
          "IP",
          "10.8.9.40"
        ],
        [
          "Попыток",
          "2 подряд, затем успешный вход"
        ]
      ]
    },
    {
      "day": "Вчера",
      "t": "09:30",
      "who": "admin",
      "type": "acc",
      "ic": "key",
      "title": "Сброшен пароль прокси dev-oleg",
      "sum": "",
      "kv": [
        [
          "Последствия",
          "Старый пароль перестал работать сразу; активные туннели не разорваны"
        ]
      ]
    },
    {
      "day": "27 окт",
      "t": "16:02",
      "who": "admin",
      "type": "acc",
      "ic": "key",
      "title": "Заблокирован пользователь contractor",
      "sum": "",
      "kv": [
        [
          "Сессии",
          "2 завершены"
        ]
      ]
    },
    {
      "day": "27 окт",
      "t": "11:02",
      "who": "admin",
      "type": "apply",
      "ic": "play",
      "title": "Применена ревизия r-38",
      "sum": "ok",
      "rev": "r-38",
      "ch": [
        [
          "m",
          "ci-01: источник 10.20.0.14 → 10.20.1.15"
        ]
      ],
      "steps": [
        [
          "Проверка правил",
          "0,1 с",
          1
        ],
        [
          "Проверка конфигурации в Squid",
          "0,4 с",
          1
        ],
        [
          "Запись журнала применения",
          "0,0 с",
          1
        ],
        [
          "Squid перечитывает конфигурацию",
          "1,1 с",
          1
        ],
        [
          "Подтверждение r-38",
          "0,2 с",
          1
        ]
      ]
    }
  ],
  "accounts": {
    "admin": {
      "t": "panel",
      "role": "admin",
      "on": true,
      "last": "сегодня 09:12, 10.8.3.17",
      "pwd": "12 дней назад",
      "fail": 0,
      "sess": [
        [
          "Firefox 131 · macOS",
          "10.8.3.17",
          "сегодня 09:12",
          "сейчас",
          1
        ],
        [
          "Safari · iPadOS",
          "10.8.4.2",
          "вчера 21:40",
          "вчера 22:05",
          0
        ]
      ]
    },
    "anna": {
      "t": "panel",
      "role": "manager",
      "on": true,
      "clients": [
        "build",
        "ci01"
      ],
      "nets": [
        "10.20.0.0/22"
      ],
      "last": "сегодня 11:02, 10.20.0.200",
      "pwd": "неделю назад",
      "fail": 0,
      "sess": [
        [
          "Chrome 130 · Linux",
          "10.20.0.200",
          "сегодня 11:02",
          "14 мин назад",
          0
        ]
      ]
    },
    "oleg": {
      "t": "panel",
      "role": "manager",
      "on": true,
      "clients": [
        "vpn",
        "dev"
      ],
      "nets": [
        "10.8.0.0/16",
        "10.40.0.0/22"
      ],
      "last": "вчера 18:15, 10.8.9.40",
      "pwd": "40 дней назад",
      "fail": 2,
      "sess": []
    },
    "contractor": {
      "t": "panel",
      "role": "manager",
      "on": false,
      "clients": [
        "arm"
      ],
      "nets": [],
      "last": "12 дней назад",
      "pwd": "—",
      "fail": 0,
      "sess": []
    },
    "backup": {
      "t": "proxy",
      "on": true,
      "clients": [
        "backup-01"
      ],
      "ok": "412",
      "bad": "0",
      "req": "412",
      "ips": [
        [
          "10.30.1.7",
          412
        ]
      ],
      "pwd": "3 месяца назад"
    },
    "dev-anna": {
      "t": "proxy",
      "on": true,
      "clients": [
        "dev-laptops"
      ],
      "ok": "5 102",
      "bad": "0",
      "req": "5 410",
      "ips": [
        [
          "10.40.1.88",
          5102
        ]
      ],
      "pwd": "2 недели назад"
    },
    "dev-oleg": {
      "t": "proxy",
      "on": true,
      "clients": [
        "dev-laptops"
      ],
      "ok": "3 870",
      "bad": "3",
      "req": "3 900",
      "ips": [
        [
          "10.40.2.14",
          3861
        ],
        [
          "10.40.1.90",
          9
        ]
      ],
      "pwd": "вчера (сброс)",
      "badLast": "13:12 с 10.40.1.90"
    },
    "ci-deploy": {
      "t": "proxy",
      "on": true,
      "owner": "anna",
      "clients": [],
      "ok": "0",
      "bad": "0",
      "req": "0",
      "ips": [],
      "pwd": "сегодня"
    }
  }
};

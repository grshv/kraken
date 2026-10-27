# Исходники приложения

Go module, backend, migrations и web находятся здесь по просьбе владельца.
Module path `squid-control.local/squid` локальный и не предполагает внешний remote.
Frontend build output в `web/dist` не редактировать; он встраивается Go embed.
Runtime не вызывает Docker/shell. Squid управляется локальным fixed-argv adapter.
Команды из корня: `make check`, `make image`, `make integration`.
Из src/: `go test -race ./...`; предварительно нужен frontend dist из make web-build.

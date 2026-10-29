# syntax=docker/dockerfile:1
FROM node:24-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS web
WORKDIR /build/web
COPY src/web/package*.json ./
RUN npm ci
COPY src/web/ ./
RUN npm run build

FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
WORKDIR /build
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
COPY --from=web /build/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /out/squid-control ./cmd/control
RUN GOBIN=/out CGO_ENABLED=0 go install github.com/canonical/pebble/cmd/pebble@v1.33.2

FROM build AS verify
RUN go test -race ./... && go vet ./...

FROM ubuntu/squid:6.6-24.04_beta@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029
COPY --from=build /out/squid-control /usr/local/bin/squid-control
COPY --from=build /out/pebble /usr/local/bin/pebble
COPY deploy/pebble/001-services.yaml /var/lib/pebble/default/layers/001-services.yaml
# Squid (proxy) and the panel (sccontrol, extra group proxy) use distinct UIDs
# (ADR 0015): Squid reads runtime files and the proxy cert through the group but
# not the panel database, panel key or manager token. Setgid directories give
# new files group proxy without CAP_CHOWN.
RUN (getent group proxy >/dev/null || groupadd -g 13 proxy) \
    && groupadd -g 10013 sccontrol \
    && useradd -u 10013 -g sccontrol -G proxy -r -s /usr/sbin/nologin sccontrol \
    && install -d -o sccontrol -g proxy -m 0750 /data \
    && install -d -o sccontrol -g sccontrol -m 0700 /data/app \
    && install -d -o sccontrol -g proxy -m 2750 /data/certs \
    && install -d -o sccontrol -g proxy -m 2750 /data/runtime \
    && install -d -o proxy -g proxy -m 2750 /data/squid \
    && install -d -o proxy -g proxy -m 2750 /data/logs
ENV PEBBLE=/var/lib/pebble/default
EXPOSE 8443 3128 3129
VOLUME /data
ENTRYPOINT ["/usr/local/bin/pebble"]
CMD ["run", "--verbose"]

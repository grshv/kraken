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
RUN CGO_ENABLED=0 go build -trimpath -o /out/kraken ./cmd/control
RUN GOBIN=/out CGO_ENABLED=0 go install github.com/canonical/pebble/cmd/pebble@v1.33.2

FROM build AS verify
RUN go test -race ./... && go vet ./...

FROM ubuntu/squid:6.6-24.04_beta@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029
COPY --from=build /out/kraken /usr/local/bin/kraken
COPY --from=build /out/pebble /usr/local/bin/pebble
COPY deploy/pebble/001-services.yaml /var/lib/pebble/default/layers/001-services.yaml
# Squid (proxy) and the panel (kraken, extra group proxy) use distinct UIDs
# (ADR 0015): Squid reads derivative files in the tmpfs run directory and the
# proxy cert through the group, but never the panel database, panel key or the
# manager auth secret. The volume holds only state: the control database under
# data/ (kraken-only), certificates under certs/ (setgid proxy), and Squid logs
# under log/ (proxy). Setgid directories give new files group proxy without
# CAP_CHOWN. Derivative runtime files (squid.conf, manager-auth.conf,
# proxy-accounts export, apply.lock) live only in the tmpfs run directory and
# are never persisted.
RUN (getent group proxy >/dev/null || groupadd -g 13 proxy) \
    && groupadd -g 10013 kraken \
    && useradd -u 10013 -g kraken -G proxy -r -s /usr/sbin/nologin kraken \
    && install -d -o kraken -g proxy -m 0750 /var/lib/kraken \
    && install -d -o kraken -g kraken -m 0700 /var/lib/kraken/data \
    && install -d -o kraken -g kraken -m 0700 /var/lib/kraken/data/telemetry \
    && install -d -o kraken -g proxy -m 2750 /var/lib/kraken/certs \
    && install -d -o proxy -g proxy -m 2750 /var/lib/kraken/log
ENV PEBBLE=/var/lib/pebble/default
EXPOSE 8443 3128 3129
VOLUME /var/lib/kraken
ENTRYPOINT ["/usr/local/bin/pebble"]
CMD ["run", "--verbose"]

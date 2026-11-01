# syntax=docker/dockerfile:1
FROM node:26-alpine@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94 AS web
WORKDIR /build/web
COPY src/web/package*.json ./
RUN npm ci
COPY src/web/ ./
RUN npm run build

FROM golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385 AS build
WORKDIR /build
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
COPY --from=web /build/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /out/kraken ./cmd/control
RUN GOBIN=/out CGO_ENABLED=0 go install github.com/canonical/pebble/cmd/pebble@v1.33.2

# Tests run unprivileged like the panel (UID 10013): root would ignore the
# read-only directories that failure tests rely on. Run-directory tests take
# their tmpfs from /dev/shm themselves.
FROM build AS verify
RUN useradd -m -u 10013 tester && chown -R tester /build
USER tester
ENV GOCACHE=/home/tester/.cache/go-build
RUN go test -race ./... && go vet ./...

# Squid is built from the upstream release tarball (ADR 0019): unpatched, with
# only installation paths, --with-openssl, the default user and
# --disable-strict-error-checking. The compiler and headers stay out of runtime.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS squid
ARG SQUID_VERSION=7.7
ARG SQUID_SHA256=1a748b91722259e986f17d85e0da9d9403054a50bc20f78394ff90e46ef55601
RUN apk add --no-cache build-base perl openssl-dev libcap-dev libtool linux-headers curl
RUN tag="SQUID_$(echo "$SQUID_VERSION" | tr '.' '_')" \
    && curl -fsSL -o /squid.tar.bz2 "https://github.com/squid-cache/squid/releases/download/${tag}/squid-${SQUID_VERSION}.tar.bz2" \
    && echo "${SQUID_SHA256}  /squid.tar.bz2" | sha256sum -c - \
    && mkdir /src && tar -xjf /squid.tar.bz2 -C /src \
    && cd /src/squid-${SQUID_VERSION} \
    && ./configure --prefix=/usr --sysconfdir=/etc/squid --localstatedir=/var --libexecdir=/usr/lib/squid --datadir=/usr/share/squid --with-default-user=proxy --with-openssl --disable-strict-error-checking \
    && make -j"$(nproc)" \
    && make install-strip DESTDIR=/out \
    && rm -rf /out/var

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
# The source label links the GHCR package to the repository; CI adds the revision.
LABEL org.opencontainers.image.title="Kraken" \
      org.opencontainers.image.description="Web panel for outbound client access through Squid" \
      org.opencontainers.image.source="https://github.com/grshv/kraken"
COPY --from=squid /out/ /
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
RUN apk add --no-cache libstdc++ libssl3 libcrypto3 libcap libltdl setpriv \
    && (deluser news 2>/dev/null || true) \
    && (delgroup news 2>/dev/null || true) \
    && addgroup -g 13 -S proxy \
    && adduser -S -D -H -u 13 -G proxy -s /sbin/nologin proxy \
    && addgroup -g 10013 -S kraken \
    && adduser -S -D -H -u 10013 -G kraken -s /sbin/nologin kraken \
    && addgroup kraken proxy \
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

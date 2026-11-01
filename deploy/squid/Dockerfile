# syntax=docker/dockerfile:1
# Squid built from the upstream release (ADR 0019, ADR 0020): unpatched, with
# only installation paths, --with-openssl, the default user and
# --disable-strict-error-checking. The release tarball is accepted only with a
# valid signature by the Squid release keys in squid-keys.asc, so a version
# bump needs no checksum edit. The result is a scratch image holding the
# installed tree; the panel image copies it onto the same Alpine release.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS build
# renovate: datasource=github-releases depName=squid-cache/squid versioning=regex:^SQUID_(?<major>\d+)_(?<minor>\d+)$
ARG SQUID_TAG=SQUID_7_8
COPY squid-keys.asc /tmp/squid-keys.asc
RUN apk add --no-cache --virtual .squid-build build-base perl openssl-dev libcap-dev libtool linux-headers curl gnupg xz \
    && version="$(echo "${SQUID_TAG#SQUID_}" | tr '_' '.')" \
    && base="https://github.com/squid-cache/squid/releases/download/${SQUID_TAG}/squid-${version}.tar.xz" \
    && curl -fsSL -o /squid.tar.xz "$base" \
    && curl -fsSL -o /squid.tar.xz.asc "$base.asc" \
    && gpg --dearmor < /tmp/squid-keys.asc > /tmp/squid-keys.gpg \
    && gpgv --keyring /tmp/squid-keys.gpg /squid.tar.xz.asc /squid.tar.xz \
    && mkdir /src && tar -xJf /squid.tar.xz -C /src \
    && cd "/src/squid-${version}" \
    && ./configure --prefix=/usr --sysconfdir=/etc/squid --localstatedir=/var --libexecdir=/usr/lib/squid --datadir=/usr/share/squid --with-default-user=proxy --with-openssl --disable-strict-error-checking \
    && make -j"$(nproc)" \
    && make install-strip DESTDIR=/out \
    && rm -rf /out/var \
    && cd / && rm -rf /src /squid.tar.xz /squid.tar.xz.asc /tmp/squid-keys.* \
    && apk del --no-cache .squid-build

FROM scratch
ARG SQUID_TAG
LABEL org.opencontainers.image.title="Kraken Squid" \
      org.opencontainers.image.description="Upstream Squid installed tree for the Kraken image" \
      org.opencontainers.image.source="https://github.com/grshv/kraken" \
      org.opencontainers.image.version="${SQUID_TAG}"
COPY --from=build /out/ /

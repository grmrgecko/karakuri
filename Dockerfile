# Deployable image, built from a checkout: `make docker`, or `docker build .`
# straight from a clone. docker/Dockerfile.release builds the same image from an
# unpacked release archive instead, compiling nothing, which is what a published
# image is made of.
#
# Build stage: uses the Makefile for all build logic so the Dockerfile does
# not need updating when the build layout changes. The server itself is pure Go
# (the SQLite driver runs as wasm under wazero); the compilers are for the MIDI
# plugin alone, whose rtmidi backend is C++ over ALSA.
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache gcc g++ musl-dev alsa-lib-dev nodejs npm make findutils curl

WORKDIR /build

# Cache Go module downloads (changes infrequently). The automation module is a
# local replace, so its go.mod is needed to resolve the graph.
COPY go.mod go.sum ./
COPY automation/go.mod automation/go.sum automation/
RUN go mod download

# Cache frontend npm packages (changes less often than source).
COPY frontend/package.json frontend/package-lock.json frontend/
RUN cd frontend && npm install

# Copy everything and build.
COPY . .
# Unset, the build stamps whatever the VERSION file says; `make docker` passes
# it explicitly. The arg is in the environment even when empty, and make's
# `VERSION ?=` treats an empty environment value as set, so the fallback to the
# file is done here.
# The build fetches the CPython WebAssembly module so Python scripts work;
# PYTHON_SCRIPTS=0 builds a smaller image without it. build/runtimes is created
# either way because the runtime stage copies it.
ARG VERSION=
ARG PYTHON_SCRIPTS=1
RUN make VERSION="${VERSION:-$(cat VERSION)}" PYTHON_SCRIPTS="${PYTHON_SCRIPTS}" && mkdir -p build/runtimes

# Runtime stage.
FROM alpine:latest

# Dashboard renders drive a headless Chromium; the fonts are what keep a
# rendered dashboard from being a grid of boxes. It is the largest thing in the
# image by far, so a deployment that never renders can leave it out with
# --build-arg DASHBOARD_RENDERS=0.
ARG DASHBOARD_RENDERS=1
RUN apk add --no-cache ca-certificates tzdata alsa-lib && \
    if [ "${DASHBOARD_RENDERS}" = "1" ]; then \
        apk add --no-cache chromium font-noto font-noto-emoji ttf-dejavu; \
    fi

RUN mkdir -p /usr/share/karakuri \
             /etc/karakuri \
             /var/lib/karakuri

COPY --from=builder /build/build/karakuri /usr/local/bin/karakuri
COPY --from=builder /build/build/plugins/ /usr/share/karakuri/plugins/
COPY --from=builder /build/build/themes/ /usr/share/karakuri/themes/
COPY --from=builder /build/build/runtimes/ /usr/share/karakuri/runtimes/
COPY docker/entrypoint.sh /usr/local/bin/karakuri-entrypoint

# The configuration is written on first start rather than baked in, so two
# containers never share a signing key. Both images use the same entrypoint for
# it.
RUN adduser -D -H -s /sbin/nologin karakuri && \
    chown -R karakuri:karakuri /var/lib/karakuri /etc/karakuri

USER karakuri

# A container is replaced by pulling a newer image, never updated in place.
ENV KARAKURI_UPDATE_ENABLED=false

# The healthcheck asks the server on this host for its health route. The long
# start period covers migrations and the database copy a new release takes
# before it serves.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5m --retries=3 \
    CMD ["karakuri", "healthcheck"]

VOLUME ["/etc/karakuri", "/var/lib/karakuri"]
EXPOSE 8080 9443

ENTRYPOINT ["/usr/local/bin/karakuri-entrypoint"]
CMD ["serve"]

# Build stage: produce a static Go binary.
FROM golang:1.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ups-mqtt ./cmd/ups-mqtt

# Runtime stage.
#
# This image contains only the bridge. pwrstatd stays on the host and keeps
# ownership of the UPS, so no /dev/hidraw* access is needed here. The pwrstat
# CLI itself is bind-mounted read-only at run time (see compose.yaml) to avoid
# vendoring proprietary binaries into the image.
#
# BASE_IMAGE determines the glibc the vendor CLI runs against. The host is
# Ubuntu 26.04 (resolute) and the CLI is dynamically linked against its libc, so
# the image must be at least as new — do not downgrade this to an older base or
# to a musl-based image, or the mounted binary will fail to load.
ARG BASE_IMAGE=ubuntu:26.04
FROM ${BASE_IMAGE}

RUN set -eux; \
    if command -v apt-get >/dev/null 2>&1; then \
        apt-get update; \
        apt-get install -y --no-install-recommends ca-certificates; \
        rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null 2>&1; then \
        dnf install -y ca-certificates; \
        dnf clean all; \
    fi

COPY --from=build /out/ups-mqtt /usr/local/bin/ups-mqtt

# /usr/bin/pwrstat is mode 0700 on the host, so the process must run as root.
# Do not drop all capabilities: root needs DAC_OVERRIDE to execute the mounted
# binary when its host owner does not map to uid 0 inside the container.
USER root

ENTRYPOINT ["/usr/local/bin/ups-mqtt"]

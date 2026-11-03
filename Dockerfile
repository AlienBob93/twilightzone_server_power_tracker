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
# The host is Ubuntu 26.04 (resolute) and the pwrstat CLI is dynamically linked
# against its libc, so the base must be at least as new — do not downgrade this
# to an older release or a musl image, or the mounted binary will fail to load.
#
# Pinned literally rather than through an ARG: the deploy pipeline builds this
# Dockerfile without compose build args, and an unpinned BASE_IMAGE resolves to
# empty in FROM.
FROM ubuntu:26.04

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

# The CLI is world-executable, but the daemon socket (/var/pwrstatd.ipc) is
# 0755 root:root, and connecting to a unix socket requires write permission on
# the inode — which only the owner holds. Run as root, and do not drop
# CAP_DAC_OVERRIDE in case the mounted socket's owner does not map to uid 0 here.
USER root

ENTRYPOINT ["/usr/local/bin/ups-mqtt"]

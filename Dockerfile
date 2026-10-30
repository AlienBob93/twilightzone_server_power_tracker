# syntax=docker/dockerfile:1

# ---- build stage ----------------------------------------------------------
FROM golang:1.27-alpine AS build

ARG VERSION=dev
WORKDIR /src

# Dependency layer: rebuilds only when go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/ups-monitor ./cmd/ups-monitor

# ---- runtime stage --------------------------------------------------------
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S ups \
    && adduser -S -G ups ups \
    && mkdir -p /var/lib/ups-monitor /etc/ups-monitor \
    && chown -R ups:ups /var/lib/ups-monitor

COPY --from=build /out/ups-monitor /usr/local/bin/ups-monitor
COPY config.yaml /etc/ups-monitor/config.yaml

USER ups

VOLUME ["/var/lib/ups-monitor"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["ups-monitor"]
CMD ["-config", "/etc/ups-monitor/config.yaml"]

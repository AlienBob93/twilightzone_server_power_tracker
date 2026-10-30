FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ups-mqtt ./cmd/ups-mqtt

FROM alpine:3.21
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates
COPY --from=build /ups-mqtt /usr/local/bin/ups-mqtt
USER app
ENTRYPOINT ["/usr/local/bin/ups-mqtt"]

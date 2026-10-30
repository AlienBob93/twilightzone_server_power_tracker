BINARY  := ups-monitor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test vet fmt tidy clean docker-build docker-up docker-down docker-logs

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

run:
	go run ./cmd/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal ./web

tidy:
	go mod tidy

clean:
	rm -rf bin

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t twilightzone/ups-monitor:$(VERSION) .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

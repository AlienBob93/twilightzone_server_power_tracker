# Work log

## 2026-10-29 — Option A scaffolding

Implemented the Option A topology: NUT runs natively on the host and the Go app runs in Docker, reaching `upsd` over TCP 3493.

- Added a NUT protocol client (`LIST UPS`, `LIST VAR`, `GET VAR`, optional auth).
- Added a configurable collector that maps NUT variables, estimates power draw, and prunes old samples.
- Added a SQLite store, JSON API, and embedded dashboard with charts and auto-refresh.
- Added container/deployment files (`Dockerfile`, `docker-compose.yml`, `config.yaml`) and host-side NUT configs under `deploy/nut/`.
- Added unit and integration tests; `go vet`, `go test ./...`, and a CGO-free build pass.

Docker was unavailable, so image/compose startup was not exercised.

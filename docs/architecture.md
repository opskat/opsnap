# Architecture

> Quick map: [`../AGENTS.md`](../AGENTS.md#architecture). Product requirements: [`specs/2026-09-23-opsnap-v1.md`](specs/2026-09-23-opsnap-v1.md).

## Layering and dependency direction

```text
internal/api (request/response types + router.go)
        │ router.go binds
        ▼
internal/controller ──> internal/service ──> internal/repository ──> db.Ctx(ctx) (GORM / SQLite)
```

| Constraint | Concrete form | Enforcement |
|---|---|---|
| Controllers only forward | methods shaped `func (c *X) M(ctx, *api.Req) (*api.Resp, error)` that call `xxx_svc.X().M(...)` | review-only |
| Controllers do not import `internal/repository` | test files excepted (they register mocks) | `internal/archtest` |
| No upward imports | services never import controllers; repositories never import services or controllers | `internal/archtest` |
| `internal/api` does not depend on business layers | only `internal/api/router.go` may import controllers | `internal/archtest` |
| Services reach dependencies through getters | `system_repo.System()`; the implementation is registered in `cmd/opsnap/main.go` via `RegisterSystem(NewSystem())` | review-only |

There are no exemptions beyond those listed; any future debt is enumerated in the rule and only shrinks.

## Subsystems

### HTTP and routing

cago's `mux.HTTP(api.Router)` starts Gin. `internal/api/router.go` mounts every business endpoint under `/api/v1`; each request struct declares its path and method with a `mux.Meta` tag (e.g. `HealthRequest` in `internal/api/system/system.go`). cago's built-in `/health` only returns `ok`; the health check with version and metadata database status is `/api/v1/system/health`.

### Embedded frontend

`internal/web` embeds the frontend build with `//go:embed all:dist` and registers a Gin `NoRoute` handler through `mux.RegisterMiddleware(web.Register)`:

- existing static files are served as-is
- other GET requests fall back to `index.html` for client-side routing
- unknown paths under `/api/` and non-GET requests stay 404
- before the frontend is built (`dist/` holds only `.gitkeep`) it answers with a "run make build" hint

### Startup order

`cmd/opsnap/main.go` registers, in order: `component.Core()` (logging) → creating the SQLite data directory → `component.Database()` → migrations → the HTTP server. The config file defaults to `./configs/config.yaml` and can be set with `-c`. The version is injected at build time into `github.com/cago-frame/cago/configs.Version` (the `Makefile` uses `git describe`).

## Extension recipes

### Add an endpoint

1. Define the request (with `mux.Meta`) and response structs in `internal/api/<domain>/`.
2. Write the controller test in `internal/controller/<domain>_ctr/`: a `setupXxxTest` that registers mock repositories and binds the controller on `muxtest.NewTestMux()`. Run it and watch it fail.
3. For data access, define the interface in `internal/repository/<domain>_repo/` with a `//go:generate mockgen ...` directive and run `make generate`.
4. Implement the service (`internal/service/<domain>_svc/`) and the controller until the test passes.
5. `Bind` the handler in `internal/api/router.go`; register a new repository in `cmd/opsnap/main.go`.
6. Run `go test ./...` and `make lint`.

Reference: the health check (`internal/api/system`, `internal/controller/system_ctr`, `internal/service/system_svc`, `internal/repository/system_repo`).

## Data and migrations

- The metadata database is SQLite at the path in `db.dsn`, `./runtime/opsnap.db` by default (WAL mode, 5-second busy timeout). Its directory is created at startup.
- Migrations live in `migrations/`: add a function returning `*gormigrate.Migration` and append it to the `RunMigrations` argument list. Never edit a released migration; use deterministic SQL, never `AutoMigrate(&entity)`, so later entity changes cannot alter old migrations.
- No business tables exist yet; `RunMigrations` returns immediately while the list is empty.

## Generated output

| Path | Source | Regenerate |
|---|---|---|
| `internal/repository/*/mock/*.go` | `//go:generate mockgen` on each repository interface | `make generate` |
| `internal/web/dist/` (only `.gitkeep` tracked) | `frontend/` | `make build-web` |

## Related

[`develop.md`](develop.md) · [`testing.md`](testing.md) · [`../AGENTS.md`](../AGENTS.md)

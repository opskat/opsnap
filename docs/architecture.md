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

### Authentication

Requirements: [`specs/2026-09-23-admin-auth.md`](specs/2026-09-23-admin-auth.md).

- `internal/api/router.go` has three groups under `/api/v1`:
  - `public`: only the endpoints the spec lists as public;
  - `authed`: behind `middleware.Auth()`, which accepts either a browser session or `Authorization: Bearer <API token>`; business endpoints go here;
  - `account`: `authed` plus `middleware.RequireSession()`, for password change, token management and sign-in methods; API tokens get 403.

  `internal/api/router_test.go` swaps both middlewares for sentinels and fails if a non-public route skips `Auth`, or if the set of session-only routes differs from its list. Adding a public or session-only endpoint means changing the spec and the test's list first.
- `middleware.SameOrigin()` runs on the whole `/api/v1` group and rejects writes whose `Origin` differs from the request host (403).
- Sessions: a random token in the HttpOnly, SameSite=Lax cookie `opsnap_session`; the `sessions` table stores only its SHA-256. A session expires after 7 days without use and is extended at most once a minute. Handlers read the caller from `authctx.From(ctx)`.
- First-run setup: while no administrator exists, startup logs `OpsNap 设置码：XXXX-XXXX-XXXX` (a new code on every start, held only in memory). The `admins` table allows a single row (`id = 1`), so concurrent setups cannot both succeed.
- Passwords are hashed with argon2id (`internal/pkg/password`, OWASP parameters: 19 MiB, t=2, p=1). A login with an unknown username still runs one hash check, so response time does not reveal whether the username exists.
- Login protection (`auth_svc` `loginGuard`, memory only): 5 consecutive failures from one client IP within 15 minutes (wrong password or wrong setup code) lock that IP out of password login and first-run setup for 15 minutes (HTTP 429); a success clears the count. The account itself is never locked.
- API tokens (`token_svc`, table `api_tokens`): `onp_` followed by 32 random bytes in base64url; only the SHA-256 and the first 8 characters (for display) are stored. Validity is 30, 90 or 365 days or never. Revocation keeps the row. Last use is recorded with minute precision. Token requests skip the `Origin` check because they carry no cookie.
- OIDC (`oidc_svc`, settings rows `oidc_provider` and `oidc_binding`): one provider, one bound identity (issuer + subject). Saving the provider fetches its discovery document; the client secret is encrypted with the master key and never returned. `GET /auth/oidc/login` (public) and `GET /auth/oidc/bind` (session only) redirect to the IdP with state, nonce and PKCE S256; pending requests live in memory for 10 minutes and are single-use. `GET /auth/oidc/callback` (public) verifies the ID token (signature, issuer, audience, expiry, nonce) and redirects: a login to its `next` with a session cookie, a binding to `/settings?oidc=bound`, a failure to `/login` or `/settings` with `oidc_error=not_bound|idp|invalid|unreachable`. Changing the issuer or client ID of a bound provider needs `confirm_reset` and removes the binding.
- Password sign-in switch (settings row `password_login_disabled`, `PUT /auth/password-login`, session only): it can be turned off only after an identity is bound and has signed in through OIDC at least once. Removing the binding (unbind, or a confirmed issuer/client ID change) and `opsnap admin reset-password` turn it back on, so the administrator always keeps a way in. While off, `POST /auth/login` returns 403 and the login page shows only the OIDC button.
- Password changes (`POST /api/v1/auth/password`) need the current password (a wrong one counts toward login protection) and delete every other session. `opsnap admin reset-password` (`cmd/opsnap/admin.go`) opens only the logger and database components, runs migrations, sets the new password and deletes every session; it works while the server is running.
- Client IP: `middleware.ConfigureEngine` trusts only the proxies listed in `http.trustedProxies` (IP or CIDR, empty by default) and reads only `X-Forwarded-For` from them; otherwise the IP is the connection's peer address.

### Embedded frontend

`internal/web` embeds the frontend build with `//go:embed all:dist` and registers a Gin `NoRoute` handler through `mux.RegisterMiddleware(web.Register)`:

- existing static files are served as-is
- other GET requests fall back to `index.html` for client-side routing
- unknown paths under `/api/` and non-GET requests stay 404
- before the frontend is built (`dist/` holds only `.gitkeep`) it answers with a "run make build" hint

### Startup order

`cmd/opsnap/main.go` registers, in order: `component.Core()` (logging) → creating the SQLite data directory → `component.Database()` → migrations → loading the master key → printing the setup code (only while no administrator exists) → the HTTP server. A component that fails stops startup with one `启动失败: ...` line and exit status 1. The config file defaults to `./configs/config.yaml` and can be set with `-c`. The version is injected at build time into `github.com/cago-frame/cago/configs.Version` (the `Makefile` uses `git describe`).

### Request language

`internal/middleware.Language()` runs on every request and picks the error-message language from the first `Accept-Language` entry: English for `en*`, Chinese otherwise. Error codes and both translations live in `internal/pkg/code`; `code_test` fails when a code lacks either translation. Return errors with `i18n.NewError(ctx, code.X)` (or the status-specific variants).

### Master key and encryption

Secrets that must be recovered later (OIDC client secret, later storage and data-source credentials) are encrypted with AES-256-GCM through `secret_svc.Secret().Encrypt/Decrypt`. Values that never need recovering (passwords, session IDs, API tokens) are hashed instead.

- Key source: the environment variable `OPSNAP_MASTER_KEY` (base64 of 32 random bytes) when set; otherwise `master.key` in the data directory (the directory holding the SQLite file), generated with mode 0600 on first start.
- The `settings` row `master_key_check` holds a value encrypted with the key. At startup the key must decrypt it; otherwise startup fails with "主密钥与数据库不匹配". If that row exists but neither the file nor the variable is present, startup fails with "主密钥缺失" and no new key is generated.
- Primitives live in `internal/pkg/secret`; loading and checking live in `internal/service/secret_svc`.

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
- Tables: `settings` (key/value system settings, including the master key check value), `admins` (the single administrator), `sessions` (browser sessions, token hashes only), `api_tokens` (API tokens, hashes only).

## Generated output

| Path | Source | Regenerate |
|---|---|---|
| `internal/repository/*/mock/*.go` | `//go:generate mockgen` on each repository interface | `make generate` |
| `internal/web/dist/` (only `.gitkeep` tracked) | `frontend/` | `make build-web` |

## Related

[`develop.md`](develop.md) · [`testing.md`](testing.md) · [`../AGENTS.md`](../AGENTS.md)

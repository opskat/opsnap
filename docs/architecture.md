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
  - `account`: `authed` plus `middleware.RequireSession()`, for password change, token management, sign-in methods, revealing storage keys and browsing host directories; API tokens get 403.

  `internal/api/router_test.go` swaps both middlewares for sentinels and fails if a non-public route skips `Auth`, or if the set of session-only routes differs from its list. Adding a public or session-only endpoint means changing the spec and the test's list first.
- `middleware.SameOrigin()` runs on the whole `/api/v1` group and rejects writes whose `Origin` differs from the request host (403).
- Sessions: a random token in the HttpOnly, SameSite=Lax cookie `opsnap_session`; the `sessions` table stores only its SHA-256. A session expires after 7 days without use and is extended at most once a minute. Handlers read the caller from `authctx.From(ctx)`.
- First-run setup: while no administrator exists, startup logs `OpsNap 设置码：XXXX-XXXX-XXXX` (a new code on every start, held only in memory). The `admins` table allows a single row (`id = 1`), so concurrent setups cannot both succeed.
- Passwords are hashed with argon2id (`internal/pkg/password`, OWASP parameters: 19 MiB, t=2, p=1). A login with an unknown username still runs one hash check, so response time does not reveal whether the username exists.
- Login protection (`auth_svc` `loginGuard`, memory only): 5 consecutive failures from one client IP within 15 minutes (wrong password or wrong setup code) lock that IP out of password login and first-run setup for 15 minutes (HTTP 429); a success clears the count. The account itself is never locked.
- API tokens (`token_svc`, table `api_tokens`): `onp_` followed by 32 random bytes in base64url; only the SHA-256 and the first 8 characters (for display) are stored. Validity is 30, 90 or 365 days or never. Revocation keeps the row. Last use is recorded with minute precision. Token requests skip the `Origin` check because they carry no cookie.
- OIDC (`oidc_svc`, settings rows `oidc_provider` and `oidc_binding`): one provider, one bound identity (issuer + subject). Saving the provider fetches its discovery document; the client secret is encrypted with the master key and never returned. `GET /auth/oidc/login` (public) and `GET /auth/oidc/bind` (session only) redirect to the IdP with state, nonce and PKCE S256; pending requests live in memory for 10 minutes and are single-use. `GET /auth/oidc/callback` (public) verifies the ID token (signature, issuer, audience, expiry, nonce) and redirects: a login to its `next` with a session cookie, a binding to `/settings?oidc=bound`, a failure to `/login` or `/settings` with `oidc_error=not_bound|idp|invalid|unreachable`. Changing the issuer or client ID of a bound provider needs `confirm_reset` and removes the binding.
- Password sign-in switch (settings row `password_login_disabled`, `PUT /auth/password-login`, session only): it can be turned off only after an identity is bound and has signed in through OIDC at least once. Removing the binding (unbind, or a confirmed issuer/client ID change) and `opsnap admin reset-password` turn it back on, so the administrator always keeps a way in. While off, `POST /auth/login` returns 403 and the login page shows only the OIDC button.
- Re-verification before revealing a storage key (`POST /storages/:id/reveal`, session only): while password sign-in is on, the administrator password is checked through the same login protection (wrong passwords count, lockouts apply). While it is off, `GET /auth/oidc/reauth?next=...` (session only) runs the OIDC flow in `reauth` mode; a callback with the bound identity grants the initiating session one reveal within 5 minutes (`auth_svc` `GrantReauth` / `ConsumeReauth`, memory only) and redirects to `next`; failures go back to the `next` page with `oidc_error`.
- Password changes (`POST /api/v1/auth/password`) need the current password (a wrong one counts toward login protection) and delete every other session. `opsnap admin reset-password` (`cmd/opsnap/admin.go`) opens only the logger and database components, runs migrations, sets the new password and deletes every session; it works while the server is running.
- Client IP: `middleware.ConfigureEngine` trusts only the proxies listed in `http.trustedProxies` (IP or CIDR, empty by default) and reads only `X-Forwarded-For` from them; otherwise the IP is the connection's peer address.

### Embedded frontend

`internal/web` embeds the frontend build with `//go:embed all:dist` and registers a Gin `NoRoute` handler through `mux.RegisterMiddleware(web.Register)`:

- existing static files are served as-is
- other GET requests fall back to `index.html` for client-side routing
- unknown paths under `/api/` and non-GET requests stay 404
- before the frontend is built (`dist/` holds only `.gitkeep`) it answers with a "run make build" hint

### Startup order

`cmd/opsnap/main.go` registers, in order: `component.Core()` (logging) → creating the SQLite data directory → `component.Database()` → migrations → loading the master key → setting the kopia directory (`<data dir>/kopia`) → printing the setup code (only while no administrator exists) → the HTTP server. A component that fails stops startup with one `启动失败: ...` line and exit status 1. The config file defaults to `./configs/config.yaml` and can be set with `-c`. The version is injected at build time into `github.com/cago-frame/cago/configs.Version` (the `Makefile` uses `git describe`).

### Request language

`internal/middleware.Language()` runs on every request and picks the error-message language from the first `Accept-Language` entry: English for `en*`, Chinese otherwise. Error codes and both translations live in `internal/pkg/code`; `code_test` fails when a code lacks either translation. Return errors with `i18n.NewError(ctx, code.X)` (or the status-specific variants).

### Master key and encryption

Secrets that must be recovered later (OIDC client secret, later storage and data-source credentials) are encrypted with AES-256-GCM through `secret_svc.Secret().Encrypt/Decrypt`. Values that never need recovering (passwords, session IDs, API tokens) are hashed instead.

- Key source: the environment variable `OPSNAP_MASTER_KEY` (base64 of 32 random bytes) when set; otherwise `master.key` in the data directory (the directory holding the SQLite file), generated with mode 0600 on first start.
- The `settings` row `master_key_check` holds a value encrypted with the key. At startup the key must decrypt it; otherwise startup fails with "主密钥与数据库不匹配". If that row exists but neither the file nor the variable is present, startup fails with "主密钥缺失" and no new key is generated.
- Primitives live in `internal/pkg/secret`; loading and checking live in `internal/service/secret_svc`.

### kopia repositories

Requirements: [`specs/2026-09-24-storage.md`](specs/2026-09-24-storage.md). kopia v0.23.1 is embedded as a Go library; `internal/pkg/kopiarepo` is the only package that imports it.

- `Location` describes a local directory or an S3-compatible bucket and prefix. `Normalize` cleans the path, lowercases the endpoint and makes a non-empty prefix end with `/`; `Key()` identifies a location for uniqueness checks.
- `Probe` checks reachability and writability (it writes and deletes a `.opsnap-write-test-*` object) and classifies the location as empty (including a missing local directory), a kopia repository (with the write time of `kopia.repository`) or non-empty. Failures are `*LocationError` with a `Reason`.
  - Local directories are inspected with the `os` package, never through kopia's filesystem backend, which writes a `.shards` file on first access. A local repository is recognised by `<dir>/kopia.repository.f`.
  - S3 is inspected with minio-go directly (one retry), because kopia's S3 backend treats a missing bucket as an empty one.
- `Create` probes again and gives up with `ErrNotEmpty` / `ErrAlreadyRepository` rather than overwrite; it then initialises an AES256-GCM-HMAC-SHA256 repository with the storage key as password.
- `Manager.Verify` connects with a key and returns the snapshot count (`ErrInvalidPassword` for a wrong key). The repository log is disabled, so opening writes nothing to the repository. Per-storage kopia config and cache live under `<data dir>/kopia/<storage id>/`; id 0 uses a throwaway directory. `Manager.Remove` deletes that directory only.
- `ListDirs` / `Mkdir` back the directory picker: subdirectories only (symlinks to directories included), each tagged empty, repository, non-empty, not writable or no access; new folders are created with mode 0700.
- `GenerateKey` returns 24 alphanumerics in dash-separated groups of four; `Fingerprint` is the first and last four uppercase hex digits of the key's SHA-256.

### Storage management

`storage_svc` (table `storages`, error codes 10400–10499) builds on `kopiarepo`; routes are in the `authed` group, so API tokens can manage storages.

- Name and location are unique; `location_key` (a unique column) holds `Location.Key()`. The S3 secret key and the repository key are stored encrypted with the master key and never returned. A blank secret key on edit keeps the saved one.
- `POST /storages/probe` validates fields and uniqueness, then reports `empty`, `repository` or `not_empty`; the frontend picks the next dialog from it. `POST /storages` and `PUT /storages/:id` probe again on the server before acting, so a stale answer never overwrites data: an empty location gets a new repository (on create, only with `confirm_saved` and a key of at least 12 characters; on a location change, with the current managed key), an existing repository needs the submitted key, and a non-empty location is refused. A location change needs `confirm_location_change`.
- `POST /storages/:id/test` records the outcome in `status` (`ok` / `wrong_key` / `unreachable`) with `status_code` and `status_detail`; the list only shows the last result and never tests on load. `POST /storages/:id/unlock` replaces the managed key after a successful open.
- Delete removes the row and `<data dir>/kopia/<id>/`; the repository data stays.
- Session-only (`account` group): `POST /storages/:id/reveal`, and `GET` / `POST /storages/dirs` for the directory picker. Browsing with no path, or a path that is not an existing directory, opens the parent of the data directory.

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

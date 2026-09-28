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

`cmd/opsnap/main.go` registers, in order: `component.Core()` (logging) → creating the SQLite data directory → `component.Database()` → migrations → loading the master key → setting the kopia directory (`<data dir>/kopia`, which also removes `tmp-write-*` / `tmp-verify-*` kopia configs left by a crash) → setting the tools directory → printing the setup code (only while no administrator exists) → the backup-job scheduler (see "Backup jobs": it sweeps `<data dir>/runs`, settles runs left by the previous process, catches up missed schedules and handles "run once now" before HTTP accepts requests) → the HTTP server. On SIGINT/SIGTERM cago closes components in reverse order, so HTTP stops before the scheduler, then waits up to 10 s (`gogo.Wait`) for background work. A component that fails stops startup with one `启动失败: ...` line and exit status 1. The config file defaults to `./configs/config.yaml` and can be set with `-c`. The version is injected at build time into `github.com/cago-frame/cago/configs.Version` (the `Makefile` uses `git describe`).

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
- Writing backups (requirements: [`specs/2026-09-27-backup-jobs.md`](specs/2026-09-27-backup-jobs.md), section 执行):
  - `Manager.OpenWriter(ctx, loc, key) (*Writer, error)` connects read-write (same errors as `Verify`) with its config in a one-off 0700 directory `<data dir>/kopia/tmp-write-*/`, so concurrent sessions never share a config. `Writer.Close(ctx)` deletes that directory; `NewManager` removes `tmp-write-*` and `tmp-verify-*` directories left by a crash, so plaintext S3 credentials never outlive the operation. The client identity is fixed to `opsnap@opsnap`.
  - `Writer.WriteSnapshot(ctx, SnapshotRequest{Prefix, Tags, Compression, Files}) (*SnapshotResult, error)` streams `[]SnapshotFile{Name, Reader}` into one snapshot of source `opsnap@opsnap:/<Prefix>`. Files are read one at a time in the given order, each to EOF before the next is touched, so a caller can start each dump lazily. `SnapshotTags{JobID, RunID, Type, Kind}` are stored as kopia user tags `tag:job`, `tag:run`, `tag:type`, `tag:kind` (`kopia snapshot list --tags job:<id>` filters on them). `Compression` is `CompressionNone`, `CompressionGzip` or `CompressionZstd` (empty means zstd); anything else, a missing prefix, no files, or an empty, duplicate or `/`-containing file name fails with `ErrInvalidSnapshot` before anything is written.
  - After saving the manifest it refreshes from storage and reads the snapshot back: the set of files, each file's size against the bytes streamed, and that every content of each file is in the repository index. Only then does it return `SnapshotResult{ID, Files []FileResult{Name, Size}, UploadedBytes}`, where `UploadedBytes` counts bytes newly uploaded by this write (after dedup and compression).
  - No manifest survives a failure: a reader error is returned as-is, a cancelled `ctx` returns `context.Canceled`, and both abort the write session without flushing; a failed read-back deletes the saved manifest and returns an error wrapping `ErrVerify`. Uploaded packs left behind are orphans for maintenance to reclaim. kopia's mid-upload checkpoints are disabled because they would save incomplete manifests and apply kopia's own retention policy.
- Retention, deletion, maintenance and usage (requirements: the same spec, sections 保留, 任务详情 and 编辑、暂停、删除), all methods of `Writer`:
  - `JobRef{JobID, Prefix}` names a job's snapshots: source `opsnap@opsnap:/<Prefix>` (slashes trimmed as in `WriteSnapshot`) **and** `tag:job` equal to `JobID`. Other jobs' snapshots, snapshots from other kopia clients, and an old job's snapshots under another prefix never match.
  - `ListJobSnapshots(ctx, JobRef) ([]SnapshotInfo, error)` returns `SnapshotInfo{ID, StartTime, Tags}` sorted by start time, oldest first.
  - `DeleteSnapshots(ctx, JobRef, ids) (int, error)` deletes the given manifests in one write session. Any id that is missing or not the job's fails the whole call with `ErrNotJobSnapshot` before anything is deleted. `DeleteJobSnapshots(ctx, JobRef) (DeleteResult{Deleted, Failed}, error)` deletes all of a job's snapshots; if the session fails, `Failed` is the number found and the repository is unchanged. Deleting frees no space until maintenance runs.
  - `Maintain(ctx, MaintenanceQuick | MaintenanceFull) error` runs kopia snapshot maintenance with `force` (the repository's maintenance owner may be unset or another kopia client) and `SafetyFull` (other jobs may be writing concurrently, so unreferenced data is reclaimed only after two full cycles, a day or more after deletion). kopia locks `<session config>.mlock`, so it needs the writer's config directory, which lives until `Close`. Because every session has its own config directory, `Manager` also serialises maintenance per location within the process.
  - `Usage(ctx, ids) (*Usage{ExportBytes, PackedBytes}, error)`: `ExportBytes` sums each snapshot's file sizes; `PackedBytes` is the packed (deduplicated, compressed) size of the contents those files reference, counted once. It walks every referenced content, so its cost grows with snapshot size. Duplicate ids count once; a missing id is an error.
- `internal/pkg/retention` is a pure selector: `Select(snaps []Snapshot{ID, Time}, Policy{Days, Weeks, Months}, loc, now) (keep, remove []string)`, both in input order. `now` is the run's start time. Snapshots at or after `now - Days×24h` are kept. Older snapshots are grouped by calendar week (Monday start) and calendar month in the job's time zone `loc`. The last snapshot of each group is kept for the `Weeks` most recent weeks and `Months` most recent months, both counting the week or month that contains `now`. The newest snapshot is always kept. A value ≤ 0 disables that rule.
- `ListDirs` / `Mkdir` back the directory picker: subdirectories only (symlinks to directories included), each tagged empty, repository, non-empty, not writable or no access; new folders are created with mode 0700.
- `GenerateKey` returns 24 alphanumerics in dash-separated groups of four; `Fingerprint` is the first and last four uppercase hex digits of the key's SHA-256.

### Storage management

`storage_svc` (table `storages`, error codes 10400–10499) builds on `kopiarepo`; routes are in the `authed` group, so API tokens can manage storages.

- Name and location are unique; `location_key` (a unique column) holds `Location.Key()`. The S3 secret key and the repository key are stored encrypted with the master key and never returned. A blank secret key on edit keeps the saved one.
- `POST /storages/probe` validates fields and uniqueness, then reports `empty`, `repository` or `not_empty`; the frontend picks the next dialog from it. `POST /storages` and `PUT /storages/:id` probe again on the server before acting, so a stale answer never overwrites data: an empty location gets a new repository (on create, only with `confirm_saved` and a key of at least 12 characters; on a location change, with the current managed key), an existing repository needs the submitted key, and a non-empty location is refused. A location change needs `confirm_location_change`.
- `POST /storages/:id/test` records the outcome in `status` (`ok` / `wrong_key` / `unreachable`) with `status_code` and `status_detail`; the list only shows the last result and never tests on load. `POST /storages/:id/unlock` replaces the managed key after a successful open.
- Delete removes the row and `<data dir>/kopia/<id>/`; the repository data stays.
- Session-only (`account` group): `POST /storages/:id/reveal`, and `GET` / `POST /storages/dirs` for the directory picker. Browsing with no path, or a path that is not an existing directory, opens the parent of the data directory.

### Network chains

Requirements: [`specs/2026-09-25-datasources.md`](specs/2026-09-25-datasources.md). `internal/pkg/netchain` connects through an ordered chain of network channels (SSH jump hosts and SOCKS5 proxies, mixed freely).

- `NewChain([]Hop)` validates before any network I/O: at most `MaxHops` (5) hops (`ErrTooManyHops`), no channel twice (`ErrCycle`, by non-zero `Hop.ID`), complete fields (`ErrInvalidHop`), and a parseable SSH private key. `ParsePrivateKey` accepts OpenSSH and PEM RSA, ECDSA and Ed25519 keys and tells `ErrPassphraseMissing`, `ErrPassphraseWrong` and `ErrKeyInvalid` apart.
- `Chain.Connect(ctx)` establishes every hop in order and returns a `Tunnel`; the whole attempt is bounded by `ctx`, or by `DefaultTimeout` (30 s) when it has no deadline. `Tunnel.Dial(ctx, network, addr)` has the pgx `DialFunc` signature; `Tunnel.DialSSH(ctx, target)` logs in to a final SSH host (numbered hop `len(hops)+1`) under the same rules as an SSH hop.
- SSH host keys are checked during key exchange, before any authentication request: an empty `Hop.HostKey` fails with `ErrHostKeyUnknown`, a different one with `ErrHostKeyChanged`, both as `*HostKeyError` carrying the presented `SHA256:` fingerprint, key type and saved fingerprint. No credential is sent and nothing is forwarded through that host.
- SOCKS5 hops send target host names unresolved (socks5h) and support no authentication or username/password.
- Every failure is a `*HopError` with the 1-based hop index, the channel's ID, name, kind and address, and a `Reason` (`unreachable`, `timeout`, `canceled`, `protocol`, `negotiation`, `auth_failed`, `host_key_unknown`, `host_key_changed`, key reasons). It never contains secrets; `Hop` also formats without them.

`internal/pkg/fakessh` is the in-process SSH server (password and public-key auth, `exec` with `uname -sm` answered by default, `direct-tcpip`, host key rotation) and SOCKS5 proxy used by these tests; it records authentication attempts, commands and forwarding targets so tests can assert that nothing was sent. `tools/fakessh` wraps it into a standalone binary (`bin/fakessh`, built by `make e2e`) for Playwright: fixed test credentials via flags, plausible exec output for the capability-probing commands below, and an optional control HTTP port to rotate the host key on demand (`e2e/tests/sources.spec.ts`).

### Network channels

Requirements: [`specs/2026-09-25-datasources.md`](specs/2026-09-25-datasources.md), section 网络通道. `channel_svc`/`channel_ctr` (table `channels`, error codes 10500–10527) persist reusable hops on top of `netchain`; routes are in the `authed` group, so API tokens can manage channels.

- A channel is one hop: an SSH jump host (password or private key, with the same OpenSSH/PEM parsing as `netchain`) or a SOCKS5 proxy (no auth, or username/password). `via_id` lets a channel go through another channel, composing a chain up to `netchain.MaxHops` (5); saving rejects a cycle or a chain that would exceed the limit, and names the failing hop.
- Name and port are validated; secrets are encrypted with the master key and never returned, a blank secret on edit keeps the saved one, and switching auth method (password ↔ key) discards the credentials for the method no longer used.
- `POST /channels/probe`, `POST /channels`, `PUT /channels/:id` and `POST /channels/:id/test` all dial the real chain before acting: an edit that would fail leaves the stored channel untouched. An unknown or changed SSH host key on any hop pauses with a successful response carrying `host_key` (hop, fingerprint, whether it changed, the saved one) instead of an error; the caller resubmits with that fingerprint to accept it, `POST /channels/:id/host-key` reconfirms a changed key on its own. Nothing is sent to a host whose key has not been accepted (see `netchain`'s `HostKeyError`).
- `used_by` (other channels and data sources referencing this one, via `channel_svc.SetDataSourceReferrer`) blocks delete while non-empty. When a channel's accepted host key changes, every data source that reaches it through this channel is marked `host_key_changed` too (`channel_svc.SetHostKeyChangedHook`); confirming the channel's new key re-tests those data sources synchronously in the same request (bounded concurrency).

### Capability probing

Requirements: [`specs/2026-09-25-datasources.md`](specs/2026-09-25-datasources.md), section 能力探测. `internal/pkg/probe` takes an already-opened `dsconn.Conn` and reports, per data source type, which backup capabilities are usable. It has no knowledge of scheduling, persistence or HTTP; `datasource_svc` (below) opens the connection, calls `probe.Run`, and is responsible for the "无法探测" (could not probe) state when opening the connection itself fails or times out.

- `Run(ctx, typ dsconn.Type, conn *dsconn.Conn) []Item` looks `typ` up in a registry populated by `Register`; MySQL, PostgreSQL and server-file probes are registered in `init()`. An unregistered type returns `nil`. `Run` never returns an error: a query that itself fails (e.g. insufficient privileges) becomes a `TierFail` item carrying that error, not a package-level failure.
- Each `Item` has a stable `Key` (e.g. `"mysql.binlog"`), a `Tier` (`TierOK` / `TierWarn` / `TierFail`), and `Title`/`Detail`/`Fix` as a `Text{ZhCN, En}` pair — both languages are computed here, in the probe package, so the frontend can render either without a separate translation table. `Fix` is empty when the tier is OK or the item has no fix (e.g. CPU architecture). Fix text is directly copy-pasteable: placeholders like the granted user or the sudo account are filled in from the live connection (`CURRENT_USER()`, `conn.SSH.User()`), not left as `<...>` templates. The non-InnoDB-tables item also carries `Tables` (at most 5 names) and `TableCount` (the true total).
- MySQL (8 items): version, `log_bin`, `binlog_format`, `gtid_mode`, `binlog_expire_logs_seconds` (warns below the 7-day/604800s default threshold, 0 meaning "never expires" counts as OK), `REPLICATION SLAVE`/`REPLICATION CLIENT` via `SHOW GRANTS FOR CURRENT_USER()`, non-InnoDB tables outside the system schemas, and `mysqldump` on the control host.
- PostgreSQL (6 items): version, `wal_level`, `max_wal_senders`, replication slot margin (`max_replication_slots` − in-use, fails by computing `used + 2` into the fix), the connected role's `REPLICATION` attribute or superuser status, and `pg_dump` on the control host.
- Server file (4 items): SSH reachable (trivially OK — `Run` is only called on an already-authenticated connection), CPU architecture parsed from the `uname -sm` output `dsconn` already read while opening the connection (only `amd64`/`arm64` are OK), whether the temp directory allows writing and executing a program, and passwordless `sudo -n true`. The temp-directory check is the only write this package performs anywhere — a single shell one-liner that creates a temp file with `mktemp`, `chmod`s and runs it, and removes it in the same command regardless of outcome; it is exercised through `internal/pkg/fakessh` with a custom `ExecFunc`.
- Control-host tools (`mysqldump`, `pg_dump`) are looked up first on `PATH`, then in the configured `tools.dir` (`configs/config.example.yaml`, set at runtime with `probe.SetToolsDir`) — preparing for a future Docker image that ships several client versions side by side. Found tools are version-compared against the server (major.minor for `mysqldump`, major only for `pg_dump`).
- Tests: pure tier/threshold decisions (`decide*` functions) are unit-tested without any network; MySQL 8.0 / PostgreSQL 16 on docker.lan are exercised read-only through `internal/pkg/testenv` and skip with a reason when unconfigured; server-file items run against `internal/pkg/fakessh`.

### Data sources

Requirements: [`specs/2026-09-25-datasources.md`](specs/2026-09-25-datasources.md), section 数据源. `datasource_svc`/`datasource_ctr` (table `datasources`, error codes 10600–10626) connect MySQL, PostgreSQL and server files through a `netchain` built from the caller's channel, then hand the opened connection to `internal/pkg/probe`; routes are in the `authed` group.

- Default ports (3306/5432/22, PostgreSQL defaulting to database `postgres`), TLS (`dsconn`'s five modes plus mTLS) for the two database kinds, and password/private-key auth for server files follow the same test-before-save, encrypted-secret, blank-keeps-saved and switch-discards-the-other-method rules as channels. `POST /datasources/probe`, `POST /datasources`, `PUT /datasources/:id` and `POST /datasources/:id/test` return server version, TLS info and, on success, start a background probe.
- A server-file data source's own target host has its own TOFU, independent of any channel host key on the way there: an unknown or changed key pauses the same way (`host_key` in the response, `POST /datasources/:id/host-key` to reconfirm) as a channel hop.
- When a channel on the path changes its host key, the data source is marked `host_key_changed` with `failed_hop.channel_id` naming that channel (nothing is forwarded through it); the frontend reconfirms it by calling the *channel's* `host-key` endpoint, not the data source's — see "Network channels" above for the synchronous re-test that follows.
- Capability probing (`probe_state`/`probe_items`/`probe_error`/`probe_time` on `datasources`) runs once per data source at a time, in the background after a successful save or `POST /datasources/:id/reprobe`, bounded by a 60 s timeout; `probe_state` is empty before the first probe, `done` with `probe_items` (JSON `[]probe.Item`) on success, or `unprobeable` with `probe_error` (secrets already stripped) when opening the connection itself fails or times out — a fresh failure clears any earlier items rather than showing stale ones. Concurrent reprobe requests for the same data source collapse into one run. "Probing" itself is tracked only in memory, so a restart never leaves a data source stuck in that state.
- `used_by` on a channel counts both other channels and data sources (`channel_svc.SetDataSourceReferrer`); a channel referenced by a data source cannot be deleted. `GET /datasources` lists rows (address, version, chain, status, probe summary); `GET /datasources/:id` adds the full chain (with per-hop fingerprints) and probe items for the detail page (`/sources/<id>`).

### Backup jobs

Requirements: [`specs/2026-09-27-backup-jobs.md`](specs/2026-09-27-backup-jobs.md). `job_svc`/`job_ctr` (tables `jobs` and `job_runs`, codes 10700–10739) turn a job into snapshots: `dump` → `kopiarepo` → retention and maintenance, driven by the scheduler. Routes are in the `authed` group. Creating a job holds `datasource_svc.LockReferences` and `storage_svc.LockReferences` from its checks to the insert, and deleting a data source or storage (or changing a storage's location) holds the same lock across its "not used by any job" check, so a new job never points at a deleted data source or storage. Schedules use IANA time zones; the binary embeds the time zone database (`time/tzdata`).

- Error codes: 10700–10725 job CRUD and field validation (10724 immutable data source/storage/prefix, 10725 delete refused while a run is active); 10726–10727 snapshot messages in the delete response; 10728 already running or queued, 10729 run not found, 10730 run already finished, 10731 stats could not read the storage; 10732–10739 are display texts only: the fixed run reasons (`job_runs.reason_code`: still running, retry voided, OpsNap restarted, timeout) and the durations inside the timeout reason, shown in the request's language, while `reason` keeps the Chinese text for the run log.
- `internal/pkg/dump` runs the official `mysqldump` / `pg_dump` / `pg_dumpall` (found on `PATH`, then in `tools.dir`) against a temporary `127.0.0.1` port forwarded through the data source's `netchain`, and hands each output file to the caller as a stream. Passwords and TLS material go only into 0600 files in a 0700 run directory `<data dir>/runs/run-*`, never on the command line; the tools get only `HOME` (the run directory), `PATH`, `LC_ALL` and the dynamic-library search path from OpsNap's environment, and `mysqldump` gets the database names after `--`; `Session.Close` kills the tools, closes the port and removes the directory, and `dump.Sweep` removes directories left by a crash. Completion markers and archive headers are checked (`ErrIncomplete`).
- One run (`job_svc.Runs()`): `Enqueue` records a queued run (at most one queued or running run per job, `ErrRunActive`); `Execute` goes 准备 (storage status, host key, and `dump.CheckTools`: tools found, PostgreSQL tools not older than the data source's last-tested major version, MariaDB `mysqldump` able to honour the TLS mode — all before any connection) → 连接数据源 (lists the databases: the scope for “整个实例”, and a missing “指定数据库” fails naming it) → 导出并写入仓库 (`Writer.WriteSnapshot` with job/run/type/kind tags, read back) → 校验 → 应用保留策略 (`retention.Select`, `DeleteSnapshots`, then quick maintenance; failures here only go to the log). Logs keep at most 1000 lines per run with secrets scrubbed; each job keeps its latest 1000 runs (queued and running runs are never trimmed). `Cancel` and the job's timeout stop the tools the same way; a timeout is a failure “超时（超过 X）”.
- The scheduler (`job_svc.Scheduler`, a cago component registered just before HTTP) owns the queue through `SetDispatcher`: at most 3 runs execute at once and the rest wait in trigger order (status queued). Each enabled job fires at `schedule.Spec.Prev` times in its own time zone; a time is due only if it is after the job's `createtime` and `enabled_at`, so paused periods and time before creation never fire. If the job already has a queued or running run, the scheduler records a `skipped` run (“上一次仍在运行”) instead.
- Retries: a failed run (including a timeout) is retried after the job's interval as a new run `retry` i/N until N is used up; canceled, skipped and interrupted runs are not retried. When the next scheduled time arrives first, the pending retry is dropped, and a retry already queued but not started is canceled (“下一次计划时间已到，重试作废”); the scheduled run executes instead. Pausing a job stops its scheduled times only; a pending retry still runs. Pending retries live in memory and do not survive a restart.
- Startup: `Runs().Recover` sweeps `<data dir>/runs` and marks runs left `running` as failed “OpsNap 重启，运行中断” (no retry) and runs left `queued` as canceled “OpsNap 重启”; then, for each enabled job, if the previous scheduled time is due as above and has no run record for that time (`job_runs.scheduled_at`, skips included), one `catchup` run is queued, however many times were missed; then jobs flagged `run_now` get one manual run. Shutdown stops firing and starting runs and cancels running ones, which end as failed “OpsNap 重启，运行中断”; queued ones stay queued for the next startup.
- Maintenance: every storage referenced by a job gets a full `Writer.Maintain(MaintenanceFull)` once a day, counted from when the scheduler first sees it and persisted in the `settings` row `storage_full_maintenance_<storage id>` (unix seconds, the time the maintenance was started). `kopiarepo` serialises maintenance with maintenance but not with writes, so the scheduler starts a full maintenance only when no run for that storage is executing, and runs for that storage wait while it is due or in progress (at most 1 hour).
- `next_run_at` in job responses is the next scheduled time from `schedule.Spec.Next` (empty while paused); it does not reflect queued runs or pending retries.
- `tools/fakepg` (`bin/fakepg`, built by `make e2e`) stands in for PostgreSQL in Playwright, since CI has no database and saving a PostgreSQL data source requires a live connection test. Run as `fakepg` it is a server built on pgx's `pgproto3` backend: cleartext password auth, no TLS (answers `N` to `SSLRequest`, like a server with `ssl=off`), simple and extended query protocol, and answers only the exact queries OpsNap sends from `dsconn`, `probe` (all 6 PostgreSQL items pass), `datasource_svc.ListDatabases` (databases `app`, `broken`, `postgres`) and `dump`'s exclusion check; anything else is an `0A000` error. Run through a `pg_dump` / `pg_dumpall` symlink it is the export tool: it reports the same major version as the server, connects through OpsNap's local forward with the libpq connection string and `PGPASSFILE` (so the forward and password file are exercised), and writes a `PGDMP` custom-format header or a globals file ending with the cluster-dump completion marker; dumping `broken` exits 1 with a server read error, which gives the e2e a failed run with a real log. Its Go tests drive the real `dsconn`, `probe`, `ListDatabases` and `dump.Start` against it.

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
- Tables: `settings` (key/value system settings, including the master key check value), `admins` (the single administrator), `sessions` (browser sessions, token hashes only), `api_tokens` (API tokens, hashes only), `channels` (network channels: SSH jump hosts and SOCKS5 proxies), `datasources` (MySQL/PostgreSQL/server-file data sources, including the `probe_state`/`probe_items`/`probe_error`/`probe_time` columns that hold the last capability-probe result — see "Capability probing" below; there is no separate probe table), `jobs` (backup jobs: data source, storage and prefix are fixed at creation; `enabled_at` is the latest enable or creation time; `run_now` is the pending "run once now" flag; `snapshot_count` caches the job's snapshot count), `job_runs` (one row per run: status, trigger with `retry_attempt`/`retry_total` and `scheduled_at`, timings in milliseconds, byte counts, snapshot ID, failed step, reason, `reason_code` for the fixed reasons shown in the viewer's language, and the JSON log; see "Backup jobs").

## Generated output

| Path | Source | Regenerate |
|---|---|---|
| `internal/repository/*/mock/*.go` | `//go:generate mockgen` on each repository interface | `make generate` |
| `internal/web/dist/` (only `.gitkeep` tracked) | `frontend/` | `make build-web` |

## Related

[`develop.md`](develop.md) · [`testing.md`](testing.md) · [`../AGENTS.md`](../AGENTS.md)

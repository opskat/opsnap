# E2E

## 1. Tracks

| | Smoke | Local runtime verification |
|---|---|---|
| Path | `e2e/tests/` (committed) | `e2e/scratch/` (gitignored) |
| Command | `make e2e` | `pnpm -C e2e scratch` |
| Scope | stable core regression flows | one-off check of a change or bug |
| External systems | none | docker.internal test services, with authorization |
| Output | CI verdict | `scratch/<scenario>/report.md` and evidence |

Promoting a scratch script to smoke is a separate decision. Smoke scope: app identity and startup, main navigation, one core CRUD flow with an independent persistence oracle, and one critical integrity flow. There is no business data yet; the last two are added when those features land.

## 2. Harness

```text
make e2e → make build (produces bin/opsnap) → pnpm -C e2e test
  → global-setup.ts: start the real bin/opsnap in a temp dir on a dedicated port,
    read the setup code from its log into OPSNAP_SETUP_CODE
  → project "setup": complete first-run setup in the UI, save the session to e2e/.auth/admin.json (gitignored)
  → project "chromium": the other specs, signed in through that saved state
  → project "logout": signs out (invalidates the saved session), so it runs last
  → Playwright (Chromium) drives pages and the API
  → assertions + independent oracle (/api/v1/system/health)
```

| Resource | Isolation |
|---|---|
| config and metadata database | a new `opsnap-e2e-*` directory under the system temp dir per run, deleted in teardown |
| port | dedicated port 18291 (`e2e/ports.ts`), away from the dev instance's 8210; setup checks every fixed port (OpsNap's and the fakes') before starting anything and fails naming the busy ones, so only one e2e can run on a host at a time |
| OIDC provider | `bin/fakeidp` (`tools/fakeidp`, built by `make e2e`) on port 18292 (`e2e/ports.ts`); `POST /control/next` sets how the next authorization behaves |
| SSH servers | `bin/fakessh` (`tools/fakessh`, wraps `internal/pkg/fakessh`, built by `make e2e`), started twice on ports 18296 and 18298 (`e2e/ports.ts`) as a network-channel jump host and a server-file target reached through it; the jump instance also runs a control HTTP port (18297) so `sources.spec.ts` can `POST /rotate-host-key` to simulate a changed host key |
| PostgreSQL and export tools | `bin/fakepg` (`tools/fakepg`, built by `make e2e`) on port 18294 (`e2e/ports.ts`) as the server for a PostgreSQL data source (databases `app`, `broken`, `postgres`); the same binary, symlinked as `pg_dump` and `pg_dumpall` into the temp directory and put first on the spawned `bin/opsnap`'s `PATH`, is the export tool: it connects to the server through OpsNap's port forward and writes a valid-looking archive, except that dumping `broken` fails |
| app identity | readiness requires the health check to return `code: 0` and `database: ok`, so another program on the port cannot pass |
| browser state | every test gets its own browser context (theme and language `localStorage` do not leak); specs in the `chromium` and `logout` projects start signed in, so anonymous scenarios pass `storageState: { cookies: [], origins: [] }` explicitly |
| locale | `zh-CN`; tests that check English switch the language themselves |

## 3. Smoke command and coverage

```bash
make install   # once: dependencies and Chromium
make e2e
```

Current scenarios:

- `setup.spec.ts`: an uninitialized instance redirects to the setup page; a wrong setup code is rejected; the real one creates the administrator and signs in with an HttpOnly, SameSite=Lax session cookie.
- `auth.spec.ts`: anonymous API calls get 401 while public endpoints stay reachable; anonymous page visits redirect to the login page keeping the target; a second setup gets 409; cross-site writes get 403.
- `tokens.spec.ts`: a token generated in Settings is shown once, calls the API with `Bearer`, gets 403 on token management, records its last use, and gets 401 "令牌已吊销" right after revocation.
- `oidc.spec.ts` (serial): an unreachable issuer is reported; a saved provider hides its secret; binding returns to Settings; the login page offers "使用 FakeIdP 登录" and returns to the requested page; an unbound identity and an IdP cancellation show their errors; after an OIDC sign-in, turning password sign-in off leaves only the OIDC button and makes password login return 403, and it is turned back on at the end.
- `jobs.spec.ts` (serial): with a PostgreSQL data source on `bin/fakepg` (its background probe passes all 6 items) and a local-directory storage in a temp directory, and the storage's connection test (kopia's own snapshot count) as the independent oracle: walking the five-step wizard to create a job, "Run now" from the detail page succeeding with a snapshot ID, a run log and one snapshot on the list, the detail page and in the repository; a job on `broken` whose run fails, showing the failed step, the tool's error and the log in the expanded run row, with no snapshot; pausing on the detail page and enabling from the list menu, persisting across reload; editing through `/jobs/:id/edit` (the PUT sends data source, storage and prefix as `0`/`""`, meaning unchanged) keeps them and saves the new name and retention; the list and a failed run in English and dark theme; deleting one job with its snapshots (only its own snapshot goes) and another without (its snapshot stays), then an empty list and "任务不存在" for a deleted job. It deletes its data source and storage afterwards, so later specs start from empty lists.
- `overview.spec.ts` (serial): its first describe block runs while the instance is still empty, since this file itself is first to touch the overview page: the "开始第一次备份" guide shows with all three steps undone, then a PostgreSQL data source (`bin/fakepg`) added through the API ticks step 1 ("已有 1 个数据源", button becomes "查看数据源"), then a local-directory storage ticks step 2 and enables "新建任务" (linking to `/jobs/new`); it deletes both afterwards so the guide is back to its start. The second describe block prepares a PostgreSQL data source on `bin/fakepg`, a local-directory storage in a temp directory and two jobs (one on `app`, one on `broken`) through the API, reads the overview before and after "Run now" on each job's detail page: the 24h success rate gains 2 runs and 1 failure, the protected data sources gain 1, "失败 N" gains 1 and filters to the failed run, the two runs lead the recent list (engine, data source address, status, "刚刚"), today's bar gains one success and one failure (hover tooltip), the storage row goes from 0 to 1 snapshot with its disk bar, and a row opens the job; the page in English and dark theme; finally it deletes its jobs (with snapshots), storage and data source and checks the overview is back to the numbers it read first (an `afterAll` repeats the cleanup if a test failed), so `storage.spec.ts` still starts from empty.
- `storage.spec.ts` (serial): with temp directories on the same host as the instance, and `kopia.repository.f` on disk as the independent oracle: empty state; creating a folder in the directory picker and choosing it; a generated key shown with its key file downloaded (file name, `Key:` line, kopia command) and no repository created before the confirmation checkbox; a non-empty directory refused; viewing the key needs the sign-in password; changing the location creates a repository at the new path and leaves the old one; deleting keeps the data, and adding the location again needs the key, counting wrong attempts; the page in English and dark theme.
- `sources.spec.ts` (serial): against the two `bin/fakessh` instances described above: creating an SSH network channel and confirming its host key on first connect; creating a server-file data source through that channel, confirming the target's own host key, and seeing its background capability probe finish (3 checks pass, passwordless sudo comes back as a warning with a fix); the detail page's connection chain (with fingerprints) and probe list; rotating the jump host's key, testing the data source picking up "host key changed" (naming the channel), the channel itself showing the same status, and reconfirming the channel's new key (which re-tests data sources routed through it); a channel still referenced by a data source cannot be deleted; the page in English and dark theme. MySQL/PostgreSQL data sources are not covered here (no database in CI; `jobs.spec.ts` uses a fake PostgreSQL); see [`../docs/verification.md`](../docs/verification.md#test-environment).
- `smoke.spec.ts`: health check, 404 for unknown API paths, the sidebar footer shows "服务运行中" with the same version and commit as the API, client routes load directly and survive reload, dark theme persists across reload, switching between Chinese and English.
- `logout.spec.ts` (serial, runs last): changing the password in Settings rejects a wrong current password, then signs out another session while this browser stays signed in; signing out returns to the login page and the old session gets 401; a wrong password shows an error and the right one returns to the page originally requested; five failures lock the client IP out (429), so nothing may sign in after it.

## 4. Protocol mocks

Smoke tests depend on no external system. The protocol fakes they need are Go programs in `tools/` (`fakeidp`, `fakessh`, `fakepg`), built by `make e2e`, started by `global-setup.ts` on the fixed ports in `ports.ts` and waited for before the tests start. Each implements only the protocol responses OpsNap uses; a fake with non-trivial behaviour has Go tests beside it (`go test ./tools/fakepg/`).

## 5. Orchestration and cleanup

`e2e/global-setup.ts` checks that the fixed ports are free, writes a temporary config, starts the fakes and `bin/opsnap`, waits for readiness and returns the teardown: SIGTERM to every process, SIGKILL after 5 seconds if OpsNap is still alive, then delete the temp directory. On failure Playwright keeps traces and screenshots in `e2e/test-results/`; CI uploads them together with `playwright-report/`.

## 6. Writing a scratch script

Everything a verification writes or observes goes in `e2e/scratch/<scenario>/`. [`../docs/verification.md`](../docs/verification.md) decides the form (no script, a launcher, or a full script) and owns verdicts and evidence. Run scripts with `pnpm -C e2e scratch`.

The main config `playwright.config.ts` excludes scratch with `testIgnore: ["**/scratch/**"]`; `playwright.scratch.config.ts` targets only `./scratch`, drops the global setup, and reads the target address from `OPSNAP_BASE_URL`. This mechanical split keeps CI from collecting local scripts.

### Real environment

Copy [`.env.example`](.env.example) to `e2e/.env` (gitignored). Only the scratch config reads it; smoke tests and the application do not, and environment variables override the file. Get authorization before touching a real environment, use isolated test data, and clean up afterwards. Deployment of the test services is described in [`../docs/verification.md`](../docs/verification.md#test-environment).

If `.env` does not configure a service, ask instead of arranging one: starting a dependency yourself or swapping in a mock produces a verdict about an environment nobody chose. Name the service and the missing variables, then ask the user.

## 7. Investigating failures

```bash
pnpm -C e2e exec playwright show-report
pnpm -C e2e exec playwright show-trace e2e/test-results/<test>/trace.zip
```

Output from the process under test is printed directly in Playwright's terminal.

## Related

[`../docs/verification.md`](../docs/verification.md) · [`../docs/testing.md`](../docs/testing.md) · [`../AGENTS.md`](../AGENTS.md)

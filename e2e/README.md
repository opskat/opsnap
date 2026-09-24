# E2E

## 1. Tracks

| | Smoke | Local runtime verification |
|---|---|---|
| Path | `e2e/tests/` (committed) | `e2e/scratch/` (gitignored) |
| Command | `make e2e` | `pnpm -C e2e scratch` |
| Scope | stable core regression flows | one-off check of a change or bug |
| External systems | none | docker.lan test services, with authorization |
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
| port | dedicated port 18291 (`e2e/ports.ts`), away from the dev instance's 8210; setup fails if it is already in use |
| OIDC provider | `bin/fakeidp` (`tools/fakeidp`, built by `make e2e`) on port 18292 (`e2e/ports.ts`); `POST /control/next` sets how the next authorization behaves |
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
- `smoke.spec.ts`: health check, 404 for unknown API paths, the home page shows the same version as the API, client routes load directly and survive reload, dark theme persists across reload, switching between Chinese and English.
- `logout.spec.ts` (serial, runs last): changing the password in Settings rejects a wrong current password, then signs out another session while this browser stays signed in; signing out returns to the login page and the old session gets 401; a wrong password shows an error and the right one returns to the page originally requested; five failures lock the client IP out (429), so nothing may sign in after it.

## 4. Protocol mocks

Smoke tests depend on no external system, so there are no mocks. When one is needed it goes in `e2e/fixtures/` as a dependency-free process with a readiness check, its port passed through an environment variable, implementing only the protocol responses the tests use.

## 5. Orchestration and cleanup

`e2e/global-setup.ts` writes a temporary config, starts `bin/opsnap`, waits for readiness and returns the teardown: SIGTERM, SIGKILL after 5 seconds if the process is still alive, then delete the temp directory. On failure Playwright keeps traces and screenshots in `e2e/test-results/`; CI uploads them together with `playwright-report/`.

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

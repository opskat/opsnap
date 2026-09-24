# Development standards

## Commands

Every command starts from the root `Makefile`; docs and CI use the same commands.

```bash
make install        # install frontend and e2e dependencies plus the Playwright browser
make dev-server     # run the backend on 127.0.0.1:8210 (copies configs/config.example.yaml to config.yaml on first run)
make dev-web        # run the Vite dev server on localhost:5173, proxying /api to 127.0.0.1:8210
make build          # build the frontend and embed it into bin/opsnap
make generate       # go generate ./... (regenerate mocks)
make lint           # golangci-lint + ESLint + Prettier + i18n key check + e2e typecheck
make lint-fix       # apply automatic formatting and lint fixes
make test           # Go tests + frontend Vitest (guard tests included)
make test-cover     # Go coverage summary
make e2e            # build, then run the Playwright smoke suite
make verify         # lint + test + e2e: the full pre-PR check
```

Targeted runs:

```bash
go test -run TestSystemHealth ./internal/controller/system_ctr/
pnpm -C frontend exec vitest run src/pages/OverviewPage.test.tsx
pnpm -C e2e exec playwright test -g "主题"   # requires make build first
```

Package managers: pnpm 10 for `frontend/` and `e2e/`, pinned by each `package.json` `packageManager` field. Lockfiles are committed and CI installs with `--frozen-lockfile`. Do not use npm or yarn.

## Structure and style

```text
cmd/opsnap/            entry point: config, repository registration, cago components
configs/               config.example.yaml (committed); config.yaml (local, gitignored)
internal/
  api/                 request/response types (mux.Meta) and router.go
  controller/          controllers that only forward, <name>_ctr/
  service/             business logic: interface + singleton getter, <name>_svc/
  repository/          data access: interface + Register/getter, <name>_repo/, mocks in mock/
  middleware/          global Gin middleware (request language)
  pkg/                 shared helpers: code/ (error codes + zh/en text), secret/ (master key, AES-GCM), testdb/ (SQLite for tests)
  web/                 embedded frontend (dist/ is build output; only .gitkeep is tracked)
  archtest/            layering guard tests
migrations/            metadata database migrations (append-only)
frontend/              React app (the @ alias points to src/)
  eslint-rules/        project ESLint plugin (design-system rules)
  scripts/             check-i18n.mjs
e2e/                   Playwright smoke tests and scratch verification
deploy/test/           docker-compose.yaml for the docker.lan test services
scripts/               repository scripts (test-env.sh)
docs/                  contributor docs and specs
```

- Path alias: `@/*` → `frontend/src/*` (kept in sync in `tsconfig.json` and `vite.config.ts`).
- Go: `gofmt` + `goimports`; project imports form their own group after third-party imports (`local-prefixes` in `.golangci.yml`). Code comments and commit messages are in Chinese.
- Frontend: Prettier (print width 120, double quotes, `trailingComma: es5`; see `frontend/.prettierrc`). Markdown is excluded from Prettier. Components use PascalCase file names, utility modules camelCase.
- Tests: Go tests sit next to the code as `*_test.go`; frontend tests sit next to the code as `*.test.ts(x)`; guard tests live in `frontend/src/__tests__/`.

## Enforced rules

| Rule | Correct form | Gate and exemption |
|---|---|---|
| Every non-public endpoint requires sign-in; account endpoints reject API tokens | bind business endpoints in the `authed` group and account endpoints (password, tokens, sign-in methods) in `account` in `internal/api/router.go`; public or session-only endpoints must be listed in the spec first | `internal/api/router_test.go` (sentinel middlewares); the public and session-only lists live in that test |
| Controllers do not import repositories | call the matching service | `internal/archtest`; exempt: `*_test.go` (tests register mock repositories) |
| Services and repositories do not import upper layers | controller → service → repository | `internal/archtest` |
| `internal/api` holds only request/response types | register routes in `internal/api/router.go` | `internal/archtest`; exempt: `internal/api/router.go` |
| No standard `log` under `internal/` | cago `logger.Ctx(ctx)` | `internal/archtest` (`log/slog` unaffected) |
| No `fmt.Print*` | cago `logger.Ctx(ctx)` | golangci-lint `forbidigo`; exempt: `cmd/` |
| Security checks without global exclusions | fix the finding, or `//nolint:gosec // <reason>` at the call site | golangci-lint `gosec` + `nolintlint` (explanation and specific linter required) |
| Wrapped errors stay inspectable | `fmt.Errorf("...: %w", err)`, `errors.Is/As` | golangci-lint `errorlint` |
| Outgoing requests carry a context | `http.NewRequestWithContext`, `httptest.NewRequestWithContext` | golangci-lint `noctx` |
| No palette or raw colours | semantic tokens ([`design.md`](design.md#theme-and-tokens)) | ESLint `opsnap/no-raw-color` on `frontend/src/**`; exempt: test files |
| No arbitrary font sizes | type scale ([`design.md`](design.md#typography)) | ESLint `opsnap/no-arbitrary-font-size`; exempt: test files |
| No `dark:` variants in app code | put theme differences in token values | ESLint `opsnap/no-dark-variant`; exempt: `src/components/ui/**` (shadcn output), test files |
| No Chinese literals in JSX | `t("key")` with entries in `frontend/src/i18n/locales/*.json` | ESLint `i18next/no-literal-string` (text and visible attributes containing Han characters); exempt: test files |
| Frontend error codes match the backend | copy the number from `internal/pkg/code` into `ErrorCode` in `frontend/src/lib/auth.ts` and add the name to the test's map | `internal/pkg/code/code_test.go` `TestFrontendErrorCodesInSync` |
| Locale files agree; literal `t("a.b")` keys exist | edit `zh-CN.json` and `en.json` together | `frontend/scripts/check-i18n.mjs` (part of `pnpm lint`) |
| No direct `fetch` | `request()` in `frontend/src/lib/api.ts` | ESLint `no-restricted-globals`; exempt: `src/lib/api.ts`, test files |
| React 19 APIs | `ref` as a prop, `<Context value>`, `use(Context)` | ESLint `react-x/no-forward-ref`, `no-context-provider`, `no-use-context` |

Guard tests run each rule through the real configuration and assert that violations are reported while compliant and exempt code is not:

- Go layering: `internal/archtest/archtest_test.go`
- Authenticated routes: `internal/api/router_test.go`
- ESLint: `frontend/src/__tests__/eslint-harness.test.ts`
- i18n key check: `frontend/scripts/check-i18n.test.mjs`

These rules took effect on 2026-09-23 with no existing exemptions.

**Adding shadcn components**: after `pnpm dlx shadcn@latest add <component>`, check that the generated file imports `cn` from `@/lib/utils` and that no unrelated `cn` package was added to `package.json`; the CLI got both wrong when this project was set up. Generated files must also pass `opsnap/no-raw-color` (replace colours such as `text-white` or `bg-black/50` with tokens).

## Internationalization

- UI copy lives in `frontend/src/i18n/locales/zh-CN.json` and `en.json`; both files must contain the same keys.
- Components use `t("group.key")` from `useTranslation()`. Do not use `t(key, { defaultValue })`; a missing key must be caught by the check script.
- Switch languages with `changeLanguage()` in `frontend/src/i18n/index.ts`; it also writes `localStorage` (`opsnap-lang`) and `<html lang>`.

## API requests

The frontend calls the backend only through `request<T>(path)`. It prefixes `/api/v1`, sends `Accept-Language`, and unwraps cago's `{ code, msg, data }` envelope. It throws `ApiError` (with `code` and `status`) when `code !== 0`, the HTTP status fails, or the body is not JSON. Wrap each domain's endpoints in `frontend/src/lib/`, for example `getHealth()` in `system.ts`.

## Logging

The backend logs through cago `logger.Ctx(ctx)` and attaches errors with `zap.Error(err)`, as in `internal/service/system_svc/system.go`. Logs must never contain credentials, keys or data source passwords.

## Commits and PRs

- Never commit to `main`; branch from `main`, push, and open a PR.
- Commit messages are in Chinese, formatted `<type>: <description>` with types such as `feat`, `fix`, `docs`, `test`, `refactor`, `chore`.
- Commits and PR descriptions carry no `Co-Authored-By` or other AI attribution trailers.
- Run `make verify` before opening a PR. There is no pre-commit hook.

A PR description states what changed and why, the commands run and their results, runtime evidence or screenshots for UI changes, and the blast radius and rollback plan for metadata schema changes.

## CI

`.github/workflows/ci.yml` runs on pull requests and on pushes to `main`, using the same commands as local development:

- `go`: golangci-lint v2.12.2 + `go test ./...`
- `frontend`: `pnpm lint` and `pnpm test` in `frontend/`, `pnpm lint` in `e2e/`
- `e2e`: `make e2e`

**These checks do not block merging yet.** A repository admin has to enable branch protection on `main` and mark the three jobs as required. Until then they are advisory.

## Related

[`../AGENTS.md`](../AGENTS.md) · [`architecture.md`](architecture.md) · [`testing.md`](testing.md) · [`verification.md`](verification.md)

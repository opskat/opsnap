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
make docker-build   # build the Docker image (deploy/docker/Dockerfile) for this machine's architecture, tag opsnap:local (IMAGE=...)
make docker-smoke   # start the built image, wait for its health check, run every tool under /opt/opsnap/tools with --version
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
deploy/docker/         Dockerfile, built-in config.yaml, install-mysqldump.sh and pinned MySQL / PGDG signing keys for the image
deploy/test/           docker-compose.yaml for the docker.internal test services
scripts/               repository scripts (test-env.sh, docker-smoke.sh)
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
- `docker` (pull requests only): builds the image for amd64 on `ubuntu-latest` and arm64 on `ubuntu-24.04-arm` (native runners, no QEMU; free only while the repository is public), without pushing, and runs `scripts/docker-smoke.sh` on each. `VERSION` is `pr-<number>`, `COMMIT` the PR head's short hash.

**These checks do not block merging yet.** A repository admin has to enable branch protection on `main` and mark the jobs as required. Until then they are advisory.

The Docker image takes two build arguments, `VERSION` and `COMMIT`; `make build-server` writes them into the binary through `LDFLAGS` (`configs.Version` and `system_svc.Commit`), and `/api/v1/system/health` returns both. Outside Docker they default to `git describe` and `git rev-parse --short HEAD`.

## Nightly image

`.github/workflows/nightly.yml` and the reusable `.github/workflows/docker-publish.yml` build and publish a daily image, separately from the PR-only `docker` job above (spec [`2026-09-29-overview-docker.md`](specs/2026-09-29-overview-docker.md), "Docker 镜像" → "nightly 构建"):

- Every day at 18:00 UTC (02:00 Beijing time), it merges `main` into the `nightly` branch. A merge conflict stops the workflow with an error and leaves `nightly` untouched.
- Whether a scheduled or manual run builds is decided by the `nightly-published` git tag, not by whether `main` had new commits: after a successful publish the workflow moves that tag to the commit it just published, and a scheduled/manual run skips only when the `nightly` head equals the tag (a missing tag counts as "not published"). So a commit whose run was cancelled (the `nightly` concurrency group cancels in-progress runs) or whose tests/publish failed is built again by the next run; a commit that keeps failing is therefore retried every night. Deleting the tag (`git push origin :refs/tags/nightly-published`) forces the next scheduled run to rebuild and republish the current head. The Makefile's `git describe` excludes this tag, so local builds' version strings are unaffected by it.
- Pushing to `nightly` directly also builds, skipping the merge step (it is only meaningful for the scheduled/manual triggers). Running the workflow manually (`workflow_dispatch`) builds too, subject to the same "skip when already published" rule as the schedule.
- Each run pins one commit: `prepare` records the full SHA of the `nightly` head (after the merge), and the tests, the image build, the version date and the commit label all use that SHA, so re-running a failed job later still builds the same commit. Right before the manifest list is tagged, `docker-publish.yml` (input `expect-branch: nightly`) checks the remote `nightly` head; if it no longer equals the pinned SHA, the run pushes no tags, leaves `nightly-published` alone, ends green, and writes "已被新提交取代" with both SHAs to the run summary. The job that moves `nightly-published` repeats the same check, so re-running it after `nightly` has moved on never moves the tag back to an older commit. The newer commit is published by its own run or the next scheduled one, and `:nightly` never moves back to an older commit.
- Before publishing, it runs the same Go and frontend checks as the `go` and `frontend` jobs in `ci.yml` against the pinned `nightly` commit; the tests run on every trigger that builds (including a direct push to `nightly`, where the merge job is skipped), and the publish only runs if both succeed (a failure, cancellation or skip blocks it).
- `amd64` and `arm64` are built natively (same runners as the PR `docker` job), each pushed to `ghcr.io/opskat/opsnap` by digest and smoke-tested (`scripts/docker-smoke.sh`) right after that push — pulling the just-pushed digest back down, since `docker buildx`'s push-by-digest output cannot also load the image into the local daemon in the same build. A failed smoke test fails that job, so the digest is never merged into a tagged manifest. The two digests are then merged into one multi-arch manifest list tagged `nightly` and `nightly-<UTC date>`. There is no `latest` tag and no GitHub Release.
- The image version shown in the app is `nightly-<UTC date>`; the commit is the short SHA of the merged `nightly` commit that was built.

One-time prerequisite: the `nightly` branch does not exist until someone creates it with `git push origin main:nightly`. That push, made with a real account rather than the workflow's own token, fires the `push` trigger above right away, which doubles as the first manual confirmation that the pipeline works end to end.

GitHub disables scheduled workflows after 60 days without repository activity; a stopped nightly build does not raise any error, it silently stops updating the tags. Check the Actions tab for "Nightly" shown as disabled, and re-enable it there if needed.

To trigger a build manually: open the "Nightly" workflow under the repository's Actions tab and use "Run workflow" (`workflow_dispatch`), or `gh workflow run nightly.yml`.

## Related

[`../AGENTS.md`](../AGENTS.md) · [`architecture.md`](architecture.md) · [`testing.md`](testing.md) · [`verification.md`](verification.md)

# AGENTS.md

`CLAUDE.md` imports this file; rules for AI agents and contributors live here only.

## Read before changing

| When | Read | It owns |
|---|---|---|
| Before writing code | [`docs/develop.md`](docs/develop.md) | commands, layout, style, enforced rules, commits, CI |
| Before writing tests | [`docs/testing.md`](docs/testing.md) | test boundaries, coverage, mocks and fixtures, commands |
| Before runtime verification | [`docs/verification.md`](docs/verification.md) | docker.local test environment, scratch workflow, reports |
| Before UI work | [`docs/design.md`](docs/design.md) | tokens, type scale, components, themes, states, accessibility |
| Before changing layers or adding a module | [`docs/architecture.md`](docs/architecture.md) | layering, subsystems, extension recipes, migrations |
| Before editing docs | [`docs/documentation.md`](docs/documentation.md) | ownership and fact checks |
| Before implementing a feature | [`docs/specs/`](docs/specs/) | agreed requirements for the current round |

Index: [`docs/README.md`](docs/README.md).

## Project overview

OpsNap — a self-hosted web console for backing up and syncing servers, shipped as one Go binary with the frontend embedded.

- Backend: Go 1.26, [cago](https://github.com/cago-frame/cago) (Gin + GORM), SQLite metadata database
- Frontend: React 19, TypeScript, Vite 6, Tailwind CSS v4, shadcn/ui (new-york), i18next (zh-CN, en)
- Package managers: Go modules; pnpm 10 for `frontend/` and `e2e/` (pinned by `packageManager`, CI installs with `--frozen-lockfile`)
- Module: `github.com/opskat/opsnap`
- Output: `bin/opsnap` (`make build`)
- Requirements: [`docs/specs/2026-09-23-opsnap-v1.md`](docs/specs/2026-09-23-opsnap-v1.md)

## Language conventions

- Documentation (`AGENTS.md`, `docs/`, READMEs): English. Verification reports under `e2e/scratch/<scenario>/` may be written in Chinese.
- Code comments, lint and test diagnostics, commit messages: Chinese (cago convention).
- UI copy: never hard-coded; every visible string goes through i18n with both `zh-CN` and `en` entries.

## Hard rules

These come from the spec's invariants and repository policy. Breaking one is a blocker, not a trade-off.

1. **A snapshot reported as successful must be restorable.** Never mark a run successful while any step is unconfirmed, including incremental log continuity; a gap is reported as "incremental interrupted". review-only
2. **Storage credentials and repository keys never leave the controller.** Never send them to executors or backed-up hosts, never log them, never persist them unencrypted. gosec runs without global exclusions (golangci-lint, CI `go` job); the rest is review-only
3. **Never commit to `main`.** Branch from `main` and open a PR. review-only until branch protection is enabled (see [`docs/develop.md`](docs/develop.md#ci))
4. **The spec owns requirements.** Do not implement behaviour the spec leaves undecided; raise it and update the spec first. Never edit a spec to match an implementation. review-only
5. **On docker.local, touch only the `opsnap-test` compose project.** Other projects' containers run on the same host. review-only

## Engineering principles

- **Test first for observable behaviour.** Write the API definition and a failing test, then implement; boundaries and exceptions are in [`docs/testing.md`](docs/testing.md). review-only
- **Reproduce a bug before fixing it.** A failing test and a root cause come first; otherwise stop and report. review-only
- **Dependencies flow controller → service → repository.** Controllers never import repositories, lower layers never import upper ones, `internal/api` holds only request/response types (routing lives in `internal/api/router.go`). enforced by `internal/archtest` (CI `go` job)
- **Resolve dependencies through getters.** Services call `xxx_repo.Xxx()`; implementations are registered once in `cmd/opsnap/main.go`. Every repository interface carries a `//go:generate mockgen` directive. review-only
- **Validate at the API boundary, propagate internal errors.** Request structs use `binding` tags or `Validate(ctx)`; wrap with `%w` so callers can `errors.Is`. `errorlint` enforced by golangci-lint
- **Log through `logger.Ctx(ctx)`.** No `fmt.Print*` and no standard `log` under `internal/`. enforced by golangci-lint `forbidigo` and `internal/archtest`
- **A suppression needs a reason.** `//nolint:<linter> // <reason>` only; bare or unexplained directives fail `nolintlint`. enforced by golangci-lint
- **Migrations are append-only and use deterministic SQL**, never `AutoMigrate(&entity)`. review-only
- **Keep the architecture the spec fixed.** kopia is the storage layer; MySQL/PostgreSQL full backups use the official `mysqldump` / `pg_dump`; there is no resident agent. Changing any of these requires a spec revision first. review-only
- **Frontend calls the backend only through `request()`** in `frontend/src/lib/api.ts`. enforced by ESLint `no-restricted-globals`
- **UI uses design tokens and the type scale only**: no palette or raw colours, no arbitrary font sizes, no `dark:` variants in app code. enforced by ESLint `opsnap/*` rules
- **UI copy goes through i18n**; JSX must not contain Chinese literals, and locale files must stay in sync. enforced by ESLint `i18next/no-literal-string` and `frontend/scripts/check-i18n.mjs`
- **Reuse before adding.** Check `frontend/src/components/ui/`, `frontend/src/components/layout/`, lucide icons and `cn()` first. A new dependency needs a stated reason and is added with pnpm. review-only
- **Never hand-edit generated output**: `internal/repository/*/mock/` and `internal/web/dist/`. review-only
- **Change only what was requested.** Report unrelated problems instead of fixing them in passing. review-only

## Definition of done

- `make verify` passes (lint, unit and guard tests, build, smoke e2e)
- New or changed behaviour has tests; a bug fix has a regression test that fails without the fix
- UI changes are checked in light and dark themes and in both languages
- Docs are updated whenever commands, paths, rules or architecture change ([`docs/documentation.md`](docs/documentation.md))

## Architecture

> Details: [`docs/architecture.md`](docs/architecture.md).

```text
browser ──> Gin (cago mux)
              ├─ /api/v1/*   ──> controller ──> service ──> repository ──> SQLite (GORM)
              └─ other GETs  ──> internal/web (embedded frontend build, SPA fallback to index.html)
```

- `cmd/opsnap` — entry point: load config, register repositories, start cago components in order
- `internal/api` — request/response types (`mux.Meta` declares the route) and `router.go`
- `internal/controller`, `internal/service`, `internal/repository` — the three layers
- `internal/web` — embeds and serves the frontend
- `migrations` — metadata database migrations
- `frontend` — React app; builds into `internal/web/dist`

### Key constraints

- Routes are registered only in `internal/api/router.go`; unknown paths under `/api/` return 404 and never fall back to the SPA
- A new repository is registered in `cmd/opsnap/main.go` and its mock regenerated with `make generate`

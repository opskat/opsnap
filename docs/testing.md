# Testing

## Applicability gate

Start from the changed contract. Add cases only for behaviour that applies: thresholds and edges, invalid input and failures, state and lifecycle, ordering and concurrency, compatibility and security, process or external boundaries, or mechanical source conventions. If none apply, one representative happy path is enough.

Before writing a test, state:

1. the observable contract;
2. the triggering input, state or sequence;
3. the observable outcome;
4. a plausible wrong implementation the test rejects.

## Choosing a test boundary

Pick the narrowest boundary that observes the real contract:

| Contract | Boundary | Form in this project |
|---|---|---|
| parsing, mapping, validation, state logic | unit | Go package tests; frontend `*.test.ts` |
| endpoint input, output and errors | controller | `muxtest.NewTestMux()` + mock repositories (see `internal/controller/system_ctr/system_test.go`) |
| rendered state, interaction, accessibility | component | Vitest + Testing Library (see `frontend/src/pages/OverviewPage.test.tsx`) |
| real process, embedded frontend, build output | e2e | Playwright smoke (`e2e/tests/`) |
| driver behaviour against a real MySQL / PostgreSQL (connection, TLS, probes) | Go package test against docker.lan | `testenv.MySQL(t)` / `testenv.Postgres(t)` (`internal/pkg/testenv`, see below) |
| end-to-end flows against real databases, storage or servers | runtime verification | [`verification.md`](verification.md) |

## Covering the behaviour space deliberately

Start with one happy path, then cover each distinct branch that applies:

- boundary values and empty, omitted or duplicate input;
- dependency errors, denial, timeouts, cancellation, and unchanged state after failure;
- repeated calls, stale async results and cleanup;
- out-of-order or overlapping operations, only when the contract promises something about them.

Do not multiply ordinary samples down the same branch. A bug regression test stays close enough to the real failure that restoring the cause turns it red.

## Assertions, mocks and fixtures

- Assert returned, rendered, persisted or emitted behaviour. Assert a collaborator call only when that call is the contract.
- Go: repositories are mocked with `go.uber.org/mock` (generated into `internal/repository/*/mock/`) and registered with `RegisterXxx` inside `setupXxxTest`. Structure scenarios with GoConvey (`convey.Convey` nesting) and assert with testify. For service and repository tests against real SQLite, call `testdb.New(t)` (`internal/pkg/testdb`): it creates a temporary database, runs every migration and sets it as cago's default database (see `internal/service/secret_svc/secret_test.go`). OIDC tests use the in-process fake provider `internal/pkg/fakeidp` (`httptest` server; `SetNext` injects cancellation, wrong audience or nonce, expired tokens).
- Go tests that need a docker.lan service (the `opsnap-test` MySQL 8.0 and PostgreSQL 16) get its address and credentials from `testenv.MySQL(t)` / `testenv.Postgres(t)`. `internal/pkg/testenv` reads `e2e/.env` (environment variables override it; variables in [`../e2e/.env.example`](../e2e/.env.example)) and skips the test, stating which variables are missing, when the service is not configured. Such a skip is not a failure: CI has no databases, so only the in-process parts run there (SSH and SOCKS5 through `internal/pkg/fakessh`). These tests are read-only against the shared instances. Example: `TEST_ENV_HOST=192.168.8.141 go test ./internal/pkg/dsconn/` with the ports and password in `e2e/.env`.
- Frontend: stub HTTP with `vi.stubGlobal("fetch", ...)`, which works because every request goes through `request()`. Assert what the page shows, not what the mock returned.
- Keep fixtures minimal and able to tell right from wrong.

## Exceptions to TDD

Only these:

- genuinely behaviour-preserving refactors, type changes, deletions, renames or dependency upgrades, verified proportionately;
- automation that is genuinely infeasible, verified manually with retained evidence.

A file type or task label never grants an exception.

## Tests not to write

No tautologies, duplicate coverage, tests that only render props back, tests of mocks or of the framework, misleadingly named tests, or source-text assertions that belong in a lint rule.

Keep thin tests that uniquely cover a branch, mapping, accessibility derivation, completeness invariant or historical regression.

## Scope and cleanup

Clean up worthless tests only when they cover the changed code or are replaced by a guardrail landing in the same change; otherwise report them. Before deleting, read the production path and confirm the same contract is covered elsewhere.

Classify a red or slow test before changing it: production regression, stale contract, flaky shared state or timing, real I/O in a unit test, or genuinely redundant. Do not hide flakiness with retries or longer timeouts.

## Running tests

```bash
go test -run TestSystemHealth ./internal/controller/system_ctr/   # one Go test
pnpm -C frontend exec vitest run src/lib/api.test.ts             # one frontend test file
make test                                                        # all unit and guard tests
make test-cover                                                  # Go coverage
make e2e                                                         # smoke e2e
make lint                                                        # static checks and typechecks
```

- Go: GoConvey + testify + go.uber.org/mock, plus cago's `muxtest` and `testutils`.
- Frontend: Vitest (happy-dom, `globals: true`) + Testing Library + jest-dom matchers, configured in the `test` block of `frontend/vite.config.ts`.
- e2e: Playwright; see [`../e2e/README.md`](../e2e/README.md). Process-level fakes needed only in e2e (not by any Go test) are built as standalone binaries in `tools/` and started by `e2e/global-setup.ts`: `tools/fakeidp` for OIDC, `tools/fakessh` (wraps `internal/pkg/fakessh`) for SSH network channels and server-file data sources.

## Related

[`../AGENTS.md`](../AGENTS.md) · [`verification.md`](verification.md) · [`../e2e/README.md`](../e2e/README.md)

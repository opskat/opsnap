# Runtime verification

## When to use this guide

When committed tests fully observe the changed logic, run those tests and stop. Use this guide when confirming a change needs the real UI, process, database, storage or servers, or when reproducing a runtime-only bug. It does not replace TDD.

## Test environment

Test services run on docker.local (opsctl asset `local-docker`, 192.168.8.141) as the compose project `opsnap-test`, defined in [`../deploy/test/docker-compose.yaml`](../deploy/test/docker-compose.yaml).

| Service | Address from a dev machine | Notes |
|---|---|---|
| MySQL 8.0 | `192.168.8.141:13306`, user `root` | binlog on, ROW format, GTID on |
| PostgreSQL 16 | `192.168.8.141:15432`, user `postgres` | `wal_level=logical`, 10 replication slots |
| MinIO (S3) | API `192.168.8.141:19000`, console `:19001`, user `opsnap` | pinned to `RELEASE.2025-04-22T22-12-26Z` |
| Keycloak 26.4 (OIDC) | `http://192.168.8.141:18080`, realm `opsnap`, issuer `http://192.168.8.141:18080/realms/opsnap` | client `opsnap` (secret = test password), users `ops` and `other`; allowed callbacks are OpsNap on `127.0.0.1`/`localhost` ports 8210 and 18293 (`deploy/test/keycloak/opsnap-realm.json`); admin user `admin` |
| SSH jump host (network channel) | `192.168.8.141:12222`, user `opsnap` | `linuxserver/openssh-server`; password sign-in only, no sudo |
| SSH server-file target | not published; reachable only from `ssh-jump` (same compose network, hostname `ssh-target:2222`) | same image and user; exercises "SOCKS5 → SSH → target host" |
| SOCKS5 proxy | `192.168.8.141:11081`, user `opsnap` | `serjs/go-socks5-proxy`, username/password auth |

All services share one test password, stored as `OPSNAP_TEST_PASSWORD` in the local, gitignored `e2e/.env`; `make test-env-up` generates it on first run.

```bash
make test-env-up              # deploy or update (copies docker-compose.yaml, the Keycloak realm file and .env to /opt/opsnap-test via opsctl, waits for health checks)
make test-env-status          # container status
make test-env-down            # stop containers, keep data
scripts/test-env.sh destroy   # remove containers, volumes and the remote directory
```

Constraints:

- **Other projects' containers run on the same host**, including a MySQL, PostgreSQL and Redis on 3306, 5432 and 6379. Only ever operate on the `opsnap-test` project.
- MinIO has no buckets for OpsNap by default, and OpsNap never creates one. Create a bucket per verification run in the MinIO console (or with `mc mb`) and use a fresh prefix, so runs do not see each other's repositories.
- Storage requirements are confirmed with the official kopia CLI at the same version OpsNap embeds (0.23.1, `brew install kopia` on the dev machine): a repository OpsNap created must open with only the key (`kopia repository connect filesystem|s3 ...`), using a separate `--config-file` so the CLI does not touch any other kopia setup.
- The host has about 7.8 GiB of memory. Start services as needed; do not keep every engine version running.
- Images are pulled through the `katch.ggnb.top/` mirror (`<mirror>/docker.io/...`, `<mirror>/quay.io/...`). On another host set `OPSNAP_TEST_REGISTRY_MIRROR`; an empty value pulls directly.
- MongoDB, Redis, Kafka and the LVM SSH target (privileged container with a loop device) are not deployed yet; they are added to the compose file in the rounds that need them.
- `mysql80` runs with `--skip-name-resolve`: without it the first packet to a fresh connection sometimes takes 20-40s, most likely reverse DNS on the client address (seen while verifying task 3 of the datasources round, not fully root-caused).
- The real chain to verify for the datasources round: `SOCKS5 (192.168.8.141:11081) → ssh-jump (12222) → ssh-target` for server-file, and `SOCKS5 → mysql80` / `SOCKS5 → pg16` directly (mysql80/pg16 have no SSH in front of them). `socks5` publishes 11081 rather than 11080 because 11080 is already taken on docker.lan by the host's own `sockd`.
- To rotate `ssh-jump`'s host key for the key-changed scenario: its `/config` is an anonymous volume, so simply recreating the container keeps the same key; instead remove the whole `/config/ssh_host_keys` directory and restart the container, which regenerates a new key pair.

## Workflow

1. Run `make lint` and the relevant tests; run the full `make verify` when the risk or a gate requires it.
2. Build and start the target: `make build`, then `bin/opsnap -c <config>`, or a dev instance with `make dev-server`. Start only the target; reach real dependencies through `e2e/.env`. If `.env` lacks a service, name the service and the missing variables and ask the user — do not start a substitute or switch to a mock.
3. Choose the cheapest form that observes the contract, and put everything it produces under the gitignored `e2e/scratch/<scenario>/`:

   | To reach and observe the target | You write |
   |---|---|
   | an existing command or entry point suffices and neither depends on nor changes local state | nothing — drive it and read the independent evidence |
   | it needs a specific launch, isolated state or real-environment config, observed once | a launcher that stops at the target; drive it by hand |
   | the sequence must be replayed, or timing/concurrency is the contract | a full scratch script (`pnpm -C e2e scratch`) |

   Reuse the isolation and independent evidence from [`../e2e/README.md`](../e2e/README.md), not its fixtures. Every form includes at least one observation from a path the driven surface does not share — metadata database rows, structured logs, a read-only endpoint such as `/api/v1/system/health`, or an output file — copied into the scenario directory while the run that produced it is still alive.
4. Before running, copy [`references/verification-report-template.md`](references/verification-report-template.md) into the scenario directory as `report.md` and fill it in as evidence arrives. Reports may be written in Chinese.
5. Record how the target was driven, exit codes, the runtime observations that decide the verdict, what was not covered, and the shortest steps for the user to reproduce it.

```bash
pnpm -C e2e scratch                   # run every script under e2e/scratch/
pnpm -C e2e scratch -g "<scenario>"   # run one scenario
```

For acceptance against a spec, the scenario name is the spec slug and every requirement becomes one verdict row. Verdicts are only "holds", "does not hold" or "not observed" (in a Chinese report: 成立 / 不成立 / 未观察到).

For a bug reproduction, state whether the script asserts the expected behaviour (red until fixed) or the current buggy behaviour (green until fixed), then turn it into a committed failing test unless the manual-evidence exception in [`testing.md`](testing.md#exceptions-to-tdd) applies.

Never weaken an assertion, skip a failed step, or describe red as green. Prove background or runtime effects with a specific log line, metric or data change; "no errors" is not evidence. Get authorization before destructive or external side effects and before substituting a mock for a real dependency; the verdict row then names what stood in and what it does not cover.

## Maintaining this route

Confirm the documented commands exist, `e2e/playwright.config.ts` ignores `scratch/`, `e2e/playwright.scratch.config.ts` targets only it, and `.gitignore` covers `e2e/scratch/` and `e2e/.env`. After path or harness changes, follow [`documentation.md`](documentation.md).

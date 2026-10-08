# OpsNap

A self-hosted web console for server backup and sync: back up MySQL, PostgreSQL, MongoDB, Redis, Kafka and server files on a schedule to a local directory or S3-compatible storage, and sync between instances of the same kind. Ships as a single Go binary with the frontend embedded.

> Early development. The v1 requirements are in [`docs/specs/2026-09-23-opsnap-v1.md`](docs/specs/2026-09-23-opsnap-v1.md).

## Quick start

Requires Go 1.26, Node.js 22 and pnpm 10.

```bash
make install      # frontend and e2e dependencies
make build        # build bin/opsnap with the frontend embedded
cp configs/config.example.yaml configs/config.yaml
bin/opsnap        # open http://127.0.0.1:8210
```

For development, run `make dev-server` and `make dev-web` side by side.

## Docker

The image `ghcr.io/opskat/opsnap` is built for `linux/amd64` and `linux/arm64` on Debian stable slim. Besides the binary it carries the export tools, laid out by version under `/opt/opsnap/tools` so OpsNap can pick the one matching each server:

| Directory | Tools | Source |
|---|---|---|
| `mysql-8.0`, `mysql-8.4`, `mysql-9.7` | `mysqldump` | MySQL official binaries (signatures checked at build time) |
| `postgresql-14` … `postgresql-18` | `pg_dump`, `pg_dumpall` | PostgreSQL apt repository (PGDG) |
| `mariadb` | `mariadb-dump` | Debian's MariaDB client |

The container listens on `0.0.0.0:8210`, keeps everything it writes (metadata database, `master.key`, logs, kopia cache) in `/data`, runs as root and reports its health from `/api/v1/system/health`. The image holds no keys or credentials; they are created in `/data` on first start.

```bash
docker run -d --name opsnap --restart unless-stopped \
  -p 8210:8210 -v opsnap-data:/data \
  ghcr.io/opskat/opsnap:nightly
docker logs opsnap   # the setup code for first-time setup at http://<host>:8210
```

With docker compose:

```yaml
services:
  opsnap:
    image: ghcr.io/opskat/opsnap:nightly
    restart: unless-stopped
    ports:
      - "8210:8210"
    volumes:
      - opsnap-data:/data
      # - /srv/backups:/backups                              # a host directory for local-directory storage
      # - ./config.yaml:/opt/opsnap/configs/config.yaml:ro   # your own configuration
volumes:
  opsnap-data:
```

- **Back up `master.key` with the volume.** It sits next to the database in `/data`; without it the saved credentials and storage keys cannot be decrypted (see [Master key](#master-key)). Setting `OPSNAP_MASTER_KEY` (`-e OPSNAP_MASTER_KEY=...`) instead keeps the key out of the volume.
- **Local-directory storage needs a host directory mounted into the container**, for example `-v /srv/backups:/backups`, then a local-directory storage with the path `/backups`. Storage paths are paths inside the container; anything not on a mounted volume is lost with the container.
- **To use your own configuration**, start from [`deploy/docker/config.yaml`](deploy/docker/config.yaml) (the built-in default) and mount it over `/opt/opsnap/configs/config.yaml`, or mount it elsewhere and append `-c <path>` to the `docker run` command. Keep `db.dsn` and the log files under `/data` and `tools.dir` at `/opt/opsnap/tools`; the health check calls `127.0.0.1:8210`, so if you change the port also override it with `--health-cmd`.

To build and check the image from a checkout: `make docker-build` (tag `opsnap:local`, your machine's architecture), then `make docker-smoke`.

## OIDC sign-in

Under Settings → Sign-in methods, configure one OIDC provider (display name, issuer, client ID and secret, scopes) and register the callback URL shown there with the IdP. Then click Bind and sign in at the IdP: that identity becomes a second way to sign in as the administrator. After signing in with it once, you can turn off password sign-in; `opsnap admin reset-password` turns it back on if the IdP is unavailable.

## API tokens

Generate a token under Settings → API tokens and send it as `Authorization: Bearer <token>`. A token can call every business API but cannot manage tokens, change the password or change sign-in methods. It is shown only once; revoke it if lost.

## Storage

Under Storage, add a local directory on the OpsNap host or an S3-compatible bucket (OpsNap never creates buckets). Each storage is a standard, encrypted [kopia](https://kopia.io) repository (AES256-GCM-HMAC-SHA256):

- An empty location gets a new repository. Its key is generated for you (or you set your own password of at least 12 characters); copy it or download the key file and keep it offline.
- A location that already holds a kopia repository can only be unlocked with its key. A non-empty location that is not a kopia repository is refused, so existing data is never overwritten.
- OpsNap keeps an encrypted copy of each key (under the master key) for scheduled jobs. You can view or download it again under Storage → ⋯ → View key, after re-entering your password (or re-verifying with OIDC when password sign-in is off). API tokens cannot read keys.
- Deleting a storage removes only OpsNap's record, its copy of the key and its local cache. The repository data stays where it is; adding the location again requires the key.

### Restoring without OpsNap

The repository key is the kopia repository password, so the official kopia CLI (0.23 or later) can open a repository with nothing but the key:

```bash
# local directory
kopia repository connect filesystem --path /var/backups/opsnap
# S3-compatible storage (add --disable-tls for plain HTTP, --region if needed)
kopia repository connect s3 --endpoint minio.lan:9000 --bucket opsnap-backup --prefix prod/ \
  --access-key <Access Key> --secret-access-key <Secret Key>

kopia snapshot list --all
kopia restore <snapshot-id> /restore/target
```

Enter the key when asked for the password. The key file downloaded from OpsNap contains the key (on the `Key:` line) and the connect command for that storage.

## Data sources

Under Sources, connect the MySQL, PostgreSQL and server-file databases and hosts you want to back up:

- **Network channels** reach a data source that isn't directly reachable: chain up to 5 SSH jump hosts and SOCKS5 proxies, each one reusable by several data sources. A channel's SSH host key is trusted on first connect (TOFU, like `ssh`) and re-checked on every use; if it changes, everything routed through that channel stops until you confirm the new key with your server administrator.
- Saving a data source tests the connection first (five TLS modes plus optional mTLS for MySQL/PostgreSQL; the same host-key confirmation for server files), so a mistake never overwrites a working configuration.
- Saving also starts a background capability probe (which backup methods this data source supports, with a copy-pasteable fix for anything missing or risky); reprobe any time from its detail page.
- A network channel still used by a data source can't be deleted.

## Backup jobs

Under Jobs, a five-step wizard creates a full backup of a MySQL or PostgreSQL data source to a storage: pick the data source, what to back up (the whole instance, re-listed on every run, or chosen databases; routines, triggers, events and accounts for MySQL, roles and tablespaces for PostgreSQL; tables to exclude), where it goes (storage, path prefix, compression), when it runs and how long snapshots are kept, then confirm and optionally run it once right away.

- Exports use the official tools on the OpsNap host — `mysqldump`, or `pg_dump` per database plus `pg_dumpall --globals-only` — found on `PATH`, then in `tools.dir`. They connect through the data source's network channel via a temporary port on `127.0.0.1`; passwords never appear on a command line. The output streams straight into a kopia snapshot, which is read back before the run counts as successful.
- Schedules: hourly, daily, weekly or Cron, in the job's time zone. A run missed while OpsNap was down is caught up once at startup; a scheduled time that arrives while the previous run is still going is skipped; failures are retried and runs time out as configured. At most 3 runs execute at once; the rest queue.
- Retention keeps everything from the last N days, then the last snapshot of each week and month for as long as configured; the latest successful snapshot is always kept. Each storage used by a job gets a full repository maintenance once a day.
- Every run keeps a record with its steps and log. The job list shows each job's last run and snapshot count; the detail page shows its configuration, statistics and run history, with the failed step and log of any run.
- After creation a job's data source, storage and path prefix cannot change. Deleting a job deletes its run history and, if you tick the box, its snapshots. A data source or storage used by a job cannot be deleted, and the storage's location cannot change.

Snapshots are ordinary kopia snapshots (source `opsnap@opsnap:/<prefix>`, tagged `job:<id>`), so they can be restored without OpsNap as shown above: `kopia snapshot list --all --tags job:<id>`, then `kopia restore`, and import the files with `pg_restore` or `mysql`.

## Forgotten password

On the server, run:

```bash
bin/opsnap admin reset-password -c configs/config.yaml        # prompts twice, input hidden
echo 'new-password' | bin/opsnap admin reset-password -c configs/config.yaml --password-stdin
```

For Docker, use `docker exec -it <container> opsnap admin reset-password`. Every signed-in browser is signed out; API tokens keep working.

## Master key

OpsNap encrypts stored credentials with a master key. On first start it creates `master.key` (mode 0600) next to the SQLite database. To supply the key yourself, set `OPSNAP_MASTER_KEY` to 32 random bytes in base64 (for example `openssl rand -base64 32`); the file is then neither read nor written.

**When moving or restoring OpsNap, keep `master.key` (or the `OPSNAP_MASTER_KEY` value) together with the database.** Without it the stored credentials and storage keys cannot be decrypted, and OpsNap refuses to start rather than generate a new key. Your offline copies of the storage keys still open the repositories with the kopia CLI.

## Documentation

- Rules for contributors and AI agents: [`AGENTS.md`](AGENTS.md)
- Developer docs: [`docs/README.md`](docs/README.md)

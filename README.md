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

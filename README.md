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

## Documentation

- Rules for contributors and AI agents: [`AGENTS.md`](AGENTS.md)
- Developer docs: [`docs/README.md`](docs/README.md)

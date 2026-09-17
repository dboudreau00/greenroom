# Greenroom

Demo environment reset and seed tool. Greenroom defines demo state as
versioned seed files per persona or vertical, resets an environment to a
known-good snapshot in one command, runs a pre-call health check across
every dependency, and reports what is stale. It ships as one binary with a
web portal, and attaches to Docker, Kubernetes and EKS, EC2 instances
(including SQL bastions reached through SSM), ssh hosts, Postgres and
MySQL, and plain TCP, TLS and HTTP endpoints.

```bash
greenroom init                  # scaffold a workspace with the bundled example
docker compose -f environments/acme-demo/docker-compose.yml up -d
greenroom check acme-demo       # GREEN / AMBER / RED, exit 0 / 1 / 2
greenroom snapshot acme-demo postgres
greenroom reset acme-demo       # known-good snapshot + pinned seed set
greenroom stale acme-demo       # what drifted since
greenroom serve                 # portal on http://127.0.0.1:8470
```

## What it does

**Versioned seed sets.** `environments/<env>/seeds/<persona>/v<N>/`
holds a `seed.json` manifest and its files: SQL, fixtures, scripts, HTTP
calls. Every file is content hashed; the hash is recorded when the set is
applied, so any later edit shows up as stale. Personas are whatever your
sales motion needs: `cfo`, `retail-ops`, `fintech`, `healthcare`.

**One-command reset.** `greenroom reset <env>` probes everything, heals
what it can, runs each dependency's reset strategy (Postgres template
clone, schema drop, Docker snapshot restore or recreate, Kubernetes
rollout restart, EC2 reboot, or your own script), applies the seed steps,
verifies, and records fingerprints. Snapshots make the restore fast:
`greenroom snapshot` clones a Postgres database into a template, or
commits a container and archives its volumes.

**Pre-call check.** `greenroom check <env>` probes every dependency in
parallel: TCP ports, TLS certificate expiry, HTTP endpoints, Postgres and
MySQL (directly or through a bastion), Docker containers and their
healthchecks, Kubernetes workloads, EC2 instances and their status checks,
arbitrary commands. Critical dependencies down make it RED. The portal's
scheduler runs the same probes on an interval, so every dependency has a
24 hour and 7 day uptime figure and an incident list, and dependencies
marked `autoHeal` are restarted, started or scaled back without anyone
watching.

**Stale report.** `greenroom stale <env>` says why the environment is not
exactly what the seed set promises: never reset, pinned v3 but v2 applied,
seed files changed since the reset (with the file list), reset older than
the limit, container or instance replaced underneath, and drift checks
such as `SELECT count(*) FROM orders` that no longer match the value the
reset left behind.

**Portal.** `greenroom serve` hosts a token-protected web UI over the same
engine: environment cards with readiness, dependency table with sparklines
and heal and snapshot buttons, seed set browser, one-click pre-call check,
reset dialog with persona and version, live run logs over server-sent
events, and a JSON API for automation.

## Attaching to platforms

| Platform | Target kind | Probes | Reset and heal | Seed steps run |
| --- | --- | --- | --- | --- |
| Docker | `docker` (socket, npipe, tcp, tls) | container running and healthy | restart, recreate, snapshot restore; start, restart | docker exec |
| Kubernetes, EKS | `kubernetes` (kubeconfig with exec plugins, in-cluster, explicit token) | deployment, statefulset, daemonset, pod ready | rollout restart; scale | pod exec over WebSocket |
| EC2 | `aws` (env, profile with assume role, IRSA, task role, instance profile) | instance running and status checks | reboot; start | SSM Run Command |
| SSH host | `ssh` | via command probes | via command | ssh with stdin |
| Postgres, MySQL | direct DSN or `via` any target above | connect and version | template, schema, script | driver or psql / mysql on the far side |
| Anything with a port or URL | none needed | tcp, tls, http | command | local |

See [docs/CONFIG.md](docs/CONFIG.md) for every field,
[docs/SEEDS.md](docs/SEEDS.md) for the seed format and what a reset does,
[docs/DEPLOY.md](docs/DEPLOY.md) plus the `deploy/` folders for Docker
Compose, Kubernetes, EKS and EC2, [docs/API.md](docs/API.md) for the HTTP
API, and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how it fits
together.

## Example: a SQL bastion that must stay up

```json
{
  "name": "warehouse-demo",
  "pin": { "persona": "analyst", "version": 3 },
  "staleAfter": "72h",
  "targets": { "aws": { "kind": "aws", "region": "us-east-1" } },
  "dependencies": [
    { "name": "sql-bastion", "kind": "ec2", "target": "aws", "instanceId": "i-0123456789abcdef0",
      "critical": true, "heal": { "action": "start" }, "autoHeal": true },
    { "name": "warehouse", "kind": "postgres", "critical": true,
      "via": { "target": "aws", "instanceId": "i-0123456789abcdef0", "tool": "psql", "dsn": "postgres://demo@warehouse.internal/demo" },
      "reset": { "strategy": "schema" },
      "drift": [ { "name": "orders", "query": "SELECT count(*) FROM orders" } ] },
    { "name": "app", "kind": "http", "url": "https://demo.example.com/healthz", "critical": true },
    { "name": "cert", "kind": "tls", "address": "demo.example.com:443", "warnDays": 21 }
  ]
}
```

The bastion is probed every minute, its uptime is charted, a stopped
instance is started automatically, and the warehouse is seeded by running
`psql` on the bastion through SSM, so the database never has to be
reachable from wherever the portal runs.

## Build

Go 1.26 or newer. No cgo.

```bash
make            # vet, test, build/greenroom
make linux      # cross compile
make docker     # container image
```

On Windows: `.\build.ps1` (add `-All` for cross compiling).

## Layout

```
cmd/greenroom        CLI
internal/            engine, probes, targets, state, server, ui, config, seeds
examples/            a workspace with the acme-demo environment (greenroom -C examples serve)
deploy/              compose, kubernetes, eks, ec2
docs/                reference
```

## License

MIT.

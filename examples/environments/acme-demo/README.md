# Acme demo environment

A self-contained environment for trying Greenroom on a laptop. It runs a
Postgres database, a Redis cache and a static web page with Docker Compose,
and ships two personas with versioned seed sets.

```bash
docker compose -f docker-compose.yml up -d
greenroom check acme-demo
greenroom reset acme-demo            # applies the pinned set, cfo@v2
greenroom snapshot acme-demo postgres  # captures demo_golden as the template
greenroom stale acme-demo
greenroom serve                      # portal on http://127.0.0.1:8470
```

Ports: Postgres 5477, Redis 6390, web 8090. Override with `ACME_PG_PORT`,
`ACME_REDIS_PORT`, `ACME_WEB_PORT` for Compose and `ACME_PG_DSN`,
`ACME_PG_ADDR`, `ACME_WEB_URL` for Greenroom.

The Docker target uses `DOCKER_HOST` when set, otherwise the Unix socket.
On Windows point it at `npipe:////./pipe/docker_engine` or a TCP endpoint,
or run Greenroom inside WSL where the socket is available.

## Seed sets

| Set | What it leaves behind |
| --- | --- |
| cfo@v1 | 6 customers, 10 orders |
| cfo@v2 | 8 customers, 12 orders, invoices, quarter close view (pinned) |
| retail-ops@v1 | 3 stores, inventory, 8 orders |

The `postgres` dependency uses the template strategy: `greenroom snapshot`
clones the current database into `demo_golden`, and every reset drops and
recreates `demo` from it before the seed files run. Until a snapshot exists
the first reset fails with a clear message; run the snapshot once right
after the compose stack comes up (an empty template is fine, the seeds
create everything) or switch the strategy to `schema`.

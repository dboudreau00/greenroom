# Seed sets

A seed set is the versioned, reviewable definition of what a demo should
look like: the SQL, fixtures, scripts and API calls that take an empty or
baseline environment to the exact state a persona's walkthrough expects.

## Layout

```
environments/acme-demo/
  environment.json
  seeds/
    cfo/
      v1/  seed.json  001_schema.sql  002_data.sql
      v2/  seed.json  001_schema.sql  002_data.sql  003_views.sql
    retail-ops/
      v1/  seed.json  001_schema.sql  002_data.sql
```

`seeds/<persona>/v<N>/` is one set. The persona is a name such as `cfo`,
`retail-ops` or a vertical such as `fintech`; the version is an integer.
Every file in the directory, including `seed.json`, is hashed and the hash
is recorded when the set is applied. Editing any file afterwards makes the
environment stale.

Versions never change in place. To evolve a persona, copy `v2` to `v3`,
edit, and bump `pin.version` in `environment.json` when the new set is
ready. Old versions stay applicable, which is what you want when a
prospect's second call needs exactly what they saw on the first.

## `seed.json`

```json
{
  "persona": "cfo",
  "version": 2,
  "title": "CFO persona: quarter-end close",
  "description": "Eight customers, twelve orders, invoices, quarter_close view.",
  "vertical": "fintech",
  "tags": ["finance"],
  "dependencies": ["postgres"],
  "steps": [ ... ],
  "verify": [ ... ]
}
```

`persona` and `version` default to the directory names and must match them
when set. `dependencies` limits which dependencies run their reset strategy
before the steps; when absent, every dependency with a `reset` block is
reset.

## Steps

Steps run in order. A failing step fails the run unless it sets
`continueOnError`. Every step takes `name`, `kind`, optional `timeout`,
`env` (extra variables, `${VAR}` expanded) and `continueOnError`.

| Kind | Fields | What happens |
| --- | --- | --- |
| `sql` | `dependency`, `file` or `sql` | The script runs against a postgres or mysql dependency, directly or through its `via` route. Multi-statement files are fine. psql meta-commands and `COPY FROM STDIN` need a `via` route with the real client. |
| `shell` | `target`, `script` or `file`, location fields | The script is piped to `sh -s` on the target. |
| `command` | `target`, `command` (argv), optional `file` as stdin, location fields | Runs a program on the target. |
| `http` | `url`, `method`, `headers`, `body` or `bodyFile`, `expectStatus`, `insecure` | Sends a request. Handy for fixtures loaded through an admin API. |
| `copy` | `target`, `file`, `dest`, location fields | Copies a file to the target (docker, kubernetes, ssh, ssm, local). |
| `wait` | `dependency`, `timeout` | Polls the dependency's probe until it passes. |

Location fields select where a shell, command or copy step runs:
`container` (docker), `pod` and `namespace` (kubernetes; `pod` may be a
resource such as `deployment/db`), `instanceId` (aws through SSM). With no
`target` the step runs locally.

`${VAR}` is expanded in `url`, `headers`, `command`, `body`, `dest` and
`env` values from the step's `env`, then the environment's `env`, then the
process environment. File contents are used verbatim.

## Verify

`verify` runs after the steps and fails the run when a check does not hold:

```json
"verify": [
  { "name": "orders", "kind": "sql", "dependency": "postgres", "query": "SELECT count(*) FROM orders", "expect": 12 },
  { "name": "rows", "kind": "sql", "dependency": "postgres", "query": "SELECT count(*) FROM quarter_close", "min": 1 },
  { "name": "home", "kind": "http", "url": "http://127.0.0.1:8090/", "expectStatus": 200, "expectBody": "Acme" },
  { "name": "queue", "kind": "command", "target": "docker", "command": ["redis-cli", "-h", "cache", "PING"] }
]
```

## What a reset does

1. Preflight: every dependency is probed. Critical dependencies and the
   ones the set touches must be up; a down dependency with a `heal` block
   is healed first. `--force` continues anyway.
2. Reset strategies run for the chosen dependencies (template clone, schema
   drop, container restore, rollout restart, and so on).
3. Steps run in order with output streamed to the run log.
4. Verify checks run.
5. Every dependency is probed again; fingerprints (container id, image,
   workload generation, instance launch time, certificate serial) and the
   values of drift checks are recorded with the set hash in
   `state/<env>/applied.json`.

A failed reset clears the applied record, so the environment reports as
never reset until a reset succeeds.

## Known-good snapshots

Seeds are the source of truth, but rebuilding a large database from SQL on
every reset is slow. A snapshot captures the state right after a good reset
so the next reset restores it in seconds and then re-runs the seed on top:

```bash
greenroom reset acme-demo
greenroom snapshot acme-demo postgres     # CREATE DATABASE demo_golden TEMPLATE demo
greenroom snapshot acme-demo cache        # docker commit + archived volumes
```

The postgres template strategy restores in one statement. The docker
snapshot recreates the container from the committed image with its named
volumes replaced by the archived content. Bind mounts are overlaid, not
wiped, and read-only mounts are skipped.

## Staleness

`greenroom stale <env>` (and the Stale tab) reports:

| Code | Severity | Meaning |
| --- | --- | --- |
| `never-reset` | stale | No applied record. |
| `wrong-version` | stale | The pinned set differs from the applied one. |
| `seed-changed` | stale | Files of the applied set changed on disk; the changed files are listed. |
| `too-old` | stale | The last reset is older than `staleAfter`. |
| `recreated`, `relaunched` | stale | Container, pod or instance replaced since the reset. |
| `drift` | stale | A drift check value differs from its expectation or its post-reset baseline. |
| `redeployed`, `server-changed`, `output-changed` | warn | Workload generation, database version or probe output changed. |
| `probe-warning` | warn | Certificate expiring, healthcheck starting, and similar. |
| `newer-version` | info | A newer version of the pinned persona exists. |
| `app-changed`, `cert-rotated` | info | Application version or certificate changed. |

Exit code 1 means at least one stale item.

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

Versions never change in place. To evolve a persona, bump it to a new
version, edit the copy, and pin the environment to it when it is ready; see
[Versioning](#versioning). Old versions stay applicable, which is what you
want when a prospect's second call needs exactly what they saw on the
first.

## Versioning

```bash
greenroom seeds bump acme-demo cfo                     # copy the latest cfo set to the next version
greenroom seeds bump acme-demo cfo --from v1 --title "CFO persona: Larkspur close" --pin
greenroom seeds diff acme-demo cfo@v2 cfo@v3           # exit 0 identical, 1 different
greenroom seeds diff acme-demo cfo@v3 retail-ops@v1 --stat
```

`seeds bump` copies `seeds/<persona>/v<from>/` (default: the latest) to
`v<N+1>`, where N is the highest version on disk, so bumping an old version
never overwrites a newer one. Every file is copied except hidden
directories, which the set hash skips too. In the copy's `seed.json` only
the top-level `version` (inserted when absent) and, with `--title`, the
`title` are rewritten; comments, key order and formatting stay as they
were. The copy is built in a hidden directory, loaded, and renamed into
place only when it has no problem the source did not have. The bump is
refused when the target version already exists.

`--pin` points `pin` in `environment.json` at the new version the same
way: the `version` inside the top-level `pin` object is replaced or
inserted, `persona` only when it differs, and the rest of the file,
comments included, is left alone. Without `--pin` the environment keeps
its pin and the new set shows as `newer-version` in the stale report.

A bump only writes files. A reset that is already running keeps the set
it loaded; the next one sees the new version.

`seeds diff <env> <from> <to>` compares two sets of one environment. The
personas may differ. It reports:

- files added, removed, changed and unchanged, by content hash;
- for each changed text file, a unified diff with three lines of context,
  after CRLF is normalized to LF. A file that differs only in line endings
  or a trailing newline says so. A file with a NUL byte in its first 8 KiB,
  or that is not valid UTF-8, is binary: sizes and hashes only. A file diff
  stops at 2,000 lines of output, or diffs only the first 256 KiB of each
  side, and says it was truncated;
- the manifest by meaning: title and description changes, other changed
  top-level fields, and steps and verify checks added, removed or changed
  by name, with the fields that changed.

`--stat` drops the line diffs; `--json` prints the structure the API
returns. In the portal, each version on the Seed sets tab has "Compare with
vN" and each persona "New version".

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
`env` (extra variables, `${VAR}` expanded) and `continueOnError`. `sql`,
`command`, `http` and `copy` steps also take `personalize`, which fills
`{{brand.name}}` and the other brand placeholders at reset time; see
[PERSONALIZE.md](PERSONALIZE.md).

| Kind | Fields | What happens |
| --- | --- | --- |
| `sql` | `dependency`, `file` or `sql` | The script runs against a postgres or mysql dependency, directly or through its `via` route. Multi-statement files are fine. psql meta-commands and `COPY FROM STDIN` need a `via` route with the real client. |
| `shell` | `target`, `script` or `file`, location fields | The script is piped to `sh -s` on the target. |
| `command` | `target`, `command` (argv), optional `file` as stdin, location fields | Runs a program on the target. |
| `http` | `url`, `method`, `headers`, `body` or `bodyFile`, `expectStatus`, `insecure` | Sends a request. Handy for fixtures loaded through an admin API. |
| `copy` | `target`, `file`, `dest`, location fields | Copies a file to the target (docker, kubernetes, ssh, ssm, local). |
| `asset` | `asset`, `target`, `dest`, location fields, optional `file` as fallback | Copies an image from the brand kit chosen for the reset; without one, the fallback file. See [PERSONALIZE.md](PERSONALIZE.md). |
| `wait` | `dependency`, `timeout` | Polls the dependency's probe until it passes. |

Location fields select where a shell, command or copy step runs:
`container` (docker), `pod` and `namespace` (kubernetes; `pod` may be a
resource such as `deployment/db`), `instanceId` (aws through SSM). With no
`target` the step runs locally.

`${VAR}` is expanded in `url`, `headers`, `command`, `body`, `dest` and
`env` values from the step's `env`, then the environment's `env`, then the
process environment. Verify checks expand `url` and `command` the same way,
from the environment's `env` and the process environment. File contents are
used verbatim, unless the step sets `personalize`.

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
| `last-reset-failed` | stale | The most recent reset attempt failed, either before any success or after the applied set. |
| `wrong-version` | stale | The pinned set differs from the applied one. |
| `seed-changed` | stale | Files of the applied set changed on disk; the changed files are listed. |
| `too-old` | stale | The last reset is older than `staleAfter`. |
| `recreated`, `relaunched` | stale | Container, pod or instance replaced since the reset. |
| `drift` | stale | A drift check value differs from its expectation or its post-reset baseline. |
| `drift-error` | stale or warn | A drift check could not be evaluated. Stale when it passed right after the reset (the seeded data is gone), otherwise a warning. |
| `redeployed`, `server-changed`, `output-changed` | warn | Workload generation, database version or probe output changed. |
| `probe-warning` | warn | Certificate expiring, healthcheck starting, and similar. |
| `brand-changed` | stale | The brand kit the reset used was edited or re-fetched since. |
| `brand-missing` | stale | The brand kit the reset used was deleted. |
| `newer-version` | info | A newer version of the pinned persona exists. |
| `app-changed`, `cert-rotated` | info | Application version or certificate changed. |

Exit code 1 means at least one stale item.

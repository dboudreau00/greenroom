# Architecture

```
cmd/greenroom            CLI and "serve"
internal/config          workspace and environment schema, loading, validation, ${VAR} expansion
internal/seeds           seed set discovery, manifest validation, content hashing
internal/templates       built-in and workspace templates, module resolution, rendering
internal/state           applied record, uptime samples, runs, snapshots, heal events (files)
internal/targets         Executor / Copier / Pinger contracts and helpers
  docker/                Docker Engine API over unix, npipe or tcp, exec with hijacked stream
  kube/                  Kubernetes API, kubeconfig JSON + exec plugins, pod exec over WebSocket
  ws/                    minimal RFC 6455 client used by kube
  aws/                   credential chain, SigV4, EC2 query API, SSM Run Command
  ssh/                   system ssh binary
  local/                 os/exec
internal/registry        turns target blocks into clients, once per environment
internal/sqlrun          SQL runner: pgx / mysql driver, or psql / mysql CLI through an executor
internal/probe           one probe per dependency kind, returns Result with fingerprint
internal/engine          check, stale, reset, snapshot, heal, scheduler, run recording, views
internal/server          JSON API, SSE, auth, embedded UI
internal/ui              index.html, app.js, style.css (no build step)
internal/example         embedded acme-demo used by "greenroom init"
```

## Data flow

**Probe.** `probe.Prober.ProbeAll` runs every dependency in parallel with
its own timeout and returns a `Result` per dependency: ok, warn, latency,
message, structured detail and, when the platform exposes one, a
fingerprint (container id and image, workload generation and images,
instance launch time, certificate serial, HTTP version header). Results
become `state.Sample` rows in `samples.jsonl` and the engine's in-memory
"latest" map.

**Check.** `engine.Check` probes, records samples, computes the stale
report and folds both into a readiness of green, amber or red. With
`Record` it is also stored as a run so the portal shows history.

**Stale.** `engine.StaleReport` compares the pin with the applied record
(set id, hash and file digests, age), live fingerprints with the ones
captured after the reset, and optionally evaluates drift checks against
absolute expectations or the baselines recorded at reset.

**Reset.** `engine.runReset` is a recorded run: preflight (probe, heal,
abort unless forced), reset strategies per dependency, steps, verify, then a
post-reset probe that captures fingerprints and baselines into
`applied.json`. Every command's output streams through a `LineSink` into
the run log and to SSE subscribers.

**Scheduler.** `engine.RunScheduler` probes every environment on the
workspace interval, appends samples, triggers auto-heals (respecting the
cooldown and never while a run holds the environment), compacts samples
hourly and prunes old run files.

## Executors

Everything that runs somewhere goes through `targets.Executor`:

```go
Run(ctx, RunSpec{Argv or Script, Stdin, Env, Container/Pod/InstanceID, Timeout}, sink) (Result, error)
```

The same seed step therefore runs unchanged in a container (docker exec),
in a pod (Kubernetes exec over WebSocket with the v5 channel protocol so
stdin can be closed after a large seed file), on an instance (SSM Run
Command, staging large scripts in base64 chunks), over ssh, or locally.

SQL is a runner on top of that: `sqlrun.Open` picks the driver route when
the dependency has a DSN, or the CLI route (`psql -f -`, `mysql --batch`)
through an executor when it has a `via` block. That is how a database that
only listens behind a bastion is seeded and checked.

## State on disk

```
state/<env>/
  applied.json        last successful reset: set id, hash, files, fingerprints, baselines
  samples.jsonl       one probe observation per line, compacted to sampleRetention
  runs/<id>.json      full run with steps and log
  snapshots.json      one record per dependency snapshot
  snapshots/<dep>/    container.json and mount-N.tar for docker snapshots
  heals.json          last 200 heal attempts
```

Writes are atomic (temp file plus rename). The CLI and the portal can share
a state directory; a reset holds a per-environment lock inside one process,
and the applied record is only written on success.

## Why no external dependencies beyond two SQL drivers

The Docker, Kubernetes, AWS and WebSocket clients are small on purpose:
each implements the handful of calls Greenroom makes, with no SDK. The
binary is 13 MB, builds with `CGO_ENABLED=0`, and cross-compiles to every
platform Go supports. The two exceptions are `pgx` and `go-sql-driver/mysql`,
because talking to Postgres and MySQL directly is what makes drift checks
cheap enough to run on every check.

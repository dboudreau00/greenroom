# Configuration reference

Greenroom reads plain JSON. Line comments (`//`) and block comments are
allowed in every file, and string fields that carry hosts or secrets expand
`${VAR}` and `${VAR:-default}` from the process environment and from the
environment's own `env` block.

## Workspace: `greenroom.json`

Sits at the workspace root. Every field is optional.

| Field | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8470` | Portal address. `GREENROOM_LISTEN` overrides. |
| `environmentsDir` | `environments` | Directory holding one sub-directory per environment. |
| `templatesDir` | `templates` | Workspace templates; see [TEMPLATES.md](TEMPLATES.md). |
| `stateDir` | `state` | Where applied records, samples, runs and snapshots are written. |
| `probeInterval` | `60s` | How often the portal's scheduler probes every dependency. |
| `sampleRetention` | `720h` | How long uptime samples are kept. |
| `healCooldown` | `5m` | Minimum gap between heals of the same dependency, manual or automatic. |
| `token` | none | Portal and API token. `GREENROOM_TOKEN` overrides. Without a token the portal only binds to loopback. |
| `linkFetch` | public only | `{ "allowPrivate": false, "timeout": "15s" }`. The client that fetches brand kits from pasted links. `allowPrivate` lets it reach loopback, private and link-local addresses on any port; see [PERSONALIZE.md](PERSONALIZE.md). |

## Environment: `environments/<name>/environment.json`

| Field | Meaning |
| --- | --- |
| `name` | Defaults to the directory name. Lower case letters, digits, `.`, `_`, `-`. |
| `title`, `owner`, `description` | Shown in the portal. |
| `staleAfter` | Age after which the last reset counts as stale. Default `168h`. |
| `pin` | `{ "persona": "cfo", "version": 2 }`. The set a reset applies by default and the one the stale report compares against. `version` 0 or absent means the latest version of that persona. |
| `env` | Extra variables available to `${VAR}` expansion and exported to shell steps. |
| `targets` | Named platform connections, see below. |
| `dependencies` | Ordered list of things the demo needs, see below. |
| `brand` | House brand for resets without a brand kit: `{ "name", "tagline", "domain", "primary", "accent" }`, colors as `#rrggbb`. See [PERSONALIZE.md](PERSONALIZE.md). |
| `template` | Written by `greenroom new`: the template, its version, product type and the modules the environment was built with. Informational. |

### Targets

Each target has a `kind` and kind-specific fields.

**docker**

```json
"docker": { "kind": "docker", "host": "unix:///var/run/docker.sock" }
```

`host` accepts `unix://`, `npipe://` (Windows), `tcp://host:2375` or
`https://host:2376` with `certFile`, `keyFile`, `caFile`. Empty means
`DOCKER_HOST` or the platform default socket. The API version is
negotiated with the engine.

**kubernetes**

```json
"eks": { "kind": "kubernetes", "kubeconfig": "auto", "context": "demo", "namespace": "demo" }
```

`kubeconfig` is `auto` (run `kubectl config view --raw -o json`),
`in-cluster` (service account), or a path to a kubeconfig saved as JSON.
Exec credential plugins such as `aws eks get-token` are supported. Instead
of a kubeconfig, set `server` with `token`, `tokenFile` or `tokenCommand`,
plus `caFile` or `insecure`.

**aws**

```json
"aws": { "kind": "aws", "region": "us-east-1", "profile": "demo" }
```

Credentials come from the environment, a web identity token (EKS IRSA), the
shared credentials and config files (including `role_arn` with
`source_profile`), an ECS task role, or the EC2 instance profile, in that
order.

**ssh**

```json
"bastion": { "kind": "ssh", "host": "bastion.example.com", "user": "ec2-user", "identityFile": "~/.ssh/demo.pem", "port": 22, "args": ["-J", "jump"] }
```

Uses the system `ssh` binary in batch mode. Scripts are streamed on stdin.

**local**

```json
"local": { "kind": "local", "shell": "bash" }
```

Runs on the machine Greenroom runs on. A dependency or step with no
`target` uses the local executor.

### Dependencies

Common fields:

| Field | Meaning |
| --- | --- |
| `name`, `title`, `kind` | Identity. `kind` is one of `tcp`, `tls`, `http`, `postgres`, `mysql`, `docker`, `kubernetes`, `ec2`, `command`. |
| `target` | Target name for docker, kubernetes, ec2 and command kinds. |
| `critical` | A critical dependency that is down makes the environment RED. |
| `timeout` | Probe timeout, default `15s`. |
| `heal` | `{ "action": ..., "command": [...], "script": "...", "target": "...", "replicas": n }`. Actions: `start`, `restart`, `reboot`, `scale`, `rollout-restart`, `command`. |
| `autoHeal` | The scheduler runs the heal when the probe fails, subject to `healCooldown`. |
| `reset` | Strategy run before a seed set applies, see below. |
| `snapshot` | Strategy for `greenroom snapshot`, see below. |
| `drift` | Checks whose value is compared with what the reset left behind. |
| `tags` | Free-form labels. |

Kind-specific fields:

| Kind | Fields |
| --- | --- |
| `tcp` | `address` |
| `tls` | `address`, `warnDays` (default 14), `insecure` |
| `http` | `url`, `method`, `expectStatus`, `expectBody`, `headers`, `insecure` |
| `postgres`, `mysql` | `dsn` or `dsnEnv`, or `via` (below) |
| `docker` | `container` |
| `kubernetes` | `resource` such as `deployment/web`, `statefulset/db`, `pod/x`; `namespace`. Up means every desired replica is ready and the rollout is current; a workload scaled to zero counts as down. |
| `ec2` | `instanceId` |
| `command` | `command` (argv) or `script`; optional `container`, `resource` (pod), `instanceId` for the location |

#### Reaching SQL through a bastion: `via`

```json
"via": { "target": "aws", "instanceId": "i-0123", "tool": "psql", "dsn": "postgres://demo@10.0.1.20/demo" }
```

`via` runs `psql` or `mysql` on the target instead of connecting directly.
Location fields: `container` (docker), `pod` (kubernetes; a resource such as
`deployment/db` resolves to a running pod), `instanceId` (aws through SSM).
With no `dsn` the defaults of the official postgres and mysql images are
used, which is right when the tool runs inside the database container.

#### Reset strategies

| Kind | `reset.strategy` | Effect |
| --- | --- | --- |
| postgres | `template` | Terminate sessions, drop the database, recreate it from `reset.template` (default `<db>_golden`). Needs a snapshot first. |
| postgres | `schema` | Drop and recreate `reset.schema` (default `public`). |
| postgres, mysql | `script` | Run `reset.script`, a SQL file relative to the environment directory. |
| mysql | `schema` | Drop and recreate the database. |
| docker | `restart` | Restart the container and wait for its healthcheck. |
| docker | `recreate` | Remove and recreate the container from its image, keeping named volumes. |
| docker | `snapshot` | Replace the container with the committed snapshot image and restore archived volumes. |
| kubernetes | `rollout-restart` | Roll new pods and wait until ready. |
| ec2 | `reboot` | Reboot and wait for status checks. |
| any | `script`, `command` | Run `reset.script` or `reset.command` through the target's executor. |

`reset.database` names the database when it cannot be read from the
connection.

#### Snapshot strategies

| Kind | `snapshot.strategy` | Effect |
| --- | --- | --- |
| postgres | `template` | `CREATE DATABASE <template> TEMPLATE <db>` after terminating sessions. |
| docker | `commit` | Stop, commit the filesystem to `greenroom/<env>-<dep>:golden` (or `snapshot.tag`), archive writable volume and bind mounts into the state directory, start again. |

#### Drift checks

```json
"drift": [
  { "name": "orders", "query": "SELECT count(*) FROM orders" },
  { "name": "sessions", "query": "SELECT count(*) FROM pg_stat_activity", "max": 25 },
  { "name": "queue", "command": ["redis-cli", "LLEN", "jobs"], "expect": 0, "tolerance": 5 }
]
```

A check with `expect`, `min` or `max` is absolute. A check with none of
them is compared with the value recorded right after the last reset, so any
demo that changes the number shows up as stale. `query` needs a postgres or
mysql dependency; `command` runs through the dependency's target and reads
the first line of output.

`personas` limits a check to the seed sets of those personas, for data that
only some seed sets create. Templates set it on module checks when a
persona leaves the module out. A check without it runs for every seed set.

## Environment variables

| Variable | Effect |
| --- | --- |
| `GREENROOM_TOKEN` | Portal and API token. |
| `GREENROOM_LISTEN` | Portal address. |
| `GREENROOM_INSECURE=1` | Allow a non-loopback listen address without a token. |
| `DOCKER_HOST` | Default docker endpoint. |
| `AWS_REGION`, `AWS_PROFILE`, `AWS_*` | Standard AWS settings. |
| `KUBERNETES_SERVICE_HOST` | Present in a pod; enables in-cluster auth by default. |

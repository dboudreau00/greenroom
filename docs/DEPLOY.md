# Deploying the portal

Greenroom is a single static binary with an embedded web UI. It keeps its
state in a directory and needs network reach to whatever it watches. Pick
the place that can see the most of your demo estate.

| Where | When | Files |
| --- | --- | --- |
| Laptop | one person, local Docker demos | `greenroom serve` |
| Docker Compose | the demo stack already runs under Compose on a host | `deploy/compose/docker-compose.yml` |
| Kubernetes, EKS | demos run in a cluster | `deploy/kubernetes/greenroom.yaml`, `deploy/eks/README.md` |
| EC2 | demos on instances, SQL behind a bastion | `deploy/ec2/user-data.sh`, `deploy/ec2/iam-policy.json` |

## Security model

- Set `GREENROOM_TOKEN`. The portal signs in with it (session cookie,
  `SameSite=Strict`, 12 hour lifetime) and the API accepts it as a bearer
  token. Without a token the portal refuses to bind to anything but
  loopback unless `--insecure` is passed.
- Mutating calls from the browser need the `X-Requested-With: greenroom`
  header, which the UI sends; that blocks cross-site form posts.
- Secrets never leave the process: DSN passwords and authorization headers
  are redacted in every API response and run log.
- Put TLS in front (load balancer, ingress, Caddy). Server-sent events need
  proxy buffering off.
- The portal's permissions are exactly what its targets grant it: the
  Docker socket, a Kubernetes Role, an IAM policy scoped to tagged
  instances. See the deploy folders for least-privilege examples.

## Docker Compose

```bash
GREENROOM_TOKEN=$(openssl rand -hex 24) \
GREENROOM_WORKSPACE=/srv/greenroom \
DOCKER_GID=$(getent group docker | cut -d: -f3) \
docker compose -f deploy/compose/docker-compose.yml up -d
```

The image ships `psql` and `mysql`, so a `via` route with `target: local`
works from inside the container as well.

## Kubernetes and EKS

See `deploy/kubernetes/README.md` and `deploy/eks/README.md`. In short:
service account, a namespaced Role with get, list, patch on workloads and
create on `pods/exec`, a PVC for state, a ConfigMap or volume for the
workspace, and IRSA when the cluster's demos also involve EC2 or SSM.

## EC2

See `deploy/ec2/README.md`. The user data script installs the binary as a
systemd service; the instance profile carries the IAM policy; managed
instances are tagged `greenroom=managed` and run the SSM agent.

## Upgrades

State files are JSON and forward compatible. Replace the binary and
restart; the scheduler resumes from the samples on disk.

## Running the CLI against a remote portal's workspace

The CLI works on the workspace directory, not on the portal. For a shared
setup, run the CLI on the same host or mount the same workspace and state
directory, or call the API:

```bash
curl -s -H "Authorization: Bearer $GREENROOM_TOKEN" -X POST \
  https://greenroom.example.com/api/v1/environments/acme-demo/check | jq .readiness
```

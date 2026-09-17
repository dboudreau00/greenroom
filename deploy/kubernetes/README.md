# Greenroom on Kubernetes

`greenroom.yaml` deploys one portal replica with a persistent state volume,
a service account, and a Role in the `demo` namespace that allows exactly
what the kubernetes target needs: read workloads, patch them (scale and
rollout restart), list and delete pods, and exec into pods.

```bash
kubectl create namespace greenroom
kubectl -n greenroom create secret generic greenroom --from-literal=token="$(openssl rand -hex 24)"
kubectl -n greenroom create configmap greenroom-workspace \
  --from-file=greenroom.json=examples/greenroom.json \
  --from-file=environments/
kubectl apply -f deploy/kubernetes/greenroom.yaml
kubectl -n greenroom port-forward svc/greenroom 8470:80
```

Then in `environment.json`:

```json
"targets": {
  "cluster": { "kind": "kubernetes", "kubeconfig": "in-cluster", "namespace": "demo" }
}
```

## Notes

- Seed files live in the ConfigMap. A ConfigMap is capped at 1 MiB; for
  larger seed sets mount a volume populated by an init container or a
  git-sync sidecar, and point `environmentsDir` at it.
- `pods/exec` uses the WebSocket endpoint. Kubernetes 1.30+ negotiates
  `v5.channel.k8s.io`, which lets Greenroom close stdin after streaming a
  seed file of any size. Older API servers fall back to inlining inputs
  under 60 KB.
- Databases that only listen inside the cluster are reached with a `via`
  block on the dependency: `{"target": "cluster", "pod": "statefulset/db",
  "tool": "psql"}` runs the client inside the database pod.
- Behind an ingress, turn off proxy buffering for the run log stream
  (server-sent events). The annotations in the manifest cover nginx ingress.
- For EKS with IRSA, annotate the service account with the role ARN and give
  that role `deploy/ec2/iam-policy.json`; the AWS client picks the web
  identity token up automatically.

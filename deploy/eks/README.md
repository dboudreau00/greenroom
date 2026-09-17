# Greenroom on EKS

Two ways to attach:

## Run the portal inside the cluster

Apply `deploy/kubernetes/greenroom.yaml`. With `"kubeconfig": "in-cluster"`
the kubernetes target authenticates with the pod's service account, so the
Role in the manifest is the only permission it needs. For ec2 and ssm
targets (the SQL bastion outside the cluster), use IRSA:

```bash
eksctl create iamserviceaccount \
  --cluster demo --namespace greenroom --name greenroom \
  --attach-policy-arn arn:aws:iam::123456789012:policy/greenroom \
  --approve
```

with the policy created from `deploy/ec2/iam-policy.json`. The AWS client
reads `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN` that IRSA injects and
assumes the role through STS with no extra configuration.

## Run the portal elsewhere and point it at EKS

On a laptop, an EC2 instance or in another cluster, use the kubeconfig that
`aws eks update-kubeconfig` writes. Its exec plugin (`aws eks get-token`) is
supported:

```json
"targets": {
  "eks": { "kind": "kubernetes", "kubeconfig": "auto", "context": "arn:aws:eks:us-east-1:123456789012:cluster/demo", "namespace": "demo" }
}
```

`"auto"` runs `kubectl config view --raw -o json` for the named context and
executes the plugin whenever a token is needed. To avoid depending on
kubectl, export the config once:

```bash
kubectl config view --raw -o json --minify --context <ctx> > /etc/greenroom/eks.json
```

and set `"kubeconfig": "/etc/greenroom/eks.json"`. The identity running
Greenroom must be mapped in the cluster (aws-auth ConfigMap or EKS access
entries) to a group bound to the Role in `deploy/kubernetes/greenroom.yaml`.

## Reaching RDS through a bastion

A database inside the VPC is usually not reachable from where the portal
runs. Route SQL through something that can reach it:

- a pod in the cluster: `"via": {"target": "eks", "pod": "deployment/psql-toolbox", "tool": "psql", "dsn": "postgres://demo@rds.internal/demo"}`
- the SQL bastion instance through SSM: `"via": {"target": "aws", "instanceId": "i-...", "tool": "psql", "dsn": "..."}`

Seed SQL is streamed to the client on the far side; nothing is written to
disk unless a step asks for it.

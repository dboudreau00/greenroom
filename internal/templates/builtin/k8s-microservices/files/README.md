# {{title}}

Created from the `{{template}}` template with these modules: {{modules}}.

Greenroom talks to the cluster through the `{{context}}` kubectl context
and the `{{namespace}}` namespace. It expects these workloads:

- `deployment/gateway`
- `statefulset/postgres` running the official postgres image
- one deployment per module (`deployment/catalog`, `deployment/orders`, ...)

Seed SQL runs through `psql` inside the postgres pod over the exec API, so
the database never needs to be reachable from where Greenroom runs. The
service account needs get, list and patch on workloads and create on
`pods/exec` in the namespace (see `deploy/kubernetes` in the Greenroom
repository).

```bash
greenroom targets {{env}}
greenroom check {{env}}
greenroom reset {{env}}
```

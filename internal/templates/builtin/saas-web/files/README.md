# {{title}}

Created from the `{{template}}` template with these modules: {{modules}}.

```bash
docker compose -f environments/{{env}}/docker-compose.yml up -d
greenroom check {{env}}
greenroom reset {{env}}
```

| Service | Host port |
| --- | --- |
| Postgres | {{dbPort}} |
| Redis | {{cachePort}} |
| Web | {{webPort}} |

Each persona under `seeds/` is one seed set built from the modules it
walks through. Edit them like any other seed set: copy `v1` to `v2`,
change it, and bump `pin.version` when it is ready.

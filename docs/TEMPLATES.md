# Environment templates

A template is a pre-rolled demo environment for one type of product. It
knows the dependencies every demo of that product needs and splits the rest
into product modules: billing, analytics, a data mart, a microservice. Pick
the modules the call is going to show and Greenroom writes an environment
with exactly those dependencies, drift checks, seed steps and verify
checks, plus one seed set per persona.

```bash
greenroom templates                       # what is available
greenroom templates saas-web              # modules, personas, params
greenroom new acme-crm --template saas-web --modules billing,support \
  --persona cfo --title "Acme CRM" --set webPort=8096
greenroom new trial --template k8s-microservices --modules payments --dry-run
```

In the portal, **New environment** on the overview does the same with a
template picker, module checkboxes and the template settings.

## Built-in templates

| Template | Product | Modules | Personas |
| --- | --- | --- | --- |
| `saas-web` | SaaS web app on Docker Compose: Postgres, Redis, nginx | accounts (always), billing, analytics, support, inventory | overview, cfo, support-lead, ops |
| `warehouse-bastion` | Analytics warehouse behind an EC2 bastion, psql over SSM | sales, marketing, finance marts | analyst, cmo, controller |
| `k8s-microservices` | Commerce platform on Kubernetes or EKS, one deployment per module | catalog (always), orders, payments, search | platform, commerce, merchandising |

`saas-web` also writes a `docker-compose.yml`, so a new environment is one
`docker compose up -d` away from green.

## How modules become an environment

1. **Modules.** Required modules are always in. With no modules given, the
   template's defaults are used. A module that needs another (payments
   needs orders, orders needs catalog) pulls it in.
2. **Dependencies.** The template's base dependencies, then each module's
   own (a deployment per service, for example).
3. **Drift checks.** A module adds drift checks to existing dependencies.
   When some personas leave a module out, its checks are scoped to the
   personas that include it (`"personas": [...]` on the check), so a seed
   set that never creates the `invoices` table is not reported as drifted.
4. **Seed sets.** Every persona that shares at least one module with the
   selection becomes `seeds/<persona>/v1/`: the base steps, each module's
   steps in template order, then the base `after` steps. Module files are
   copied under a directory named after the module.
5. **Pin.** The persona you choose, or the first one that fits.
6. **Validation.** Files are written to a hidden directory, loaded with
   the same code a reload uses, and only moved into place when the
   environment and every seed set load without problems.

The environment file records where it came from:

```json
"template": { "name": "saas-web", "version": 1, "productType": "SaaS web app", "modules": ["accounts", "billing", "support"] }
```

From then on it is an ordinary environment. Edit it, version its seed sets
and commit it like any other.

## Writing a template

Put it in `templates/<name>/` in the workspace (`templatesDir` in
`greenroom.json`). A workspace template with the same name as a built-in
one replaces it.

```
templates/my-product/
  template.json
  files/              copied next to environment.json (compose, web, README)
  seed/               files the base steps reference
  modules/<module>/   files each module's steps reference
```

```json
{
  "name": "my-product",
  "version": 1,
  "title": "My product on Docker",
  "productType": "Data platform",
  "params": [
    { "name": "dbPort", "title": "Postgres port", "default": "5490", "pattern": "^[0-9]{2,5}$" }
  ],
  "environment": {
    "env": { "PG_DSN": "postgres://demo:demo@127.0.0.1:{{dbPort}}/demo?sslmode=disable" },
    "dependencies": [ { "name": "db", "kind": "postgres", "dsn": "${PG_DSN}", "reset": { "strategy": "schema" } } ]
  },
  "seed": {
    "steps": [ { "name": "base", "kind": "sql", "dependency": "db", "file": "seed/001_base.sql" } ],
    "after": [],
    "verify": []
  },
  "modules": [
    {
      "name": "ingest", "title": "Ingestion", "required": true,
      "dependencies": [],
      "drift": { "db": [ { "name": "rows", "query": "SELECT count(*) FROM raw_events" } ] },
      "steps": [ { "name": "ingest-data", "kind": "sql", "dependency": "db", "file": "modules/ingest/001.sql" } ],
      "verify": [ { "name": "rows", "kind": "sql", "dependency": "db", "query": "SELECT count(*) FROM raw_events", "min": 1 } ]
    },
    { "name": "reports", "title": "Reports", "default": true, "requires": ["ingest"], "steps": [] }
  ],
  "personas": [
    { "name": "tour", "title": "Everything selected", "modules": ["*"] },
    { "name": "analyst", "title": "Analyst", "modules": ["reports"] }
  ]
}
```

### Placeholders

`{{name}}` is replaced in the environment block, in steps and in every text
file the template copies. Built in: `env` (environment name), `envSlug`
(name with dots turned into dashes, for Compose project names), `title`,
`owner`, `modules` (module titles, comma separated) and `template`. Every
other name must be a declared param.

Values are checked before they are used:

- a param must match its `pattern`; without one, only letters, digits and
  `. _ : / @ -` are allowed, so a value can not open a shell, SQL or YAML
  construct or reference `${VAR}`;
- the title and owner allow letters, digits, spaces and `. , & ( ) _ + -`;
- in `.html`, `.htm`, `.xml` and `.svg` files values are HTML-escaped;
- in `environment.json` and `seed.json` values are set as JSON strings, not
  spliced into text.

Dotted names such as `{{brand.name}}` are reserved and left untouched. An
unknown undotted placeholder fails the render.

A step's `file` must stay inside `seed/` (base steps) or the module's own
`modules/<name>/` directory.
